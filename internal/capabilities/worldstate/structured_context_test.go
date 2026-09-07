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

func TestOrphanToolOutputDropped(t *testing.T) {
	secs := renderRawSections([]memory.ContextItem{
		toolResultItem("missing", "ok"),
	})
	if len(secs) != 0 {
		t.Fatalf("sections = %+v, want orphan tool output dropped", secs)
	}
}

func TestCallWithoutResultGetsAbortedSynthetic(t *testing.T) {
	secs := renderRawSections([]memory.ContextItem{
		toolCallItem("c3"),
	})
	if len(secs) != 2 {
		t.Fatalf("sections = %d, want call + synthetic aborted", len(secs))
	}
	if secs[0].Role != message.RoleAssistant {
		t.Fatalf("first section role = %s", secs[0].Role)
	}
	if secs[1].Role != message.RoleTool {
		t.Fatalf("second section role = %s, want tool", secs[1].Role)
	}
	results := secs[1].ToolResults()
	if len(results) != 1 || results[0].CallID != "c3" ||
		results[0].Content != "aborted" {
		t.Fatalf("synthetic result = %+v", results)
	}
}

func TestEmptyErrorResultBecomesAborted(t *testing.T) {
	item := memory.ContextItem{
		Kind:        memory.ContextRawMessage,
		MessageRole: message.RoleTool,
		Content: message.Content{Parts: []message.Part{
			message.TextPart{Text: "tool_result: "},
			message.ToolResultPart{Result: message.ToolResult{
				CallID: "c1", Content: "", IsError: true,
			}},
		}},
	}
	secs := renderRawSections([]memory.ContextItem{
		toolCallItem("c1"), item,
	})
	if len(secs) != 2 || secs[1].Role != message.RoleTool {
		t.Fatalf("sections = %+v", secs)
	}
	results := secs[1].ToolResults()
	if len(results) != 1 || results[0].Content != "aborted" {
		t.Fatalf("error result = %+v, want aborted marker", results)
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
