// Package remember provides the remember tool: the model's way to store
// a durable fact (a user preference, an environment fact, a project
// convention) in the user-level long-term memory, where it is injected
// into every later turn.
//
// Writes are deliberate and confirmed: the tool asks the user before
// anything is stored, applies the same secret rules the tool-result
// middleware applies, and refuses text that looks like a credential
// instead of storing a redacted version of it. All stores go through
// the userstore.Memory write surface, so dedupe, limits and provenance
// are applied exactly once — the settings page and an accepted review
// suggestion write through the same path.
package remember

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"github.com/GizClaw/flowcraft/core/tool"
	"go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/capabilities/memory/userstore"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/assembly"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/confirm"
)

// Name is the canonical remember tool name.
const Name = "remember"

// Operation kinds the tool accepts.
const (
	OpAdd     = "add"
	OpReplace = "replace"
	OpRemove  = "remove"
)

// Defaults and bounds. The store owns the hard limits
// (userstore.MaxItems / MaxTextBytes); these keep one call small enough
// to review in a single confirmation.
const (
	defaultMaxOperations = 5
	maxOperationsLimit   = 20
	defaultMaxTextChars  = 600
	minMaxTextChars      = 40
	maxMaxTextChars      = 4000
)

// Settings is the tool resource's settings subtree. Redact carries the
// same rules the result middleware applies, so the deploy document has
// one place to say what a secret looks like.
type Settings struct {
	// WorkDir is the workspace root a workspace-scoped fact is filed
	// under (the same key the settings page and the review queue use).
	WorkDir string `json:"work_dir,omitempty"`
	// MaxOperations caps how many operations one call may carry.
	MaxOperations int `json:"max_operations,omitempty"`
	// MaxTextChars caps one fact's text in characters.
	MaxTextChars int `json:"max_text_chars,omitempty"`
	// Redact enables refusal of text that matches a secret rule.
	Redact assembly.RedactSettings `json:"redact,omitempty"`
}

// Config is the validated, defaulted view the tool runs with.
type Config struct {
	WorkDir       string
	MaxOperations int
	MaxTextChars  int
}

// Resolve validates the settings and applies defaults.
func (s Settings) Resolve() (Config, error) {
	out := Config{
		WorkDir:       strings.TrimSpace(s.WorkDir),
		MaxOperations: s.MaxOperations,
		MaxTextChars:  s.MaxTextChars,
	}
	if out.MaxOperations == 0 {
		out.MaxOperations = defaultMaxOperations
	}
	if out.MaxOperations < 1 || out.MaxOperations > maxOperationsLimit {
		return Config{}, errdefs.Validationf(
			"remember: max_operations %d out of range (1-%d)",
			out.MaxOperations, maxOperationsLimit)
	}
	if out.MaxTextChars == 0 {
		out.MaxTextChars = defaultMaxTextChars
	}
	if out.MaxTextChars < minMaxTextChars || out.MaxTextChars > maxMaxTextChars {
		return Config{}, errdefs.Validationf(
			"remember: max_text_chars %d out of range (%d-%d)",
			out.MaxTextChars, minMaxTextChars, maxMaxTextChars)
	}
	return out, nil
}

// toolName overrides the tool name in tests.
var toolName = Name

// Tool is the LLM-callable remember tool.
type Tool struct {
	memory  userstore.Memory
	cfg     Config
	redact  func(string) string
	confirm func(ctx context.Context, title, body string) (bool, error)
}

var _ tool.Tool = (*Tool)(nil)

// New builds the tool over a memory store. A store with no user
// database behind it yields an error: the caller decides whether to
// contribute the tool at all (the deploy factory does not).
func New(
	ctx context.Context, memory userstore.Memory, settings Settings,
) (*Tool, error) {
	if memory == nil || memory.Empty() {
		return nil, errdefs.NotAvailablef(
			"remember: no user database in this runtime")
	}
	cfg, err := settings.Resolve()
	if err != nil {
		return nil, err
	}
	redact, err := assembly.CompileTextRedactor(settings.Redact)
	if err != nil {
		return nil, err
	}
	_ = ctx
	return &Tool{
		memory:  memory,
		cfg:     cfg,
		redact:  redact,
		confirm: confirm.Confirm,
	}, nil
}

