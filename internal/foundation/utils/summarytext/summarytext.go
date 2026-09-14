// Package summarytext centralizes the rendering and marker logic shared
// by the compact tool, the memory commit hooks, and the graph's
// compaction node. Keeping it in one Go package stops the same rules
// from drifting between capabilities.
package summarytext

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf16"

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
				"tool_call: "+part.Call.Name+" "+compactJSON(part.Call.Arguments))
		case message.ToolResultPart:
			lines = append(lines, "tool_result: "+part.Result.Content.Text())
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

// The prompt-footprint estimate. It is deliberately crude: a provider
// bills per token, but the graph decides compaction *before* the
// request is built, so the budget check only needs a stable
// approximation of what one round will cost. CJK code points cost about
// one token each, Latin text about one per four characters, and every
// message carries a fixed overhead for the role/format scaffolding a
// provider adds around it.
const (
	// estimateCharsPerToken approximates non-CJK text.
	estimateCharsPerToken = 4
	// estimatePerMessage covers per-message scaffolding.
	estimatePerMessage = 8
)

// EstimateText estimates the prompt footprint of one rendered message
// text. It counts UTF-16 code units, matching the graph node's JS
// `String.length`, so the two implementations stay interchangeable.
func EstimateText(text string) int {
	units := utf16.Encode([]rune(text))
	cjk := 0
	for _, unit := range units {
		if isCJKUnit(unit) {
			cjk++
		}
	}
	rest := len(units) - cjk
	return cjk + (rest+estimateCharsPerToken-1)/estimateCharsPerToken
}

// EstimateTokens estimates the prompt footprint of a message list in
// its rendered form: the sum of EstimateText over RenderMessage, plus
// the per-message overhead.
//
// The graph's compaction node mirrors this in JS
// (foundation/config/assets/graphs/nodes/compact.js) because a script
// node cannot call into Go yet — see flowcraft#539. The shared fixture
// in testdata keeps the two in step until it can.
func EstimateTokens(msgs []message.Message) int {
	total := 0
	for _, m := range msgs {
		total += EstimateText(RenderMessage(m)) + estimatePerMessage
	}
	return total
}

// isCJKUnit reports whether one UTF-16 code unit sits in a CJK block
// the estimate charges one token for.
func isCJKUnit(unit uint16) bool {
	return (unit >= 0x4e00 && unit <= 0x9fff) ||
		(unit >= 0x3400 && unit <= 0x4dbf) ||
		(unit >= 0x3040 && unit <= 0x30ff)
}

// compactJSON renders tool arguments without insignificant whitespace.
// A message crosses the script bridge as JSON, where the JS rendition
// only ever sees the parsed value and re-serializes it; rendering the
// caller's raw bytes here would make the two disagree about the same
// call. Escaping and key order are preserved, so the remaining
// difference is limited to how an exotic spelling (an explicit \uXXXX
// escape, an HTML-escaped character) is written out, never to which
// text the estimate covers. Invalid JSON is passed through unchanged.
func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}
