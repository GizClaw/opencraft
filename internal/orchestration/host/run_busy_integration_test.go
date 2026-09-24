package host_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// TestAutomationStartStepsAsideForLiveRun pins the collision policy end
// to end: while a conversation has a live run, an automation start on
// the same conversation is refused with ErrConversationBusy (and is not
// retryable), the live run finishes untouched, and once it has been
// waited on the same automation start succeeds.
func TestAutomationStartStepsAsideForLiveRun(t *testing.T) {
	provider := fakeprovider.New(t,
		fakeprovider.Reply{Text: "first answer"},
		fakeprovider.Reply{Text: "second answer"},
	)
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
	h, err := mgr.Acquire(ctx, workDir, interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire host: %v", err)
	}
	defer func() { _ = h.Close() }()

	live, err := h.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "start the real work"),
		Origin:  host.OriginInteractive,
		// Title generation is another provider turn; this test counts
		// the turns each run exchanges, so it stays off.
		SkipAutoTitle: true,
	})
	if err != nil {
		t.Fatalf("start live run: %v", err)
	}

	// The user's run is live (host-owned until it is waited on), so the
	// scheduled start must be refused, not preempt it.
	_, err = h.StartRun(ctx, host.RunOptions{
		Message:   message.NewTextMessage(message.RoleUser, "scheduled work"),
		ContextID: live.ContextID(),
		Origin:    host.OriginAutomation,
	})
	if !errors.Is(err, host.ErrConversationBusy) {
		t.Fatalf("automation start err = %v, want ErrConversationBusy", err)
	}
	if host.IsRetryableStartError(err) {
		t.Fatalf("busy start error %v must not be retryable", err)
	}

	res, err := live.Wait(ctx)
	if err != nil {
		t.Fatalf("wait live run: %v", err)
	}
	if res == nil || res.Status != agent.StatusCompleted {
		t.Fatalf("live result = %+v, want completed", res)
	}
	// The refused start never reached the provider: only the live run
	// exchanged a turn.
	if got := provider.Calls(); got != 1 {
		t.Fatalf("provider calls after live run = %d, want 1", got)
	}

	// Waiting on the run released it; the same scheduled start now runs
	// in the same conversation.
	run, err := h.StartRun(ctx, host.RunOptions{
		Message:       message.NewTextMessage(message.RoleUser, "scheduled work"),
		ContextID:     live.ContextID(),
		Origin:        host.OriginAutomation,
		SkipAutoTitle: true,
	})
	if err != nil {
		t.Fatalf("automation start after wait: %v", err)
	}
	res, err = run.Wait(ctx)
	if err != nil {
		t.Fatalf("wait automation run: %v", err)
	}
	if res == nil || res.Status != agent.StatusCompleted {
		t.Fatalf("automation result = %+v, want completed", res)
	}
	if got := provider.Calls(); got != 2 {
		t.Fatalf("provider calls after automation run = %d, want 2", got)
	}
}
