package summarytext

import (
	"encoding/json"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"
)

func TestRenderMessageIncludesToolActivity(t *testing.T) {
	m := message.Message{
		Role: message.RoleAssistant,
		Content: message.Content{Parts: []message.Part{
			message.TextPart{Text: "running"},
			message.ToolCallPart{Call: message.ToolCall{
				ID: "c1", Name: "exec_command",
				Arguments: []byte(`{"cmd":"go test ./..."}`),
			}},
		}},
	}
	got := RenderMessage(m)
	if got != "running\ntool_call: exec_command {\"cmd\":\"go test ./...\"}" {
		t.Fatalf("render = %q", got)
	}
}

func TestToolActivityEmptyWithoutTools(t *testing.T) {
	m := message.NewTextMessage(message.RoleUser, "hi")
	if got := ToolActivity(m); len(got) != 0 {
		t.Fatalf("tool activity = %v, want empty", got)
	}
}

func TestToolActivityRendersCallsAndResults(t *testing.T) {
	m := message.Message{
		Role: message.RoleAssistant,
		Content: message.Content{Parts: []message.Part{
			message.ToolCallPart{Call: message.ToolCall{
				ID: "c1", Name: "webfetch",
				Arguments: json.RawMessage(`{"url":"https://example.com"}`),
			}},
		}},
	}
	got := ToolActivity(m)
	if len(got) != 1 ||
		got[0] != `tool_call: webfetch {"url":"https://example.com"}` {
		t.Fatalf("ToolActivity = %#v", got)
	}

	m = message.Message{
		Role: message.RoleTool,
		Content: message.Content{Parts: []message.Part{
			message.ToolResultPart{Result: message.ToolResult{
				CallID: "c1", Content: "ok",
			}},
		}},
	}
	got = ToolActivity(m)
	if len(got) != 1 || got[0] != "tool_result: ok" {
		t.Fatalf("ToolActivity result = %#v", got)
	}
}

func TestRenderMessageJoinsTextAndToolActivity(t *testing.T) {
	m := message.Message{
		Role: message.RoleAssistant,
		Content: message.Content{Parts: []message.Part{
			message.TextPart{Text: "checking"},
			message.ToolCallPart{Call: message.ToolCall{
				ID: "c1", Name: "webfetch",
				Arguments: json.RawMessage(`{}`),
			}},
		}},
	}
	if got, want := RenderMessage(m), "checking\ntool_call: webfetch {}"; got != want {
		t.Fatalf("RenderMessage = %q, want %q", got, want)
	}
	only := message.NewTextMessage(message.RoleUser, "plain")
	if got := RenderMessage(only); got != "plain" {
		t.Fatalf("RenderMessage plain = %q", got)
	}
}

func TestIsSummaryText(t *testing.T) {
	if !IsSummaryText(SummaryPrefix + "\nsummary") {
		t.Fatal("marked summary must be recognized")
	}
	if IsSummaryText("plain") {
		t.Fatal("plain text must not be marked")
	}
}
