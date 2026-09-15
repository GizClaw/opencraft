package core

import (
	"context"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// TestApplyDocumentReloadEmitsReady pins the signal a document-only save
// depends on. Saving inference instances, memory, or MCP rewrites the
// user layer and applies it in place: the Host keeps serving, so nothing
// else tells the UI that the document it reads — the composer's model
// list, the default reasoning flag, the session defaults — changed. The
// frontend refreshes those on the ready event alone, which is why the
// in-place path has to emit it.
func TestApplyDocumentReloadEmitsReady(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "done"})
	workDir := t.TempDir()
	configDir := t.TempDir()
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeProviderConfig(t, configDir, provider.URL())

	c := NewCore(configDir, t.TempDir(), "")
	ctx := context.Background()
	c.SetWorkDir(workDir)
	if err := c.RebuildRuntime(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	host := c.Runtime.Current()
	if host == nil {
		t.Fatal("no current host after rebuild")
	}

	var (
		mu     sync.Mutex
		events []string
	)
	c.Shell.SetPetSink(func(typ string, _ any) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, typ)
	})

	if err := c.ApplyDocumentReload(ctx); err != nil {
		t.Fatalf("document reload: %v", err)
	}
	// The in-place branch is the one under test: a rebuild would replace
	// the current Host and emit ready through RebuildRuntime instead.
	if got := c.Runtime.Current(); got != host {
		t.Fatal("document reload replaced the host; want the in-place path")
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Contains(events, "ready") {
		t.Fatalf("emitted %v, want a ready event", events)
	}
}

