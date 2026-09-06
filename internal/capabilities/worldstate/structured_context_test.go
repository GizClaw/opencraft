package worldstate

import (
	"encoding/json"
	"testing"

	"github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"
)

func toolCallItem(id string) memory.ContextItem {
	return memory.ContextItem{
		Kind:        memory.ContextRawMessage,
		MessageRole: message.RoleAssistant,
		Content: message.Content{Parts: []message.Part{
			message.ToolCallPart{Call: message.ToolCall{
				ID: id, Name: "webfetch",
				Arguments: json.RawMessage(`{"url":"https://example.com"}`),
			}},
		}},
	}
}

func toolResultItem(callID, content string) memory.ContextItem {
	return memory.ContextItem{
		Kind:        memory.ContextRawMessage,
		MessageRole: message.RoleTool,
		Content: message.Content{Parts: []message.Part{
			message.TextPart{Text: "tool_result: " + content},
			message.ToolResultPart{Result: message.ToolResult{
				CallID: callID, Content: content,
			}},
		}},
	}
}

func TestRawPairPreservesToolRoles(t *testing.T) {
	secs := renderRawSections([]memory.ContextItem{
		toolCallItem("c1"),
		toolResultItem("c1", "ok"),
	})
	if len(secs) != 2 {
		t.Fatalf("sections = %d (%+v), want call + result", len(secs), secs)
	}
	if secs[0].Role != "assistant" || secs[0].Content.Text() != "" ||
		len(secs[0].Content.Parts) != 1 {
		t.Fatalf("call section = %+v, want assistant with one tool_call part", secs[0])
	}
	if secs[1].Role != "tool" || secs[1].Content.Text() != "" ||
		len(secs[1].Content.Parts) != 1 {
		t.Fatalf("result section = %+v, want tool with one tool_result part", secs[1])
	}
}

func TestUnpairedToolFallsBackToUserText(t *testing.T) {
	secs := renderRawSections([]memory.ContextItem{
		toolResultItem("missing", "ok"),
	})
	if len(secs) != 1 {
		t.Fatalf("sections = %+v, want one fallback", secs)
	}
	if secs[0].Role != "user" || secs[0].Content.Text() == "" ||
		len(secs[0].Content.Parts) != 1 {
		t.Fatalf("fallback = %+v, want user text message", secs[0])
	}
}

func TestHistoryPairPreservesToolRoles(t *testing.T) {
	hist := renderHistoryMessages([]memory.ContextItem{
		toolCallItem("c2"),
		toolResultItem("c2", "ok"),
	})
	if len(hist) != 2 {
		t.Fatalf("history = %+v, want call + result", hist)
	}
	if hist[0].Role != "assistant" || hist[1].Role != "tool" {
		t.Fatalf("roles = %s/%s, want assistant/tool",
			hist[0].Role, hist[1].Role)
	}
}

func TestPlainAssistantRawKeepsRole(t *testing.T) {
	secs := renderRawSections([]memory.ContextItem{{
		Kind:        memory.ContextRawMessage,
		MessageRole: message.RoleAssistant,
		Content: message.Content{Parts: []message.Part{
			message.TextPart{Text: "answer"},
		}},
	}})
	if len(secs) != 1 || secs[0].Role != "assistant" ||
		secs[0].Content.Text() != "answer" || len(secs[0].Content.Parts) != 1 {
		t.Fatalf("plain assistant = %+v", secs)
	}
}
