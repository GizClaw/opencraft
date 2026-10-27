package worldstate

import (
	"fmt"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"
)

// toolResultText digs the result payload out of the item: the outer message's
// own text is empty, the output lives inside the result part.
func toolResultText(t *testing.T, item memory.ContextItem) string {
	t.Helper()
	for _, part := range item.Content.Parts {
		if result, ok := part.(message.ToolResultPart); ok {
			return result.Result.Content.Text()
		}
	}
	t.Fatalf("item carries no tool result")
	return ""
}

// TestPruneKeepsTheNewestResultsWhole pins the boundary: the results the model
// is working with stay verbatim, and the ones it has moved past are shortened
// with their head, their tail and a note saying how much went.
func TestPruneKeepsTheNewestResultsWhole(t *testing.T) {
	body := strings.Repeat("x", 5000) + "tail"
	items := make([]memory.ContextItem, 0, 11)
	items = append(items, memory.ContextItem{
		Kind:    memory.ContextRawMessage,
		Content: message.NewTextContent("go"),
	})
	const results = 10
	for i := 0; i < results; i++ {
		items = append(items, toolResultItem(fmt.Sprintf("c%d", i), body))
	}

	out := pruneOldToolResults(items)
	if len(out) != len(items) {
		t.Fatalf("items = %d, want %d", len(out), len(items))
	}
	// The newest keepToolResultsVerbatim results are untouched.
	for i := len(out) - keepToolResultsVerbatim; i < len(out); i++ {
		if got := toolResultText(t, out[i]); got != body {
			t.Errorf("item %d: newest result was truncated (%d chars)", i, len(got))
		}
	}
	// Everything older than the newest keepToolResultsVerbatim results is
	// bounded and says so, keeping head and tail. Item 0 is the user's
	// message, so the tool results run 1..results and the boundary sits at
	// len(out)-keepToolResultsVerbatim-1.
	for i := 1; i <= len(out)-keepToolResultsVerbatim-1; i++ {
		text := toolResultText(t, out[i])
		if len([]rune(text)) >= len([]rune(body)) {
			t.Errorf("item %d: old result was not shortened (%d chars)", i, len(text))
		}
		if !strings.HasPrefix(text, "xxxx") {
			t.Errorf("item %d: lost its head", i)
		}
		if !strings.Contains(text, "tool output truncated") {
			t.Errorf("item %d: does not say it was truncated", i)
		}
		if !strings.HasSuffix(text, "the transcript keeps all of it]") {
			t.Errorf("item %d: truncation note is not the last line: %q", i, text)
		}
	}
	// The user's own message is never touched: this rule is about tool output.
	if got := out[0].Content.Text(); got != "go" {
		t.Errorf("user message = %q, want go", got)
	}
}

// TestPruneIsDeterministic pins the property the fold's fixed point depends on:
// the same rows produce the same window, so a replayed turn does not shift
// under the model between rounds.
func TestPruneIsDeterministic(t *testing.T) {
	body := strings.Repeat("y", 9000)
	build := func() []memory.ContextItem {
		items := make([]memory.ContextItem, 0, 12)
		for i := 0; i < 12; i++ {
			items = append(items, toolResultItem(fmt.Sprintf("c%d", i), body))
		}
		return items
	}
	first := pruneOldToolResults(build())
	second := pruneOldToolResults(build())
	for i := range first {
		if a, b := toolResultText(t, first[i]), toolResultText(t, second[i]); a != b {
			t.Fatalf("item %d differs between identical runs", i)
		}
	}
}

// TestPruneLeavesSmallResultsAlone: a result inside the bound is not worth a
// marker, and rewriting it would only churn the prompt cache.
func TestPruneLeavesSmallResultsAlone(t *testing.T) {
	small := "ok: 3 tests passed"
	items := make([]memory.ContextItem, 0, 12)
	for i := 0; i < 12; i++ {
		items = append(items, toolResultItem(fmt.Sprintf("c%d", i), small))
	}
	out := pruneOldToolResults(items)
	for i := range out {
		if got := toolResultText(t, out[i]); got != small {
			t.Fatalf("item %d = %q, want the result untouched", i, got)
		}
	}
}
