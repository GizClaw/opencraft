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

// TestReloadIdleHostKeepsSharedStoreOpenForActiveRun reproduces the
// closed-session.db failure after a config/plugin reload:
//
//  1. A run is active on Host A.
//  2. Reload invalidates A (it finishes on the old runtime) and
//     assembles Host B sharing the same sessions.Store.
//  3. Host B is idle, so a second reload closes it immediately.
//
// The shared Store must survive B's runtime close until A's run ends;
// otherwise A's commit fails with "sql: database is closed".
func TestReloadIdleHostKeepsSharedStoreOpenForActiveRun(t *testing.T) {
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
		t.Fatalf("acquire replacement host B: %v", err)
	}
	if hostA == hostB {
		t.Fatal("replacement host must be a fresh runtime sharing the store")
	}
	if err := hostB.Close(); err != nil {
		t.Fatalf("close idle replacement host: %v", err)
	}

	// The shared DB must still be usable while A's run is in flight.
	if err := hostA.Sessions().SetMode(
		ctx, run.ContextID(), sessions.ModeYOLO,
	); err != nil {
		t.Fatalf("shared session store closed by idle host teardown: %v", err)
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
	// last Host closes the shared store; otherwise the title write is
	// lost to "sql: database is closed".
	hostC, err := mgr.Acquire(ctx, workDir, interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire host C after teardown: %v", err)
	}
	defer func() { _ = hostC.Close() }()
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
