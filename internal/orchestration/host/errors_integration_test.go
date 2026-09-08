package host_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// TestStartRunOnClosedHostReturnsRetryableSentinel pins that StartRun
// after a Host retired surfaces ErrRuntimeClosing (not a wrapped or
// unrelated error), so adapters can retry on the replacement Host.
func TestStartRunOnClosedHostReturnsRetryableSentinel(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "done"})
	workDir := t.TempDir()
	dataDir := t.TempDir()
	configDir := t.TempDir()
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFakeConfig(t, configDir, provider.URL())

	mgr := host.NewManagerAt(dataDir, configDir)
	ctx := context.Background()
	h, err := mgr.Acquire(ctx, workDir, interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire host: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("close host: %v", err)
	}
	run, err := h.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "after close"),
	})
	if run != nil {
		t.Fatalf("StartRun on closed host returned run %+v", run)
	}
	if !errors.Is(err, host.ErrRuntimeClosing) {
		t.Fatalf("StartRun on closed host error = %v, want ErrRuntimeClosing",
			err)
	}
}
