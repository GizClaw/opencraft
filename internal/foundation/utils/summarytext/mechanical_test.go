package summarytext

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/GizClaw/flowcraft/core/message"
)

// TestMechanicalSummaryCarriesTheFoldedEvidence pins what the model-free
// fallback must keep: the previous summary, the asks in order, the tools
// that ran, and the tail of the folded region.
func TestMechanicalSummaryCarriesTheFoldedEvidence(t *testing.T) {
	msgs := []message.Message{
		message.NewTextMessage(message.RoleUser, "fix the sidebar sort\nsecond line"),
		message.NewTextMessage(message.RoleAssistant, "reading the store"),
		{
			Role: message.RoleAssistant,
			Content: message.Content{Parts: []message.Part{
				message.ToolCallPart{Call: message.ToolCall{
					ID: "c1", Name: "exec_command",
					Arguments: json.RawMessage(`{"cmd":"go test ./..."}`),
				}},
			}},
		},
		{
			Role: message.RoleTool,
			Content: message.Content{Parts: []message.Part{
				message.ToolResultPart{Result: message.ToolResult{
					CallID: "c1", Content: message.NewTextContent("ok"),
				}},
			}},
		},
		message.NewTextMessage(message.RoleUser, "now the tests"),
	}
	out := MechanicalSummary(msgs, "## Previous\nolder context", 4096)
	for _, want := range []string{
		"older context",
		"Folded rounds (mechanical digest)",
		"- folded: 5 messages (user 2, assistant 2, tool 1)",
		`"fix the sidebar sort"`,
		`"now the tests"`,
		"exec_command",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("digest missing %q:\n%s", want, out)
		}
	}
}

// TestMechanicalSummaryIgnoresDerivedContext keeps summaries and harness
// notices out of the asks list: a later fold merges the previous summary
// explicitly, and a notice is not something the user asked for.
func TestMechanicalSummaryIgnoresDerivedContext(t *testing.T) {
	msgs := []message.Message{
		message.NewTextMessage(message.RoleUser, SummaryPrefix+"\nfolded older rounds"),
		message.NewTextMessage(message.RoleUser, ContextNoticePrefix+" wrap up"),
		message.NewTextMessage(message.RoleUser, "the real ask"),
	}
	out := MechanicalSummary(msgs, "", 4096)
	if strings.Contains(out, "Another language model") ||
		strings.Contains(out, "wrap up") {
		t.Errorf("digest echoed derived context:\n%s", out)
	}
	if !strings.Contains(out, `"the real ask"`) {
		t.Errorf("digest dropped the real ask:\n%s", out)
	}
}

// TestMechanicalSummaryStaysInsideItsBudget pins the budget as a hard cap:
// the digest feeds the next fold as its previous summary, so anything over
// it grows the conversation's compaction state on every fold.
func TestMechanicalSummaryStaysInsideItsBudget(t *testing.T) {
	var msgs []message.Message
	for i := 0; i < 40; i++ {
		msgs = append(msgs, message.NewTextMessage(
			message.RoleUser, strings.Repeat("很长的用户请求 ", 40)))
	}
	prev := strings.Repeat("previous summary ", 400)
	for _, budget := range []int{64, 256, 1024} {
		out := MechanicalSummary(msgs, prev, budget)
		if got := utf8.RuneCountInString(out); got > budget {
			t.Errorf("budget %d: digest is %d runes", budget, got)
		}
	}
}
