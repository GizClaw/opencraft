// Package compact provides the internal compact tool: it condenses a
// conversation prefix into a short summary and persists the artifact
// per conversation, so repeated compactions reuse previous work instead
// of re-summarizing the same messages.
package compact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/route"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/tool"

	"github.com/GizClaw/opencraft/internal/sessions"
)

// Name is the canonical compact tool name.
const Name = "compact"

// DefaultBudgetChars is the default summary budget in characters.
const DefaultBudgetChars = 4096

// SummaryPrefix marks a compaction summary injected into the
// conversation as a user message (codex-style). Lifecycle hooks and
// the compact graph node filter messages carrying this marker so the
// summary is never persisted as real conversation; compact.js keeps
// the literal in sync.
const SummaryPrefix = "Another language model started to solve this problem and produced a summary of its thinking process."

// compactStateName is the per-conversation JSON document holding the
// latest compaction artifact (session store WriteState), so a
// compaction does not re-summarize messages it already covered.
const compactStateName = "compact"

// Args is the compact tool input.
type Args struct {
	// Conversation is the messages to fold into the summary. Only the
	// newest overflow is expected here; messages already covered by a
	// previous compaction are merged in from the persisted artifact.
	Conversation []message.Message `json:"conversation"`
	// BudgetChars caps the summary length in characters.
	BudgetChars int `json:"budget_chars,omitempty"`
	// ConversationID identifies the per-conversation artifact store.
	ConversationID string `json:"conversation_id,omitempty"`
}

// artifact is the persisted compaction result for one conversation.
type artifact struct {
	// Covered is the cumulative set of folded message ids. Stored in
	// fold order so the union stays deterministic.
	Covered []string `json:"covered"`
	Summary string   `json:"summary"`
}

// generateFunc is the condensation entry; production wires the router,
// tests inject a fake.
type generateFunc func(
	ctx context.Context,
	req inference.GenerateRequest,
) (inference.GenerateResponse, error)

// Tool condenses conversation prefixes and persists the artifact. It
// is safe for concurrent use.
type Tool struct {
	store    *sessions.Store
	generate generateFunc
	mu       sync.Mutex
}

// New builds a compact tool whose condensation goes through the
// deployment router (same model selection/fallback as agent turns).
func New(router *route.Router, store *sessions.Store) *Tool {
	t := &Tool{store: store}
	if router != nil {
		t.generate = func(
			ctx context.Context,
			req inference.GenerateRequest,
		) (inference.GenerateResponse, error) {
			resp, _, err := router.Generate(ctx, req)
			return resp, err
		}
	}
	return t
}

var _ tool.Tool = (*Tool)(nil)

// Definition describes the compact tool. It is reserved for the graph's
// compaction node; the dynamic catalog exposes it as hidden.
func (t *Tool) Definition() message.ToolDefinition {
	msgSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"role": map[string]any{"type": "string"},
			"content": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"parts": map[string]any{"type": "array"},
				},
			},
		},
		"required": []any{"role"},
	}
	return message.DefineSchema(
		Name,
		"Compresses an internal conversation prefix into a compact summary. "+
			"Reserved for the graph compaction node; do not call directly.",
		message.ToolArrayProperty("conversation",
			"The full messages to fold, oldest first.", msgSchema),
		message.ToolProperty("budget_chars", "integer",
			"Maximum summary length in characters."),
		message.ToolProperty("conversation_id", "string",
			"Conversation id for the persisted compaction artifact."),
	).Required("conversation").Build()
}

// Metadata reports the tool's execution metadata.
func (t *Tool) Metadata() tool.ToolMeta {
	return tool.ToolMeta{}
}

