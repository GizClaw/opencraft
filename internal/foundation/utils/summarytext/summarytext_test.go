package summarytext

import (
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

func TestIsSummaryText(t *testing.T) {
	if !IsSummaryText(SummaryPrefix + "\nsummary") {
		t.Fatal("marked summary must be recognized")
	}
	if IsSummaryText("plain") {
		t.Fatal("plain text must not be marked")
	}
}
