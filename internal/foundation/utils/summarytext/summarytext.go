// Package summarytext centralizes the rendering and marker logic shared
// by the compact tool, the memory commit hooks, and the graph's
// compaction node. Keeping it in one Go package stops the same rules
// from drifting between capabilities.
package summarytext

import (
	"strings"

	"github.com/GizClaw/flowcraft/core/message"
)

// SummaryPrefix marks a compaction summary injected into the
// conversation as a user message (codex-style). Lifecycle hooks and
// the graph compaction node filter messages carrying this marker so
// the summary is never persisted as real conversation.
const SummaryPrefix = "Another language model started to solve this problem and produced a summary of its thinking process."

// ToolActivity returns the rendered tool_call / tool_result lines of
// m, or nil when the message carries no tool activity.
func ToolActivity(m message.Message) []string {
	var lines []string
	for _, p := range m.Content.Parts {
		switch part := p.(type) {
		case message.ToolCallPart:
			lines = append(lines,
				"tool_call: "+part.Call.Name+" "+string(part.Call.Arguments))
		case message.ToolResultPart:
			lines = append(lines, "tool_result: "+part.Result.Content)
		}
	}
	return lines
}

// RenderMessage flattens one conversation message into its prompt
// form: text parts keep their content, and tool activity is rendered
// as tool_call / tool_result lines.
func RenderMessage(m message.Message) string {
	text := m.Content.Text()
	lines := ToolActivity(m)
	if len(lines) == 0 {
		return text
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return strings.Join(lines, "\n")
	}
	return trimmed + "\n" + strings.Join(lines, "\n")
}

// IsSummaryText reports whether text is a marked compaction summary.
func IsSummaryText(text string) bool {
	return strings.HasPrefix(text, SummaryPrefix+"\n")
}
