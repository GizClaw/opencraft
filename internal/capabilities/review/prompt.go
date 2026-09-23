package review

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/capabilities/memory/userstore"
)

// Candidate is one proposed durable fact: the payload of a memory
// suggestion and the shape the settings page renders. Scope is
// "global" or "workspace"; everything else the store derives on write.
type Candidate struct {
	Text   string `json:"text"`
	Scope  string `json:"scope,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// reviewSystemPrompt is the whole instruction set of the review call.
// It is deliberately narrow: propose only facts that stay true next
// week, in a fixed JSON shape, and prefer an empty answer over a guess.
const reviewSystemPrompt = `You review one finished turn of a local coding agent and propose what is worth remembering across future sessions.

Report only durable facts: user preferences, environment and tooling facts, project conventions, decisions that bind later work. Never report task state (what was in progress, what file was open), one-off values (paths of temporary files, ids, timestamps), secrets or credentials, or anything already listed as an existing memory.

Answer with JSON only, no prose and no code fence:
{"memory":[{"text":"<one self-contained sentence>","scope":"global|workspace","kind":"preference|environment|convention|fact","reason":"<why this stays true>"}]}

Rules:
- "workspace" scope is for facts about this project; "global" for facts about the user or their machine.
- Text must stand alone: a future reader sees only that sentence.
- At most %d item(s). An empty list is the correct answer for an ordinary turn.`

// promptInput is everything the review prompt is built from.
type promptInput struct {
	Turn     runTurn
	Facts    []userstore.Fact
	Skills   []string
	WorkDir  string
	MaxItems int
}

// buildPrompt bounds the inputs that ride into the prompt. The caller
// passes what it has; the limits live here, next to the template that
// depends on them.
func buildPrompt(in promptInput) promptInput {
	if len(in.Facts) > maxPromptFacts {
		in.Facts = in.Facts[:maxPromptFacts]
	}
	if len(in.Skills) > maxPromptSkills {
		in.Skills = in.Skills[:maxPromptSkills]
	}
	if in.MaxItems <= 0 {
		in.MaxItems = 1
	}
	return in
}

// contextMessages is the fixed prefix of the request.
func (p promptInput) contextMessages() []message.Message {
	if p.MaxItems <= 0 {
		p.MaxItems = 1
	}
	return []message.Message{{
		Role: message.RoleSystem,
		Content: message.NewTextContent(fmt.Sprintf(
			reviewSystemPrompt, p.MaxItems)),
	}}
}

// userText renders the turn, what is already remembered and which
// skills exist.
func (p promptInput) userText() string {
	var b strings.Builder
	b.WriteString("Workspace: ")
	b.WriteString(displayPath(p.WorkDir))
	b.WriteString("\n\n## Finished turn\n")
	fmt.Fprintf(&b, "Status: %s\n", p.Turn.Status)
	if p.Turn.ToolCalls > 0 {
		fmt.Fprintf(&b, "Tool calls: %d", p.Turn.ToolCalls)
		if len(p.Turn.Tools) > 0 {
			fmt.Fprintf(&b, " (%s)", strings.Join(p.Turn.Tools, ", "))
		}
		b.WriteString("\n")
	}
	if p.Turn.Request != "" {
		b.WriteString("\nUser request:\n")
		b.WriteString(indent(p.Turn.Request))
	}
	if p.Turn.Answer != "" {
		b.WriteString("\nAssistant answer:\n")
		b.WriteString(indent(p.Turn.Answer))
	}
	b.WriteString("\n## Existing memory (do not repeat these)\n")
	if len(p.Facts) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, fact := range p.Facts {
			fmt.Fprintf(&b, "- [%s] %s\n", scopeLabel(fact.Scope), fact.Text)
		}
	}
	if len(p.Skills) > 0 {
		b.WriteString("\n## Installed skills (already written down; do not restate)\n")
		b.WriteString(strings.Join(p.Skills, ", "))
		b.WriteString("\n")
	}
	return b.String()
}

func scopeLabel(scope string) string {
	if scope == userstore.ScopeWorkspace {
		return "workspace"
	}
	return "global"
}

func displayPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "(unknown)"
	}
	return path
}

// indent prefixes every line so the excerpt cannot be mistaken for the
// prompt's own structure.
func indent(text string) string {
	var b strings.Builder
	for _, line := range strings.Split(text, "\n") {
		b.WriteString("  ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// maxParsedCandidates bounds the answer's candidate list before it is
// judged. It is deliberately not the configured cap: the configured
// number says how many suggestions one review may *queue*, and the
// duplicates that dedupe drops must not consume that budget — a model
// that proposes the same fact twice would otherwise queue one
// suggestion where two distinct ones were available.
const maxParsedCandidates = 32

// parseCandidates decodes the model's answer. A model answer is not
// user input: unknown keys inside an item are ignored (a chatty model
// adding "confidence" must not throw away a usable candidate), while
// everything that reaches the queue is validated on its own terms
// before it is stored.
func parseCandidates(text string, limit int) ([]Candidate, error) {
	body, err := extractJSONObject(text)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Memory []Candidate `json:"memory"`
	}
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		return nil, fmt.Errorf("decode answer: %w", err)
	}
	out := make([]Candidate, 0, len(answer.Memory))
	for _, candidate := range answer.Memory {
		candidate.Text = strings.TrimSpace(candidate.Text)
		if candidate.Text == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(candidate.Scope)) {
		case "workspace":
			candidate.Scope = userstore.ScopeWorkspace
		case "", "global":
			candidate.Scope = userstore.ScopeGlobal
		default:
			// An unknown scope is not worth dropping the candidate for:
			// the safe default is the narrower one.
			candidate.Scope = userstore.ScopeWorkspace
		}
		candidate.Kind = strings.ToLower(strings.TrimSpace(candidate.Kind))
		candidate.Reason = strings.TrimSpace(candidate.Reason)
		out = append(out, candidate)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// extractJSONObject returns the first balanced {...} block in text,
// tolerating surrounding prose or a code fence.
func extractJSONObject(text string) (string, error) {
	start := strings.IndexByte(text, '{')
	if start < 0 {
		return "", errdefs.Internalf("no JSON object in answer")
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
		c := text[i]
		switch {
		case escaped:
			escaped = false
		case c == '\\' && inString:
			escaped = true
		case c == '"':
			inString = !inString
		case inString:
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return text[start : i+1], nil
			}
		}
	}
	return "", errdefs.Internalf("unterminated JSON object in answer")
}

// ApplyMemory writes one memory candidate through the store. It is the
// single accepted-write path: the settings page accepting a review
// suggestion and the remember tool both land here, so dedupe, limits
// and provenance are applied once.
func ApplyMemory(
	ctx context.Context,
	memory userstore.Memory,
	workspace string,
	payload json.RawMessage,
) (userstore.Fact, error) {
	if memory == nil || memory.Empty() {
		return userstore.Fact{}, errdefs.NotAvailablef(
			"review: no user database in this runtime")
	}
	var candidate Candidate
	if err := json.Unmarshal(payload, &candidate); err != nil {
		return userstore.Fact{}, errdefs.Validationf(
			"review: suggestion payload: %v", err)
	}
	fact := userstore.Fact{
		Kind: candidate.Kind,
		Text: candidate.Text,
	}
	if candidate.Scope == userstore.ScopeWorkspace {
		fact.Scope = userstore.ScopeWorkspace
		fact.Workspace = workspace
	} else {
		fact.Scope = userstore.ScopeGlobal
	}
	return memory.Add(ctx, fact)
}
