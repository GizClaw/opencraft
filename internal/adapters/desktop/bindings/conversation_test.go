package bindings

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/event"

	"github.com/GizClaw/opencraft/internal/orchestration/interact"
)

func TestStartTurnRequestDecodesMessageObject(t *testing.T) {
	raw := `{
		"context_id": "s-1",
		"message": {
			"role": "user",
			"content": {
				"parts": [
					{"type": "text", "text": "hello"}
				]
			}
		}
	}`
	var req StartTurnRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("decode StartTurnRequest: %v", err)
	}
	if req.ContextID != "s-1" {
		t.Fatalf("context_id = %q, want s-1", req.ContextID)
	}
	if got := req.Message.Content.Text(); got != "hello" {
		t.Fatalf("message text = %q, want hello", got)
	}
}

func TestStreamRunIDExtractsFromSubject(t *testing.T) {
	if got := interact.StreamRunID(
		event.Subject("agent.run.r-123.stream.assistant.delta"),
	); got != "r-123" {
		t.Fatalf("streamRunID = %q, want r-123", got)
	}
	if got := interact.StreamRunID(
		event.Subject("agent.run.r-123.start"),
	); got != "r-123" {
		t.Fatalf("streamRunID = %q, want r-123 for run events", got)
	}
	if got := interact.StreamRunID(event.Subject("agent.other.x")); got != "" {
		t.Fatalf("streamRunID = %q, want empty", got)
	}
}

// A turn_end event carries one of the engine's terminal statuses and
// nothing else. A wait that produced no result used to publish the
// "unknown" placeholder — a value no consumer can render, which the
// frontend half of the finding reads as a failure. The result-less
// wait now reports the terminal its error supports.
func TestWaitStatusNamesAResultlessWait(t *testing.T) {
	cases := []struct {
		name    string
		res     *agent.Result
		waitErr error
		want    string
	}{
		{
			name: "the engine's status is the answer",
			res:  &agent.Result{Status: agent.StatusCompleted},
			want: "completed",
		},
		{
			name:    "a cancelled wait is a cancelled turn",
			waitErr: context.Canceled,
			want:    "canceled",
		},
		{
			name:    "a deadline is cancelled too; the error kind says timeout",
			waitErr: context.DeadlineExceeded,
			want:    "canceled",
		},
		{
			name:    "any other result-less wait failed",
			waitErr: errors.New("host: run already finished"),
			want:    "failed",
		},
		{
			name: "no result and no error is still a failure",
			want: "failed",
		},
		{
			name:    "an engine status this build cannot name is not published",
			res:     &agent.Result{},
			waitErr: context.Canceled,
			want:    "canceled",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := waitStatus(tc.res, tc.waitErr); got != tc.want {
				t.Fatalf("waitStatus = %q, want %q", got, tc.want)
			}
		})
	}
}
