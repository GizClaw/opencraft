package bindings

import (
	"encoding/json"
	"testing"

	"github.com/GizClaw/flowcraft/core/event"
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
	if got := streamRunID(event.Subject("agent.run.r-123.stream.assistant.delta")); got != "r-123" {
		t.Fatalf("streamRunID = %q, want r-123", got)
	}
	if got := streamRunID(event.Subject("agent.run.r-123.start")); got != "r-123" {
		t.Fatalf("streamRunID = %q, want r-123 for run events", got)
	}
	if got := streamRunID(event.Subject("agent.other.x")); got != "" {
		t.Fatalf("streamRunID = %q, want empty", got)
	}
}
