package interact

import (
	"context"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/testing/logcapture"
)

// waitForBody polls the captured telemetry for one log body.
func waitForBody(
	recorder *logcapture.Recorder, body string, timeout time.Duration,
) bool {
	deadline := time.Now().Add(timeout)
	for {
		for _, record := range recorder.Records() {
			if record.Body().AsString() == body {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestBrokerDeliverAfterUnbindIsReported pins the unobservable-reply
// case: a prompt reply that arrives after the turn unbound cannot be
// delivered, and the broker must say so instead of dropping it in
// silence (a UI stuck on a stale prompt has nothing else to trace).
func TestBrokerDeliverAfterUnbindIsReported(t *testing.T) {
	recorder := logcapture.Install(t)
	broker := NewWithBackendResolver(nil, nil, nil)
	turn := &fakeReplier{}
	broker.BindTurn("t1", turn)
	broker.UnbindTurn("t1")

	broker.deliver(context.Background(), Spec{
		ID: "p1", RunID: "r1", TurnID: "t1",
	}, Reply{Status: ReplyOK, Text: "late"})

	if !waitForBody(recorder,
		"runtime: prompt reply dropped; the turn already ended", 5*time.Second) {
		t.Fatalf("the dropped reply was not reported (bodies: %v)",
			recorder.Bodies())
	}
	turn.mu.Lock()
	defer turn.mu.Unlock()
	if turn.id != "" {
		t.Fatalf("reply reached an unbound turn: %q", turn.id)
	}
}