// Execute condenses the given conversation prefix, merging any
// previously persisted artifact, and returns the summary.
func (t *Tool) Execute(ctx context.Context, arguments string) (string, error) {
	var args Args
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", errdefs.Validationf("compact: parse arguments: %v", err)
	}
	if len(args.Conversation) == 0 {
		return "", errdefs.Validationf("compact: conversation is required")
	}
	budget := args.BudgetChars
	if budget <= 0 {
		budget = DefaultBudgetChars
	}
	for i := range args.Conversation {
		if args.Conversation[i].Role == "" {
			args.Conversation[i].Role = message.RoleUser
		}
	}

	ids := make([]string, len(args.Conversation))
	covered := map[string]bool{}
	for i, m := range args.Conversation {
		ids[i] = stableID(m)
		covered[ids[i]] = true
	}

	var art artifact
	if args.ConversationID != "" {
		_ = t.store.ReadState(args.ConversationID, compactStateName, &art)
	}
	if art.Summary != "" && setsEqual(art.Covered, ids) {
		return art.Summary, nil
	}

	// Only the messages this artifact does not cover yet need to be
	// condensed; the previous summary already holds the rest. Marked
	// summary messages are skipped too: their content is already part
	// of the previous summary merged below.
	var fresh []message.Message
	for i, m := range args.Conversation {
		if art.Summary != "" && isSummaryMessage(RenderMessage(m)) {
			continue
		}
		if !containsID(art.Covered, ids[i]) {
			fresh = append(fresh, m)
		}
	}
	if len(fresh) == 0 {
		if art.Summary != "" {
			return art.Summary, nil
		}
		return "", errdefs.Validationf("compact: nothing new to compact")
	}
	if t.generate == nil {
		return "", errdefs.NotAvailablef("compact: condensation not configured")
	}

	raw := renderMessages(fresh)
	if art.Summary != "" {
		raw = art.Summary + "\n\n" + raw
	}
	resp, err := t.generate(ctx, condenseRequest(raw, budget))
	if err != nil {
		return "", fmt.Errorf("compact: condense: %w", err)
	}
	summary := strings.TrimSpace(resp.Message.Content.Text())
	if summary == "" {
		return "", errors.New("compact: condensation returned no text")
	}
	if runes := []rune(summary); len(runes) > budget {
		summary = string(runes[:budget])
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	merged := mergeIDs(art.Covered, ids)
	if args.ConversationID != "" {
		_ = t.store.WriteState(args.ConversationID, compactStateName, artifact{
			Covered: merged,
			Summary: summary,
		})
	}
	return summary, nil
}

// RenderMessage flattens one conversation message into its prompt
// form: text parts keep their content, tool calls and results render
// as tool_call / tool_result lines. It is the single rendering used by
// the compaction prompt and the artifact stable ids, and mirrors the
// commit-hook renderer so ids stay consistent with the persisted raw
// window (internal/memory/hooks.go).
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

// ToolActivity returns the rendered tool_call / tool_result lines of
// m, or nil when the message carries no tool activity. The commit hook
// uses it to decide whether a text rendering must be appended.
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

func stableID(m message.Message) string {
	sum := sha256.Sum256([]byte(string(m.Role) + "\x00" + RenderMessage(m)))
	return hex.EncodeToString(sum[:])
}

// isSummaryMessage reports whether text is a compaction summary
// message injected by the compact graph node.
func isSummaryMessage(text string) bool {
	return strings.HasPrefix(text, SummaryPrefix+"\n")
}

func renderMessages(msgs []message.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		role := string(m.Role)
		if role == "" {
			role = "user"
		}
		b.WriteString(role)
		b.WriteString(": ")
		b.WriteString(RenderMessage(m))
		b.WriteString("\n")
	}
	return b.String()
}

func condenseRequest(raw string, budget int) inference.GenerateRequest {
	maxOut := max(budget/3, 256)
	return inference.GenerateRequest{
		Input: inference.GenerateInput{
			Role: inference.InputRoleUser,
			Content: inference.InputContent{
				Content: message.Content{Parts: []message.Part{
					message.TextPart{Text: condensePrompt(raw)},
				}},
				Intent: inference.Intent{
					Text: &inference.TextIntent{MaxOutputTokens: &maxOut},
				},
			},
		},
	}
}

func condensePrompt(raw string) string {
	const instruction = `You are performing a CONTEXT CHECKPOINT COMPACTION. Create a handoff summary ` +
		`for another LLM that will resume the task.

Include:
- Current progress and key decisions made
- Important context, constraints, or user preferences
- What remains to be done (clear next steps)
- Any critical data, examples, or references needed to continue - file paths, commands run, tool results, and exact names or values that must not be paraphrased
Preserve the substance of any prior checkpoint summary below.

Be concise, structured, and focused on helping the next LLM seamlessly continue the work. Output only the summary, with no preamble.`
	return instruction + "\n\n" + raw
}

func containsID(ids []string, want string) bool {
	return slices.Contains(ids, want)
}

func setsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]bool, len(a))
	for _, id := range a {
		seen[id] = true
	}
	for _, id := range b {
		if !seen[id] {
			return false
		}
	}
	return true
}

func mergeIDs(prev, next []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(prev)+len(next))
	for _, id := range append(append([]string(nil), prev...), next...) {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
