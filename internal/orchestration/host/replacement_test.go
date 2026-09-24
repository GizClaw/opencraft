package host_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// TestScheduleReplacementAssemblesWhenTheDrainIsAlreadyGone pins the
// race the deferred-rebuild watcher has to survive: a workspace's
// retired Host can finish teardown between the moment a reload armed
// the replacement and the moment the watcher looks. "No Host for this
// workspace" must not be read as "a live Host serves it" — a workspace
// left unserved shows the window an empty session list until the next
// turn or rebuild — so the watcher assembles the replacement the arm
// asked for, and that replacement serves new turns.
func TestScheduleReplacementAssemblesWhenTheDrainIsAlreadyGone(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "done"})
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
	first, err := mgr.Acquire(ctx, workDir, interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire host: %v", err)
	}
	// Retire the idle Host and let the teardown settle, so the watcher
	// finds a workspace with no Host at all.
	if err := first.Close(); err != nil {
		t.Fatalf("close host: %v", err)
	}
	if err := first.WaitClosed(ctx); err != nil {
		t.Fatalf("wait closed: %v", err)
	}
	if got := mgr.Current(workDir); got != nil {
		t.Fatalf("current after teardown = %p, want the workspace unserved",
			got)
	}

	installed := make(chan string, 1)
	mgr.SetReplacementHooks(host.ReplacementHooks{
		Installed: func(workDir string) { installed <- workDir },
	})
	if !mgr.ScheduleReplacement(ctx, workDir) {
		t.Fatal("the replacement was not armed")
	}
	select {
	case got := <-installed:
		if got != workDir {
			t.Fatalf("replacement installed for %q, want %q", got, workDir)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("no replacement was assembled: the workspace was left " +
			"with no Host")
	}
	replacement := mgr.Current(workDir)
	if replacement == nil || replacement == first {
		t.Fatalf("current after replacement = %p, want a fresh Host",
			replacement)
	}
	run, err := replacement.StartRun(ctx, host.RunOptions{
		Message:       message.NewTextMessage(message.RoleUser, "after retire"),
		SkipAutoTitle: true,
	})
	if err != nil {
		t.Fatalf("start run on the replacement: %v", err)
	}
	if res, err := run.Wait(ctx); err != nil || res == nil ||
		res.Status != "completed" {
		t.Fatalf("replacement run = %v, %v; want completed", res, err)
	}

	// The armed slot is released once the replacement settled, so the
	// next reload can arm one of its own.
	deadline := time.Now().Add(30 * time.Second)
	for mgr.ReplacementArmed(workDir) {
		if time.Now().After(deadline) {
			t.Fatal("the armed slot was never released")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
