package core

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
	"github.com/GizClaw/opencraft/internal/testing/logcapture"
)

// TestRebuildRuntimeArmsOneReplacementForDrainingWorkspace pins the
// rebuild-storm guard. A save, a workspace switch and a plugin write can
// all reload the same workspace while it is still draining a live turn;
// each reload marks the busy Host stale, and before this every one of
// them armed its own watcher. When the drain finished they all woke at
// once and assembled a replacement each — one runtime installed and the
// rest built and closed again, which is the storm the log showed as
// several `retry_after_drain` assemblies in the same millisecond.
//
// The armed watcher always builds from the document on disk when it
// runs, so one replacement is both necessary and sufficient here.
func TestRebuildRuntimeArmsOneReplacementForDrainingWorkspace(t *testing.T) {
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
	first := c.ActiveHost()
	if first == nil {
		t.Fatal("no current host after rebuild")
	}

	gate := provider.HoldNext()
	run, err := first.StartRun(ctx, host.RunOptions{
		Message:       message.NewTextMessage(message.RoleUser, "hold"),
		SkipAutoTitle: true,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	defer gate.Release()
	select {
	case <-gate.Ready():
	case <-time.After(30 * time.Second):
		t.Fatal("run did not reach the provider")
	}

	recorder := logcapture.Install(t)
	// Five reloads land while the turn is live: the workspace is stale
	// and stays busy for every one of them.
	for i := 0; i < 5; i++ {
		reloadCtx := host.WithAssemblyReason(ctx, host.ReasonInferenceChange)
		if err := c.RebuildRuntime(reloadCtx); err != nil {
			t.Fatalf("rebuild %d: %v", i, err)
		}
	}

	gate.Release()
	if res, err := run.Wait(ctx); err != nil || res == nil ||
		res.Status != "completed" {
		t.Fatalf("run wait = %v, %v; want completed", res, err)
	}

	deadline := time.Now().Add(45 * time.Second)
	for {
		h := c.ActiveHost()
		if h != nil && h != first && !h.IsStale() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no replacement host was installed after the drain")
		}
		time.Sleep(50 * time.Millisecond)
	}

	var assembled, retried int
	for _, record := range recorder.Records() {
		if record.Body().AsString() != "host: runtime assembled" {
			continue
		}
		assembled++
		if logcapture.Attribute(record, "reason") == "retry_after_drain" {
			retried++
		}
	}
	if assembled != 1 || retried != 1 {
		t.Fatalf(
			"assemblies = %d (retry_after_drain = %d), want exactly one "+
				"replacement for five invalidations", assembled, retried)
	}
}

// TestUnchangedPluginWriteSkipsRebuildWhileReplacementArmed pins the
// capability-plugin contract that a catalog sync does not reassemble the
// runtime. The SSO plugin re-submits its unchanged inference rows every
// time its panel becomes visible and on startup; the host has to answer
// that with nothing, including while the active workspace is draining
// (a replacement is already armed and reads the same document). A write
// that really changed the document still rebuilds.
func TestUnchangedPluginWriteSkipsRebuildWhileReplacementArmed(t *testing.T) {
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
	first := c.ActiveHost()
	if first == nil {
		t.Fatal("no current host after rebuild")
	}
	// The unchanged-write skip is only allowed after a rebuild that
	// actually reached a runtime.
	c.pluginWrites.markApplied(nil)

	gate := provider.HoldNext()
	run, err := first.StartRun(ctx, host.RunOptions{
		Message:       message.NewTextMessage(message.RoleUser, "hold"),
		SkipAutoTitle: true,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	defer gate.Release()
	select {
	case <-gate.Ready():
	case <-time.After(30 * time.Second):
		t.Fatal("run did not reach the provider")
	}

	reloadCtx := host.WithAssemblyReason(ctx, host.ReasonInferenceChange)
	if err := c.RebuildRuntime(reloadCtx); err != nil {
		t.Fatalf("rebuild while draining: %v", err)
	}

	recorder := logcapture.Install(t)
	if err := c.applyPluginInferenceWrite(false); err != nil {
		t.Fatalf("unchanged plugin write while draining: %v", err)
	}
	if got := countInvalidations(recorder); got != 0 {
		t.Fatalf(
			"unchanged plugin write invalidated the runtime %d times "+
				"while a replacement was armed, want 0", got)
	}

	gate.Release()
	if res, err := run.Wait(ctx); err != nil || res == nil ||
		res.Status != "completed" {
		t.Fatalf("run wait = %v, %v; want completed", res, err)
	}
	deadline := time.Now().Add(45 * time.Second)
	for {
		h := c.ActiveHost()
		if h != nil && h != first && !h.IsStale() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no replacement host was installed after the drain")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// A fresh Host serving the active workspace also answers an
	// unchanged write with nothing.
	if err := c.applyPluginInferenceWrite(false); err != nil {
		t.Fatalf("unchanged plugin write after rebuild: %v", err)
	}
	if got := countInvalidations(recorder); got != 0 {
		t.Fatalf(
			"unchanged plugin write invalidated the live runtime %d "+
				"times, want 0", got)
	}

	// A write that changed the stored rows still reaches the runtime.
	if err := c.applyPluginInferenceWrite(true); err != nil {
		t.Fatalf("changed plugin write: %v", err)
	}
	if got := countInvalidations(recorder); got != 1 {
		t.Fatalf("changed plugin write invalidated the runtime %d "+
			"times, want 1", got)
	}
}

// countInvalidations counts the runtime invalidations a recorder saw.
func countInvalidations(recorder *logcapture.Recorder) int {
	count := 0
	for _, record := range recorder.Records() {
		if record.Body().AsString() == "host: runtime invalidated" {
			count++
		}
	}
	return count
}
