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
		name      string
		res       *agent.Result
		want      int
		wantKnown bool
	}{
		{name: "nil result", res: nil, want: 0, wantKnown: true},
		{name: "no state", res: &agent.Result{}, want: 0, wantKnown: true},
		{
			// Core records the key only when something is pending, so an
			// absent key is a known zero, not an unknown count.
			name: "missing key",
			res:  &agent.Result{State: map[string]any{"other": 1}},
			want: 0, wantKnown: true,
		},
		{
			name: "recorded count",
			res: &agent.Result{State: map[string]any{
				"session.pending_steer": 2,
			}},
			want: 2, wantKnown: true,
		},
		{
			// A result state that crossed JSON carries numbers as
			// float64; whole values are the same count.
			name: "json-decoded count",
			res: &agent.Result{State: map[string]any{
				"session.pending_steer": float64(3),
			}},
			want: 3, wantKnown: true,
		},
		{
			name: "unexpected type",
			res: &agent.Result{State: map[string]any{
				"session.pending_steer": "2",
			}},
			want: 0, wantKnown: false,
		},
		{
			name: "negative count",
			res: &agent.Result{State: map[string]any{
				"session.pending_steer": -1,
			}},
			want: 0, wantKnown: false,
		},
		{
			name: "fractional count",
			res: &agent.Result{State: map[string]any{
				"session.pending_steer": 1.5,
			}},
			want: 0, wantKnown: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, known := pendingSteerCount(tc.res)
			if got != tc.want || known != tc.wantKnown {
				t.Fatalf("pendingSteerCount = %d/%v, want %d/%v",
					got, known, tc.want, tc.wantKnown)
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
