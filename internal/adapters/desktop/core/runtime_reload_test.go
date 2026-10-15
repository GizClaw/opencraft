package core

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/inference/inferencetest"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/testing/configseed"
	"github.com/GizClaw/opencraft/internal/testing/logcapture"
)

// TestApplyDocumentReloadEmitsReady pins the signal a document-only save
// depends on. Saving inference instances, memory, or MCP rewrites the
// user layer and applies it in place: the Host keeps serving, so nothing
// else tells the UI that the document it reads — the composer's model
// list, the default reasoning flag, the session defaults — changed. The
// frontend refreshes those on the ready event alone, which is why the
// in-place path has to emit it.
func TestApplyDocumentReloadEmitsReady(t *testing.T) {
	provider := inferencetest.NewScriptedOpenAI(t, inferencetest.ScriptedReply{Text: "done"})
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
	host := c.ActiveHost()
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
	if got := c.ActiveHost(); got != host {
		t.Fatal("document reload replaced the host; want the in-place path")
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Contains(events, "ready") {
		t.Fatalf("emitted %v, want a ready event", events)
	}
}

// TestApplyDocumentReloadLogsRebuildFallback pins the diagnostic for the
// expensive branch: when the in-place swap refuses the document, the
// save falls back to a full runtime rebuild, and the refusal reason gets
// a log line of its own instead of being dropped. That line (with its
// reason attribute) is what makes "why did a settings save reassemble
// everything" answerable after the fact.
func TestApplyDocumentReloadLogsRebuildFallback(t *testing.T) {
	provider := inferencetest.NewScriptedOpenAI(t, inferencetest.ScriptedReply{Text: "done"})
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
	if c.ActiveHost() == nil {
		t.Fatal("no current host after rebuild")
	}

	// Clear the inference configuration so the in-place swap has to
	// refuse the document ("inference router is not configured") and the
	// save goes down the rebuild path.
	if err := configseed.Write(configDir, config.InferenceConfig{}); err != nil {
		t.Fatal(err)
	}

	recorder := logcapture.Install(t)
	reloadCtx := host.WithAssemblyReason(ctx, host.ReasonInferenceChange)
	if err := c.ApplyDocumentReload(reloadCtx); err != nil {
		t.Fatalf("document reload: %v", err)
	}

	var found bool
	for _, record := range recorder.Records() {
		body := record.Body().AsString()
		if body == "host: document reloaded in place" {
			t.Fatal("unconfigured router still took the in-place path")
		}
		if body != "host: in-place document reload unavailable; rebuilding" {
			continue
		}
		found = true
		if got := logcapture.Attribute(record, "reason"); got != "inference_change" {
			t.Fatalf("fallback reason = %q, want inference_change", got)
		}
		if got := logcapture.Attribute(record, "workspace"); got != workDir {
			t.Fatalf("fallback workspace = %q, want %q", got, workDir)
		}
		if got := logcapture.Attribute(record, "error.message"); !strings.Contains(got, "router") {
			t.Fatalf("fallback error = %q, want the router refusal", got)
		}
	}
	if !found {
		t.Fatalf("no fallback record emitted; bodies: %v", recorder.Bodies())
	}
}

// TestRebuildRuntimeReplacesRetiredHostAfterSwitchBack pins the
// lifecycle gap where a workspace switch away and back left the
// workspace with a Host that closes itself and nothing scheduled in its
// place. The scenario:
//
//   - workdir A has a live turn when the active workspace moves to B;
//   - A's Host is marked stale but keeps serving until the turn ends;
//   - the active workspace moves back to A before the turn ends, so the
//     pool hands the stale Host out again and A is served by a
//     generation this reload already retired;
//   - once the last turn ends, that Host retires and closes itself.
//
// RebuildRuntime must schedule a replacement whenever the active
// workspace's Host is stale, not only when it is the Host that was
// serving the window at the start of the reload. Without the
// replacement nothing reassembles A after teardown and StartRun keeps
// failing with "host: runtime is closing" until an unrelated rebuild.
func TestRebuildRuntimeReplacesRetiredHostAfterSwitchBack(t *testing.T) {
	provider := inferencetest.NewScriptedOpenAI(t, inferencetest.ScriptedReply{Text: "done"})
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
	hostA := c.ActiveHost()
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
	if got := c.ActiveHost(); got == nil || got == hostA {
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
	if got := c.ActiveHost(); got != hostA {
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
		h := c.ActiveHost()
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
			cur := c.ActiveHost()
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

// TestEnsureHostRebuildsRetiredWorkspace covers the recovery
// primitive behind the binding retry: once a stale Host retired while
// another workspace was on screen, EnsureHost must wait out the
// teardown and assemble a fresh Host for the original workspace that
// immediately accepts new turns.
func TestEnsureHostRebuildsRetiredWorkspace(t *testing.T) {
	provider := inferencetest.NewScriptedOpenAI(t, inferencetest.ScriptedReply{Text: "done"})
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
	hostA := c.ActiveHost()
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
	if got := c.ActiveHost(); got == nil || got == hostA {
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

	// EnsureHost must assemble a replacement for A even though no
	// rebuild was armed while B was on screen.
	h, err := c.Runtime.EnsureHost(ctx, workA)
	if err != nil {
		t.Fatalf("ensure usable host for A: %v", err)
	}
	if h == nil || h == hostA {
		t.Fatalf("EnsureHost returned %p, want a fresh host", h)
	}
	if h.IsClosing() || h.IsStale() {
		t.Fatalf("replacement host closing=%v stale=%v, want usable host",
			h.IsClosing(), h.IsStale())
	}
	if h.WorkDir() != workA {
		t.Fatalf("replacement host workdir = %q, want %q", h.WorkDir(), workA)
	}
	// The replacement serves A, which is not the workspace on screen.
	if got := c.Runtime.HostFor(workA); got != h {
		t.Fatalf("host for A after EnsureHost = %p, want %p", got, h)
	}
	if got := c.ActiveHost(); got == h {
		t.Fatalf("ensuring A moved the window's Host to %p", got)
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

// TestSetWorkDirStaysSpelledLikeTheHistoryRows pins the spelling the
// window switches on. A workspace reaches the window as the string
// ActiveWorkDir carries in the ready event, and the window compares it
// against the history row the user clicked, character for character. A
// path the window hands in — the picker's fallback input, a paste —
// travelled into that string verbatim while the row was written with
// filepath.Clean: the sidebar then took every row of the active
// workspace for another workspace's, and clicking one became a switch
// the backend answered with "already announced" while the window waited
// out its deadline and reported "workspace switch did not complete". A
// launch flag cannot be the source, whatever spelling it carries:
// config.ResolveLaunch absolutizes the flag on the way in.
func TestSetWorkDirStaysSpelledLikeTheHistoryRows(t *testing.T) {
	configDir := t.TempDir()
	dataDir := t.TempDir()
	workDir := t.TempDir()
	c := NewCore(configDir, dataDir, "")

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(cwd, workDir)
	if err != nil {
		t.Fatalf("relative spelling of %q from %q: %v", workDir, cwd, err)
	}
	for _, spelling := range []string{
		workDir + "/",
		workDir + "//",
		filepath.Join(workDir, "."),
		filepath.Join(workDir, "sub", ".."),
		rel,
	} {
		c.SetWorkDir(spelling)
		if got := c.ActiveWorkDir(); got != workDir {
			t.Fatalf("ActiveWorkDir after SetWorkDir(%q) = %q, want %q",
				spelling, got, workDir)
		}
	}

	// No workspace keeps comparing as none: cleaning a blank path must
	// not name the process's own directory.
	c.SetWorkDir("   ")
	if got := c.ActiveWorkDir(); got != "" {
		t.Fatalf("ActiveWorkDir of a blank path = %q, want it empty", got)
	}

	// The history record for the same directory — what the rows and the
	// ready event are both read from — is that same string, for every
	// spelling the two halves are handed independently.
	for _, spelling := range []string{workDir + "/", rel} {
		c.SetWorkDir(spelling)
		c.RecordWorkspace(spelling)
		metas, err := c.Workspaces()
		if err != nil {
			t.Fatalf("workspaces: %v", err)
		}
		if len(metas) != 1 {
			t.Fatalf("workspaces = %d entries for one directory, want 1",
				len(metas))
		}
		if metas[0].Path != workDir {
			t.Fatalf("history path after RecordWorkspace(%q) = %q, want %q",
				spelling, metas[0].Path, workDir)
		}
		if metas[0].Path != c.ActiveWorkDir() {
			t.Fatalf("history path %q != active workspace %q after %q: the "+
				"window would take its own rows for another workspace's",
				metas[0].Path, c.ActiveWorkDir(), spelling)
		}
	}
}
