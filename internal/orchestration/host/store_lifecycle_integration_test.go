package host_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// TestReloadDefersUntilActiveRunFinishes pins the reload-during-run
// semantics that keep one conversation on one runtime:
//
//  1. A run is active on Host A.
//  2. Reload invalidates A, but A stays pooled and keeps serving the
//     live run; Acquire must return the same Host instead of
//     assembling a second runtime for the workspace.
//  3. After the run ends, A retires itself at idle and the next
//     Acquire assembles a fresh Host.
//
// The fresh Host must be able to read the persisted turn and its
// auto-title from the shared workspace DB.
func TestReloadDefersUntilActiveRunFinishes(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "done"})
	gate := provider.HoldNext()
	defer gate.Release()

	workDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dataDir, "home"))
	configDir := filepath.Join(dataDir, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFakeConfig(t, configDir, provider.URL())

	mgr := host.NewManagerAt(dataDir, configDir)
	ctx := context.Background()

	hostA, err := mgr.Acquire(ctx, workDir, interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire host A: %v", err)
	}
	run, err := hostA.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "reload during run"),
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	select {
	case <-gate.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("provider request did not start")
	}

	mgr.Invalidate(workDir)
	hostB, err := mgr.Acquire(ctx, workDir, interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire host during active run: %v", err)
	}
	if hostA != hostB {
		t.Fatalf("reload assembled a second runtime (%p) while %p still had a live run",
			hostB, hostA)
	}

	// The old runtime must still be usable while the run is in
	// flight; no teardown may happen under a live turn.
	if err := hostA.Sessions().SetMode(
		ctx, run.ContextID(), sessions.ModeYOLO,
	); err != nil {
		t.Fatalf("host torn down while a run was in flight: %v", err)
	}

	gate.Release()
	res, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("wait run after idle host close: %v", err)
	}
	if res == nil || res.Status != "completed" {
		t.Fatalf("run result = %+v, want completed", res)
	}

	// run.Wait must also wait for the post-run auto-title before the
	// stale Host closes the shared store; otherwise the title write is
	// lost to "sql: database is closed".
	hostC, err := mgr.Acquire(ctx, workDir, interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire host C after teardown: %v", err)
	}
	defer func() { _ = hostC.Close() }()
	if hostA == hostC {
		t.Fatal("fresh host was not assembled after the stale host retired")
	}
	var title string
	if err := hostC.Sessions().ReadState(
		run.ContextID(), "title", &title,
	); err != nil {
		t.Fatalf("read persisted auto title: %v", err)
	}
	if title == "" {
		t.Fatal("auto title did not finish before the session store closed")
	}
}
