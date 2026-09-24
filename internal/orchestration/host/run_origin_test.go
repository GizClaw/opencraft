package host

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"
)

// TestRunOriginPolicy pins which origins may preempt a live run and
// which step aside: only a person's message preempts; scheduled
// (automation) and internal (system) work is refused. The empty origin
// reads as interactive because every caller predating the field sent
// user turns.
func TestRunOriginPolicy(t *testing.T) {
	for _, tc := range []struct {
		origin   RunOrigin
		valid    bool
		preempts bool
	}{
		{"", true, true},
		{OriginInteractive, true, true},
		{OriginAutomation, true, false},
		{OriginSystem, true, false},
		{"chat", false, false},
		{"Interactive", false, false},
	} {
		if got := tc.origin.valid(); got != tc.valid {
			t.Errorf("%q.valid() = %v, want %v", tc.origin, got, tc.valid)
		}
		if got := tc.origin.preemptsLiveRun(); got != tc.preempts {
			t.Errorf("%q.preemptsLiveRun() = %v, want %v",
				tc.origin, got, tc.preempts)
		}
	}
}

// TestStartRunRejectsUnknownOrigin pins that validation runs before any
// runtime or store access and before the conflict policy is applied: a
// typo must fail the start rather than silently buy the interactive
// policy's permission to preempt.
func TestStartRunRejectsUnknownOrigin(t *testing.T) {
	h := &Host{}
	_, err := h.StartRun(context.Background(), RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "hello"),
		Origin:  RunOrigin("chatty"),
	})
	if err == nil || !strings.Contains(err.Error(), `unknown run origin "chatty"`) {
		t.Fatalf("err = %v, want unknown run origin", err)
	}
}

// TestStartRunKnownOriginStillReportsLifecycleGuard documents the order
// after the origin check: a known origin on a host whose runtime is not
// assembled keeps reporting the retryable lifecycle guard, so origin
// validation cannot mask a Host rebuild.
func TestStartRunKnownOriginStillReportsLifecycleGuard(t *testing.T) {
	h := &Host{}
	_, err := h.StartRun(context.Background(), RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "hello"),
		Origin:  OriginAutomation,
	})
	if !errors.Is(err, ErrRuntimeNotReady) {
		t.Fatalf("err = %v, want ErrRuntimeNotReady", err)
	}
}