// Operation is one requested memory change.
type Operation struct {
	// Op is add, replace or remove.
	Op string `json:"op"`
	// ID identifies the fact for replace and remove.
	ID string `json:"id,omitempty"`
	// Text is the fact's text for add and replace.
	Text string `json:"text,omitempty"`
	// Scope is "workspace" (this project) or "global" (the user); the
	// default is workspace, the narrower one.
	Scope string `json:"scope,omitempty"`
	// Kind labels the fact: preference, environment, convention, fact.
	Kind string `json:"kind,omitempty"`
}

// Definition implements tool.Tool. The tool is always visible to the
// model that has it (it is not part of the discovery pool): storing a
// fact is a decision the model should make while it has the context,
// not after a search round.
func (t *Tool) Definition() message.ToolDefinition {
	itemSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"op": map[string]any{
				"type":        "string",
				"description": "add a new fact, replace an existing one's text, or remove one.",
				"enum":        []any{OpAdd, OpReplace, OpRemove},
			},
			"id": map[string]any{
				"type":        "string",
				"description": "Fact id, required for replace and remove.",
			},
			"text": map[string]any{
				"type": "string",
				"description": "The fact, one self-contained sentence " +
					"that reads correctly with no other context.",
			},
			"scope": map[string]any{
				"type": "string",
				"description": "workspace for a fact about this " +
					"project, global for a fact about the user or " +
					"their machine. Defaults to workspace.",
				"enum": []any{userstore.ScopeWorkspace, userstore.ScopeGlobal},
			},
			"kind": map[string]any{
				"type": "string",
				"description": "preference, environment, convention or " +
					"fact. Free-form; a short label.",
			},
		},
		"required":             []any{"op"},
		"additionalProperties": false,
	}
	return message.DefineSchema(
		toolName,
		"Store a durable fact in long-term memory so it survives this "+
			"conversation and is known in later sessions: a user "+
			"preference, a machine or tooling fact, a project convention. "+
			"The user confirms every change, and stored facts are injected "+
			"into every later turn of this workspace — so remember what "+
			"stays true next week, not what happened in this task. Do not "+
			"store secrets, credentials or task state.",
		message.ToolArrayProperty(
			"operations",
			"One or more memory changes, applied in order.",
			itemSchema),
	).Required("operations").DisallowAdditionalProperties().Build()
}

// Metadata implements tool.Tool: the tool mutates durable user state.
func (t *Tool) Metadata() tool.ToolMeta {
	return tool.ToolMeta{MutatesState: true}
}

