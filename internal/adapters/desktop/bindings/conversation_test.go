package bindings

import (
	"encoding/json"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/event"

	"github.com/GizClaw/opencraft/internal/orchestration/interact"
)

func TestPendingSteerCountReadsResultState(t *testing.T) {
	for _, tc := range []struct {
		name string
		res  *agent.Result
		want int
	}{
		{name: "nil result", res: nil, want: 0},
		{name: "no state", res: &agent.Result{}, want: 0},
		{
			name: "missing key",
			res:  &agent.Result{State: map[string]any{"other": 1}},
			want: 0,
		},
		{
			name: "recorded count",
			res: &agent.Result{State: map[string]any{
				"session.pending_steer": 2,
			}},
			want: 2,
		},
		{
			name: "unexpected type",
			res: &agent.Result{State: map[string]any{
				"session.pending_steer": "2",
			}},
			want: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pendingSteerCount(tc.res); got != tc.want {
				t.Fatalf("pendingSteerCount = %d, want %d", got, tc.want)
			}
		})
	}
}

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
