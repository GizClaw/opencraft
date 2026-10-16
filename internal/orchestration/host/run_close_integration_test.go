package host_test

import (
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/inference/inferencetest"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/testing/logcapture"
)

// TestRunCloseReleasesWithoutWait is the regression for the Run release
// contract: StartRun documents "call Wait (or Close)", and Close must
// actually be that other path. A caller that walks away without either
// would leave the conversation busy forever and the Host unable to
// close; this test takes Close, with a run parked in the provider, and
// then asserts the run really was released: the Host still closes and
// the next turn on the same conversation is accepted.
func TestRunCloseReleasesWithoutWait(t *testing.T) {
	provider := inferencetest.NewScriptedOpenAI(t,
		inferencetest.ScriptedReply{Text: "first"},
		inferencetest.ScriptedReply{Text: "second"},
	)
	hold := provider.HoldNext()
	defer hold.Release()

	h, ctx := steerHost(t, provider)
	run, err := h.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "hold this turn"),
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	waitSteerGate(t, hold)

	// The caller gives up on the result: Close must cancel the parked
	// turn and settle it, without Wait ever being called.
	closed := make(chan struct{})
	go func() {
		run.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(30 * time.Second):
		t.Fatal("Run.Close did not return")
	}
	// Close settled the run, not just returned: the settle path is what
	// drops the Host's run slot (and its cleanup would otherwise hang
	// the host close at test end).
	if err := h.CancelRun(run.RunID()); err == nil {
		t.Fatal("run still live after Close: the settle path did not run")
	}

	// Released means the run slot is back: a second turn on the same
	// conversation starts and finishes normally.
	second, err := h.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "again"),
	})
	if err != nil {
		t.Fatalf("start run after close: %v", err)
	}
	if _, err := second.Wait(ctx); err != nil {
		t.Fatalf("wait second run: %v", err)
	}
}

// TestRunCloseAfterWaitIsDeferred pins the other half: after Wait
// already settled the run, Close is a no-op and must not log a cancel
// failure for a run that no longer exists.
func TestRunCloseAfterWaitIsDeferred(t *testing.T) {
	recorder := logcapture.Install(t)
	provider := inferencetest.NewScriptedOpenAI(t,
		inferencetest.ScriptedReply{Text: "done"},
	)
	h, ctx := steerHost(t, provider)
	run, err := h.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "hello"),
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("wait run: %v", err)
	}
	run.Close()
	if got := recordsWithBody(recorder, "host: cancel run on close failed"); len(got) != 0 {
		t.Fatalf("Close after a settled Wait cancelled a finished run: %d records", len(got))
	}
}

// TestRunSecondWaitReportsTheOwnersOutcome pins the duplicate-release
// semantics: a caller that races a Wait or a Close reads the outcome
// the release produced instead of an invented "already finished" error,
// which a turn renderer would show as a failed turn.
func TestRunSecondWaitReportsTheOwnersOutcome(t *testing.T) {
	provider := inferencetest.NewScriptedOpenAI(t,
		inferencetest.ScriptedReply{Text: "only reply"},
	)
	h, ctx := steerHost(t, provider)
	run, err := h.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "wait twice"),
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	first, firstErr := run.Wait(ctx)
	second, secondErr := run.Wait(ctx)
	if second != first || secondErr != firstErr {
		t.Fatalf("second wait = (%v, %v), want the owner's (%v, %v)",
			second, secondErr, first, firstErr)
	}
}
