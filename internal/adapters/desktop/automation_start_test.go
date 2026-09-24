package desktop

import (
	"errors"
	"fmt"
	"testing"

	"github.com/GizClaw/opencraft/internal/capabilities/automations"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
)

// TestAutomationStartFailure pins the mapping a refused automation
// start goes through: a busy conversation becomes a skipped record
// carrying the localizable reason (and the conversation, so the panel
// can link to it) with no error returned; anything else stays a failure
// and its original error surfaces unchanged.
func TestAutomationStartFailure(t *testing.T) {
	busy := fmt.Errorf("%w: session %q is running %s",
		host.ErrConversationBusy, "s-1", host.RunID("run-1"))
	res, err := automationStartFailure(busy, "s-1")
	if err != nil {
		t.Fatalf("busy start returned error %v, want a skipped record", err)
	}
	if res.Status != automations.RunSkipped {
		t.Fatalf("busy status = %q, want skipped", res.Status)
	}
	if res.Error != conversationBusyReason {
		t.Fatalf("busy reason = %q, want %q", res.Error, conversationBusyReason)
	}
	if res.ConversationID != "s-1" {
		t.Fatalf("busy conversation = %q, want s-1", res.ConversationID)
	}

	boom := errors.New("host: start turn: boom")
	res, err = automationStartFailure(boom, "s-1")
	if !errors.Is(err, boom) {
		t.Fatalf("failure error = %v, want the original", err)
	}
	if res.Status != automations.RunFailed || res.Error != "" {
		t.Fatalf("failure result = %+v, want a plain failure", res)
	}
	if res.ConversationID != "" {
		t.Fatalf("failure conversation = %q, want empty", res.ConversationID)
	}
}
