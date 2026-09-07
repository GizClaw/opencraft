package rollout

import (
	"encoding/json"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"
)

func TestItemEventsFromStreamMapsParts(t *testing.T) {
	conv, run := "s-1", "r-1"
	toolCall := agent.StreamDeltaPayload{
		Type: agent.StreamDeltaPart,
		Part: message.ToolCallPart{Call: message.ToolCall{
			ID: "c1", Name: "webfetch",
			Arguments: json.RawMessage(`{"url":"https://example.com"}`),
		}},
	}
	got := ItemEventsFromStream(conv, run, toolCall)
	if len(got) != 1 {
		t.Fatalf("tool_call events = %d, want 1", len(got))
	}
	ev := got[0]
	if ev.Type != TypeItemToolCall || ev.ConversationID != conv ||
		ev.RunID != run || ev.CallID != "c1" ||
		ev.Tool != "webfetch" || string(ev.Arguments) != `{"url":"https://example.com"}` {
		t.Fatalf("tool_call event = %+v", ev)
	}

	toolResult := agent.StreamDeltaPayload{
		Type: agent.StreamDeltaPart,
		Part: message.ToolResultPart{Result: message.ToolResult{
			CallID: "c1", Content: "ok", IsError: true,
		}},
	}
	got = ItemEventsFromStream(conv, run, toolResult)
	if len(got) != 1 || got[0].Type != TypeItemToolResult ||
		got[0].CallID != "c1" || got[0].Content != "ok" ||
		!got[0].IsError {
		t.Fatalf("tool_result events = %+v", got)
	}

	for _, delta := range []agent.StreamDeltaPayload{
		{
			Type: agent.StreamDeltaPart,
			Part: message.TextPart{Text: "hello"},
		},
		{
			Type: agent.StreamDeltaPart,
			Part: message.ReasoningPart{Text: "thinking"},
		},
		{Type: agent.StreamDeltaFinish},
	} {
		if got := ItemEventsFromStream(conv, run, delta); len(got) != 0 {
			t.Fatalf("delta %+v produced events %+v, want none", delta, got)
		}
	}
}

func TestFlushItemEvents(t *testing.T) {
	got := FlushItemEvents("s-1", "r-1", "", "")
	if len(got) != 0 {
		t.Fatalf("empty flush = %+v, want none", got)
	}
	got = FlushItemEvents("s-1", "r-1", "think", "answer")
	if len(got) != 2 {
		t.Fatalf("flush events = %d, want reasoning + text", len(got))
	}
	if got[0].Type != TypeItemReasoning || got[0].Content != "think" {
		t.Fatalf("first flush event = %+v", got[0])
	}
	if got[1].Type != TypeItemAssistantMsg || got[1].Content != "answer" {
		t.Fatalf("second flush event = %+v", got[1])
	}
}
