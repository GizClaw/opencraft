package summarytext

import (
	"strconv"
	"strings"

	"github.com/GizClaw/flowcraft/core/message"
)

// mechanicalDefaultBudget mirrors compact.DefaultBudgetChars for callers
// that pass no budget. Every production caller hands in the configured
// budget; the fallback only keeps the shape of the digest stable.
const mechanicalDefaultBudget = 4096

// Mechanical digest caps. The digest exists to keep a fold usable when no
// model summary can be produced, so it stays small and factual instead of
// trying to reconstruct the conversation.
const (
	mechanicalAsks        = 5
	mechanicalTools       = 8
	mechanicalTail        = 3
	mechanicalSnippetRune = 100
	mechanicalPreviousCap = 2
)

// MechanicalSummary builds a model-free summary for a fold whose
// condensation call could not produce text (a reasoning-only response, a
// provider error). Folding without a model keeps the prompt shrinking —
// skipping the fold instead sends an over-window request next round and the
// provider rejects the whole turn — while the folded messages themselves
// stay durable in the archive side channel and in memory.
//
// The digest invents nothing: the previous summary comes through verbatim
// (halved at most, so this fold's own evidence fits), followed by a count
// of what was folded, the user asks in order, the tools that ran, and the
// tail of the folded region.
func MechanicalSummary(
	msgs []message.Message, prevSummary string, budget int,
) string {
	if budget <= 0 {
		budget = mechanicalDefaultBudget
	}
	var b strings.Builder
	if prev := strings.TrimSpace(prevSummary); prev != "" {
		b.WriteString(truncateRunes(prev, budget/mechanicalPreviousCap))
		b.WriteString("\n\n")
	}
	b.WriteString("## Folded rounds (mechanical digest)\n")
	b.WriteString("> Automatic summarization was unavailable for these rounds; " +
		"this block is extracted from the folded messages, not generated.\n\n")

	var users, assistants, tools int
	var asks, toolNames, tail []string
	seenTool := map[string]bool{}
	for _, m := range msgs {
		rendered := RenderMessage(m)
		// Marked summaries and harness notices are derived context: the
		// previous summary already rides along verbatim, and a notice is
		// not something the user asked for.
		derived := IsSummaryText(rendered) || IsContextNotice(rendered)
		switch m.Role {
		case message.RoleUser:
			users++
			if !derived {
				if snippet := firstLine(rendered); snippet != "" &&
					len(asks) < mechanicalAsks {
					asks = append(asks, snippet)
				}
			}
		case message.RoleAssistant:
			assistants++
		case message.RoleTool:
			tools++
		}
		collectToolNames(m, seenTool, &toolNames)
		if derived {
			continue
		}
		if snippet := firstLine(rendered); snippet != "" {
			tail = append(tail, string(m.Role)+": "+snippet)
			if len(tail) > mechanicalTail {
				tail = tail[1:]
			}
		}
	}
	b.WriteString("- folded: " + strconv.Itoa(len(msgs)) + " messages (user " +
		strconv.Itoa(users) + ", assistant " + strconv.Itoa(assistants) +
		", tool " + strconv.Itoa(tools) + ")\n")
	if len(asks) > 0 {
		b.WriteString("- user asks: " + quoteList(asks) + "\n")
	}
	if len(toolNames) > 0 {
		b.WriteString("- tools: " + strings.Join(toolNames, ", ") + "\n")
	}
	if len(tail) > 0 {
		b.WriteString("- last folded: " + quoteList(tail) + "\n")
	}
	return truncateRunes(strings.TrimSpace(b.String()), budget)
}

// collectToolNames records the distinct tool names a message called, in
// first-seen order. Tool results do not carry their name, so the calls are
// the only source.
func collectToolNames(m message.Message, seen map[string]bool, out *[]string) {
	for _, p := range m.Content.Parts {
		call, ok := p.(message.ToolCallPart)
		if !ok || call.Call.Name == "" || seen[call.Call.Name] {
			continue
		}
		if len(*out) >= mechanicalTools {
			return
		}
		seen[call.Call.Name] = true
		*out = append(*out, call.Call.Name)
	}
}

// firstLine renders one snippet: the first non-empty line of the message,
// bounded so a pasted log cannot take over the digest.
func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return truncateRunes(line, mechanicalSnippetRune)
		}
	}
	return ""
}

func quoteList(items []string) string {
	quoted := make([]string, 0, len(items))
	for _, item := range items {
		quoted = append(quoted, strconv.Quote(item))
	}
	return strings.Join(quoted, ", ")
}

// truncateRunes cuts s to at most n runes, never inside one, marking the
// cut with an ellipsis that still fits the budget.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return strings.TrimRight(string(runes[:n-1]), " \n") + "…"
}
