package host_test

import (
	"context"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// TestWaitWithACancelledContextReturnsNoResult pins the wait contract
// for a caller whose context fires before the turn settles: core's turn
// Wait answers (nil, ctx error) instead of a result, and the host settle
// path must survive that shape. It used to read the missing result while
// recording the turn (host/run.go), which panicked the goroutine the
// desktop starts for every turn — a whole-app crash out of a cancelled
// wait.
func TestWaitWithACancelledContextReturnsNoResult(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "never read"})
	hold := provider.HoldNext()
	defer hold.Release()

	h, ctx := steerHost(t, provider)
	run, err := h.StartRun(ctx, host.RunOptions{
		Message:       message.NewTextMessage(message.RoleUser, "hold this turn"),
		SkipAutoTitle: true,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	// The provider holds the first round, so the turn is genuinely live
	// when the caller's context fires.
	waitSteerGate(t, hold)

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	res, waitErr := run.Wait(cancelled)
	if waitErr == nil {
		t.Fatal("Wait with a cancelled context reported no error")
	}
	if res != nil {
		t.Fatalf("Wait with a cancelled context returned a result: %+v", res)
	}
	// Let the turn finish before the test tears the host down.
	hold.Release()
}