// Execute implements tool.Tool. The result is one text part carrying
// the JSON envelope.
func (t *Tool) Execute(
	ctx context.Context, arguments string,
) (message.Content, error) {
	var args struct {
		Operations []Operation `json:"operations"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return message.Content{}, errdefs.Validationf(
			"remember: invalid arguments: %v", err)
	}
	ops, err := t.prepare(args.Operations)
	if err != nil {
		return message.Content{}, err
	}
	title, body := confirmation(ops)
	approved, err := t.confirm(ctx, title, body)
	if err != nil {
		return message.Content{}, err
	}
	if !approved {
		return message.Content{}, errdefs.Validationf(
			"remember: the user declined these memory changes; nothing was stored")
	}
	env := envelope{Applied: make([]applied, 0, len(ops))}
	for _, op := range ops {
		out, err := t.apply(ctx, op)
		if err != nil {
			// Partial application is reported by returning the error:
			// the envelope would otherwise claim a success that did not
			// happen. Already-applied operations stay applied (each one
			// is a deliberate, confirmed change).
			return message.Content{}, err
		}
		env.Applied = append(env.Applied, out)
	}
	env.Note = fmt.Sprintf(
		"%d change(s) stored; they are now part of this workspace's long-term memory",
		len(env.Applied))
	encoded, err := json.Marshal(env)
	if err != nil {
		return message.Content{}, errdefs.Internalf(
			"remember: encode result: %v", err)
	}
	telemetry.Info(ctx, "remember: memory updated",
		log.Int("operations", len(env.Applied)))
	return message.NewTextContent(string(encoded)), nil
}

// prepare validates every operation before anything is written, so a
// call with one bad entry changes nothing. Text is checked against the
// configured secret rules here: a fact that looks like a credential is
// refused, not stored redacted (a silently altered memory is worse than
// a refusal the model can report).
func (t *Tool) prepare(ops []Operation) ([]Operation, error) {
	if len(ops) == 0 {
		return nil, errdefs.Validationf(
			"remember: at least one operation is required")
	}
	if len(ops) > t.cfg.MaxOperations {
		return nil, errdefs.Validationf(
			"remember: %d operations exceed the limit of %d per call",
			len(ops), t.cfg.MaxOperations)
	}
	out := make([]Operation, 0, len(ops))
	for i, op := range ops {
		normalized, err := t.prepareOne(i, op)
		if err != nil {
			return nil, err
		}
		out = append(out, normalized)
	}
	return out, nil
}

func (t *Tool) prepareOne(index int, op Operation) (Operation, error) {
	op.Op = strings.ToLower(strings.TrimSpace(op.Op))
	op.ID = strings.TrimSpace(op.ID)
	op.Text = strings.TrimSpace(op.Text)
	op.Kind = strings.TrimSpace(op.Kind)
	switch op.Op {
	case OpAdd, OpReplace:
	case OpRemove:
	case "":
		return Operation{}, errdefs.Validationf(
			"remember: operations[%d].op is required", index)
	default:
		return Operation{}, errdefs.Validationf(
			"remember: operations[%d].op %q is not one of add, replace, remove",
			index, op.Op)
	}
	if op.Op == OpRemove {
		if op.ID == "" {
			return Operation{}, errdefs.Validationf(
				"remember: operations[%d] remove needs the fact id", index)
		}
		return op, nil
	}
	if op.Text == "" {
		return Operation{}, errdefs.Validationf(
			"remember: operations[%d] needs text", index)
	}
	if op.Op == OpReplace && op.ID == "" {
		return Operation{}, errdefs.Validationf(
			"remember: operations[%d] replace needs the fact id", index)
	}
	if len([]rune(op.Text)) > t.cfg.MaxTextChars {
		return Operation{}, errdefs.Validationf(
			"remember: operations[%d] text is %d characters, over the %d limit; "+
				"keep it to a sentence or two",
			index, len([]rune(op.Text)), t.cfg.MaxTextChars)
	}
	if t.redact != nil && t.redact(op.Text) != op.Text {
		return Operation{}, errdefs.Validationf(
			"remember: operations[%d] text matches a configured secret rule "+
				"and was not stored; credentials do not belong in long-term memory",
			index)
	}
	switch op.Scope {
	case "":
		op.Scope = userstore.ScopeWorkspace
	case userstore.ScopeWorkspace, userstore.ScopeGlobal:
	default:
		return Operation{}, errdefs.Validationf(
			"remember: operations[%d].scope %q is not one of %s, %s",
			index, op.Scope, userstore.ScopeWorkspace, userstore.ScopeGlobal)
	}
	return op, nil
}

// apply performs one validated operation through the store.
func (t *Tool) apply(ctx context.Context, op Operation) (applied, error) {
	switch op.Op {
	case OpAdd:
		workspace := ""
		if op.Scope == userstore.ScopeWorkspace {
			workspace = t.cfg.WorkDir
		}
		fact, err := t.memory.Add(ctx, userstore.Fact{
			Kind:      op.Kind,
			Scope:     op.Scope,
			Text:      op.Text,
			Workspace: workspace,
		})
		if err != nil {
			return applied{}, err
		}
		return applied{
			Op: op.Op, ID: fact.ID, Scope: fact.Scope, Text: fact.Text,
		}, nil
	case OpReplace:
		fact, err := t.memory.Replace(ctx, op.ID, op.Text)
		if err != nil {
			return applied{}, err
		}
		return applied{
			Op: op.Op, ID: fact.ID, Scope: fact.Scope, Text: fact.Text,
		}, nil
	default:
		if err := t.memory.Remove(ctx, op.ID); err != nil {
			return applied{}, err
		}
		return applied{Op: op.Op, ID: op.ID}, nil
	}
}

// confirmation renders the one prompt the whole call is gated on.
func confirmation(ops []Operation) (string, string) {
	var b strings.Builder
	for _, op := range ops {
		switch op.Op {
		case OpAdd:
			fmt.Fprintf(&b, "Remember (%s): %s\n", op.Scope, op.Text)
		case OpReplace:
			fmt.Fprintf(&b, "Update %s to: %s\n", op.ID, op.Text)
		case OpRemove:
			fmt.Fprintf(&b, "Forget: %s\n", op.ID)
		}
	}
	return "Remember this?", strings.TrimRight(b.String(), "\n")
}

// envelope is the tool result shape.
type envelope struct {
	Applied []applied `json:"applied"`
	Note    string    `json:"note,omitempty"`
}

// applied is one stored change, carrying the id a later call needs to
// replace or remove it.
type applied struct {
	Op    string `json:"op"`
	ID    string `json:"id"`
	Scope string `json:"scope,omitempty"`
	Text  string `json:"text,omitempty"`
}