// TestRebuildRuntimeReplacesRetiredHostAfterSwitchBack pins the
// lifecycle gap where a workspace switch away and back left
// Runtime.current pointing at a closed Host. The scenario:
//
//   - workdir A has a live turn when the active workspace moves to B;
//   - A's Host is marked stale but keeps serving until the turn ends;
//   - the active workspace moves back to A before the turn ends, so
//     Acquire hands the stale Host out again and it becomes current;
//   - once the last turn ends, that Host retires and closes itself.
//
// RebuildRuntime must arm the idle-rebuild watcher whenever Acquire
// returns a stale Host for the active workspace, not only when that
// Host is the same object that was current before the reload. Without
// the watcher nothing reassembles A after teardown and StartRun keeps
// failing with "host: runtime is closing" until an unrelated rebuild.
func TestRebuildRuntimeReplacesRetiredHostAfterSwitchBack(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "done"})
	workA := t.TempDir()
	workB := t.TempDir()
	dataDir := t.TempDir()
	configDir := t.TempDir()
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeProviderConfig(t, configDir, provider.URL())

	c := NewCore(configDir, dataDir, "")
	ctx := context.Background()

	// Workdir A hosts the long turn.
	c.SetWorkDir(workA)
	if err := c.RebuildRuntime(ctx); err != nil {
		t.Fatalf("rebuild for A: %v", err)
	}
	hostA := c.Runtime.Current()
	if hostA == nil {
		t.Fatal("no current host after rebuilding A")
	}
	if hostA.IsStale() {
		t.Fatal("freshly assembled host A must not be stale")
	}

	gate := provider.HoldNext()
	run, err := hostA.StartRun(ctx, host.RunOptions{
		Message:       message.NewTextMessage(message.RoleUser, "long turn"),
		SkipAutoTitle: true,
	})
	if err != nil {
		t.Fatalf("start run on A: %v", err)
	}
	defer gate.Release()
	select {
	case <-gate.Ready():
	case <-time.After(30 * time.Second):
		t.Fatal("run did not reach the provider")
	}

	// Switch to B while the A turn is in flight: A becomes stale but
	// keeps serving its live run.
	c.SetWorkDir(workB)
	if err := c.RebuildRuntime(ctx); err != nil {
		t.Fatalf("rebuild for B: %v", err)
	}
	if got := c.Runtime.Current(); got == nil || got == hostA {
		t.Fatalf("current host after switch to B = %p, want a B host", got)
	}
	if !hostA.IsStale() {
		t.Fatal("host A must be stale while its run is live")
	}

	// Switch back to A before the turn ends: Acquire hands out the
	// draining Host again and RebuildRuntime must schedule the
	// replacement for after teardown.
	c.SetWorkDir(workA)
	if err := c.RebuildRuntime(ctx); err != nil {
		t.Fatalf("rebuild for A after switch-back: %v", err)
	}
	if got := c.Runtime.Current(); got != hostA {
		t.Fatalf("current host after switch back to A = %p, want draining host A (%p)",
			got, hostA)
	}

	// Finish the turn: host A retires and closes itself once its last
	// run drains.
	gate.Release()
	if res, err := run.Wait(ctx); err != nil || res == nil ||
		res.Status != "completed" {
		t.Fatalf("run wait = %v, %v; want completed", res, err)
	}

	// The background watcher must install a fresh, usable Host for A.
	deadline := time.Now().Add(45 * time.Second)
	for {
		h := c.Runtime.Current()
		if h != nil && h != hostA && !h.IsStale() {
			second, err := h.StartRun(ctx, host.RunOptions{
				Message: message.NewTextMessage(
					message.RoleUser, "after retire"),
				SkipAutoTitle: true,
			})
			if err != nil {
				t.Fatalf("start run on replacement host: %v", err)
			}
			if res, err := second.Wait(ctx); err != nil ||
				res == nil || res.Status != "completed" {
				t.Fatalf("replacement run wait = %v, %v; want completed",
					res, err)
			}
			if got := provider.Calls(); got < 2 {
				t.Fatalf("provider calls = %d, want at least 2 "+
					"(original + replacement runs)", got)
			}
			return
		}
		if time.Now().After(deadline) {
			cur := c.Runtime.Current()
			stale := cur != nil && cur.IsStale()
			t.Fatalf(
				"current host was never replaced after retire "+
					"(current=%p stale=%v); StartRun stays broken with "+
					"host: runtime is closing",
				cur, stale)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestEnsureUsableHostRebuildsRetiredWorkspace covers the recovery
// primitive behind the binding retry: once a stale Host retired while
// another workspace was current, EnsureUsableHost must wait out the
// teardown and assemble a fresh Host for the original workspace that
// immediately accepts new turns.
func TestEnsureUsableHostRebuildsRetiredWorkspace(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "done"})
	workA := t.TempDir()
	workB := t.TempDir()
	dataDir := t.TempDir()
	configDir := t.TempDir()
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeProviderConfig(t, configDir, provider.URL())

	c := NewCore(configDir, dataDir, "")
	ctx := context.Background()

	c.SetWorkDir(workA)
	if err := c.RebuildRuntime(ctx); err != nil {
		t.Fatalf("rebuild for A: %v", err)
	}
	hostA := c.Runtime.Current()
	if hostA == nil {
		t.Fatal("no current host after rebuilding A")
	}

	gate := provider.HoldNext()
	run, err := hostA.StartRun(ctx, host.RunOptions{
		Message:       message.NewTextMessage(message.RoleUser, "long turn"),
		SkipAutoTitle: true,
	})
	if err != nil {
		t.Fatalf("start run on A: %v", err)
	}
	defer gate.Release()
	select {
	case <-gate.Ready():
	case <-time.After(30 * time.Second):
		t.Fatal("run did not reach the provider")
	}

	// Move to B while A's run is live: A becomes stale but keeps
	// serving until the run ends, then retires with B still current
	// (no replacement is armed for a workspace that is no longer
	// active).
	c.SetWorkDir(workB)
	if err := c.RebuildRuntime(ctx); err != nil {
		t.Fatalf("rebuild for B: %v", err)
	}
	if got := c.Runtime.Current(); got == nil || got == hostA {
		t.Fatalf("current host after switch to B = %p, want a B host", got)
	}

	gate.Release()
	if res, err := run.Wait(ctx); err != nil || res == nil ||
		res.Status != "completed" {
		t.Fatalf("run wait = %v, %v; want completed", res, err)
	}
	if !hostA.IsClosing() {
		t.Fatal("host A must have retired after its last run ended")
	}

	// EnsureUsableHost must assemble a replacement for A even though
	// no rebuild was armed while B was current.
	h, err := c.Runtime.EnsureUsableHost(ctx, workA)
	if err != nil {
		t.Fatalf("ensure usable host for A: %v", err)
	}
	if h == nil || h == hostA {
		t.Fatalf("EnsureUsableHost returned %p, want a fresh host", h)
	}
	if h.IsClosing() || h.IsStale() {
		t.Fatalf("replacement host closing=%v stale=%v, want usable host",
			h.IsClosing(), h.IsStale())
	}
	if h.WorkDir() != workA {
		t.Fatalf("replacement host workdir = %q, want %q", h.WorkDir(), workA)
	}
	if got := c.Runtime.Current(); got != h {
		t.Fatalf("current after EnsureUsableHost = %p, want %p", got, h)
	}

	second, err := h.StartRun(ctx, host.RunOptions{
		Message:       message.NewTextMessage(message.RoleUser, "recovered"),
		SkipAutoTitle: true,
	})
	if err != nil {
		t.Fatalf("start run on replacement host: %v", err)
	}
	if res, err := second.Wait(ctx); err != nil || res == nil ||
		res.Status != "completed" {
		t.Fatalf("replacement run wait = %v, %v; want completed", res, err)
	}
}
