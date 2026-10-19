// Package summarytext centralizes the rendering and marker logic shared
// by the compact tool, the memory commit hooks, and the graph's
// compaction node. Keeping it in one Go package stops the same rules
// from drifting between capabilities.
package summarytext

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"unicode/utf16"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/message/media"
)

// SummaryPrefix marks a compaction summary injected into the
// conversation as a user message (codex-style). Lifecycle hooks and
// the graph compaction node filter messages carrying this marker so
// the summary is never persisted as real conversation.
const SummaryPrefix = "Another language model started to solve this problem and produced a summary of its thinking process."

// ContextNoticePrefix marks a harness notice injected into the
// conversation when automatic compaction can no longer keep the prompt
// inside the model's window (repeated condensation failures, or the
// per-turn fold budget spent). The notice exists so the model can wrap
// up — finish the step in flight, prefer short tool output — instead of
// running into a provider context error with no warning.
//
// It is a user-role message for provider portability, and it is filtered
// out of the persisted conversation exactly like a compaction summary:
// the graph node writes this literal, so the Go constant and
// graphs/nodes/compact.js must stay in step (pinned by
// foundation/config/graph_contract_test.go).
const ContextNoticePrefix = "Context notice:"

// IsContextNotice reports whether text is a harness context notice.
func IsContextNotice(text string) bool {
	return strings.HasPrefix(text, ContextNoticePrefix)
}

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
	// estimateImageFloor charges an image whose payload this estimate
	// cannot size: a URL source, where the prompt carries only the link
	// and the provider fetches the pixels itself.
	estimateImageFloor = 1024
	// estimateImageCharsPerToken calibrates an inline payload. It is
	// deliberately conservative: a real session showed a gateway billing
	// ~100 KB inline JPEGs at roughly 8k input tokens each (about one
	// token per 16 base64 characters), while the OpenAI-style tile
	// formula would charge far less. Under-estimating an image is what
	// lets a prompt past the model window, so the high end wins.
	estimateImageCharsPerToken = 16
)

// EstimateText estimates the prompt footprint of one rendered message
// text. The unit is the UTF-16 code point count, so an astral character
// is charged two units — the same way a provider's tokenizer and the
// UI's own measurements see it.
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
// This is the only implementation of the rule. The graph's compaction
// node used to mirror it in JS because a script node could not call into
// Go; it now reaches this function through the occompact script binding
// (capabilities/compaction), so the estimate the node budgets a fold
// against is the number every other reader sees.
func EstimateTokens(msgs []message.Message) int {
	total := 0
	for _, m := range msgs {
		total += EstimateText(RenderMessage(m)) + estimatePerMessage +
			imageTokensInParts(m.Content.Parts)
	}
	return total
}

// EstimateImageTokens approximates the prompt footprint of one image
// part. Text rendering cannot see this cost: the prompt carries the base64
// payload (or the URL) and a provider bills the pixels, so a conversation
// full of screenshots is far larger than its text alone.
//
// The unit is the base64 character count: that is the payload a provider
// actually receives, and it is what the calibration above was measured
// against.
func EstimateImageTokens(source media.ImageSource) int {
	chars := 0
	if source.Kind() == media.SourceInline {
		chars = base64.StdEncoding.EncodedLen(len(source.Bytes()))
	}
	tokens := chars / estimateImageCharsPerToken
	if tokens < estimateImageFloor {
		tokens = estimateImageFloor
	}
	return tokens
}

// imageTokensInParts sums the image cost of a part list, descending into
// tool results: a screenshot arrives as an image part inside the result's
// content, not as a top-level part of the tool message.
func imageTokensInParts(parts []message.Part) int {
	total := 0
	for _, p := range parts {
		switch part := p.(type) {
		case message.ImagePart:
			total += EstimateImageTokens(part.Source)
		case message.ToolResultPart:
			total += imageTokensInParts(part.Result.Content.Parts)
		}
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

// compactJSON renders tool arguments without insignificant whitespace, so
// the estimate charges one call the same regardless of how the caller
// formatted its arguments (a pretty-printed JSON blob and a compact one
// describe one call). Escaping and key order are preserved: only
// whitespace changes, never the text the estimate covers. Invalid JSON is
// passed through unchanged.
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
