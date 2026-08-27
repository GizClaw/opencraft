// Package askuser provides the ask_user tool: it lets the model ask
// the user a question (free text, confirm, or choose from options)
// through the core prompt protocol.
package askuser

import (
	"context"
	"encoding/json"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/tool"

	"github.com/GizClaw/opencraft/internal/runtime"
)

// Name is the canonical ask_user tool name.
const Name = "ask_user"

// Tool asks the user a question via the core prompt protocol.
type Tool struct{}

// New creates the ask_user tool.
func New() *Tool { return &Tool{} }

// Definition implements tool.Tool.
func (t *Tool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		Name,
		"Ask the user a question and wait for the answer. "+
			"kind=text asks for a free-form answer; kind=confirm asks "+
			"a yes/no question; kind=select offers options to pick from "+
			"(the user may also type a custom direction). Use this when "+
			"you need a decision, a preference, or information only the "+
			"user has. Returns JSON: {\"choice\", \"text\", \"cancelled\"}.",
		message.ToolProperty("question", "string",
			"The question to ask (required)."),
		message.ToolEnumProperty("kind", "string",
			"text (default), confirm, or select.", "text", "confirm", "select"),
		message.ToolArrayProperty("options",
			"Options for kind=select, as plain strings (e.g. "+
				"[\"Option A\", \"Option B\"]). Ignored for text/confirm.",
			message.Items("string")),
		message.ToolProperty("multiple", "boolean",
			"kind=select only: allow picking several options at once "+
				"(default false)."),
		message.ToolPropertyWithDefault("allow_other", "boolean",
			"kind=select only: let the user type a custom answer that is "+
				"not in options (default true).", true),
	).Required("question").DisallowAdditionalProperties().Build()
}

// Metadata implements tool.ToolMetadata.
func (t *Tool) Metadata() tool.ToolMeta { return tool.ToolMeta{} }

// Execute implements tool.Tool.
func (t *Tool) Execute(ctx context.Context, arguments string) (string, error) {
	var args struct {
		Question string   `json:"question"`
		Kind     string   `json:"kind"`
		Options  []string `json:"options"`
		Multiple *bool    `json:"multiple"`
		Other    *bool    `json:"allow_other"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", errdefs.Validationf("ask_user: parse arguments: %v", err)
	}
	if args.Question == "" {
		return "", errdefs.Validationf("ask_user: question is required")
	}
	kind := runtime.Kind(args.Kind)
	switch kind {
	case "", runtime.KindText:
		kind = runtime.KindText
	case runtime.KindConfirm, runtime.KindSelect:
	default:
		return "", errdefs.Validationf("ask_user: invalid kind %q", args.Kind)
	}

	opts := make([]runtime.Option, 0, len(args.Options))
	for _, o := range args.Options {
		opts = append(opts, runtime.Option{Label: o, Value: o})
	}
	if kind == runtime.KindConfirm && len(opts) == 0 {
		opts = []runtime.Option{
			{Label: "Yes", Value: "yes"},
			{Label: "No", Value: "no"},
		}
	}
	if (kind == runtime.KindConfirm || kind == runtime.KindSelect) &&
		len(opts) == 0 {
		return "", errdefs.Validationf(
			"ask_user: kind %s requires options", kind)
	}
	multiple := args.Multiple != nil && *args.Multiple
	if multiple && kind != runtime.KindSelect {
		return "", errdefs.Validationf(
			"ask_user: multiple only applies to kind=select")
	}
	allowOther := args.Other == nil || *args.Other
	if kind != runtime.KindSelect {
		allowOther = false
	}

	host, ok := agent.HostFromContext(ctx)
	if !ok {
		return "", errdefs.NotAvailablef(
			"ask_user: no host in tool context")
	}
	rawOpts, _ := json.Marshal(opts)
	meta := map[string]string{
		runtime.MetaKind:    string(kind),
		runtime.MetaTitle:   args.Question,
		runtime.MetaOptions: string(rawOpts),
	}
	if multiple {
		meta[runtime.MetaMulti] = "true"
	}
	if !allowOther {
		meta[runtime.MetaAllowOther] = "false"
	}
	reply, err := host.AskUser(ctx, agent.UserPrompt{
		Parts:    []message.Part{message.TextPart{Text: args.Question}},
		Source:   "opencraft.ask_user",
		Metadata: meta,
	})
	if err != nil {
		return "", err
	}
	var choices []string
	if raw := reply.Metadata[runtime.MetaChoices]; raw != "" {
		_ = json.Unmarshal([]byte(raw), &choices)
	}
	out := map[string]any{
		"cancelled": reply.Metadata[runtime.MetaStatus] ==
			string(runtime.ReplyCancelled),
		"choice":  reply.Metadata[runtime.MetaChoice],
		"choices": choices,
		"other":   reply.Metadata[runtime.MetaOther],
		"text":    runtime.PartsText(reply.Parts),
	}
	res, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(res), nil
}
