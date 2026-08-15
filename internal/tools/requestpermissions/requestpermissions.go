// Package requestpermissions provides the request_permissions tool:
// the model can proactively ask the user to grant command permissions
// so later shell calls run without per-command approval prompts.
package requestpermissions

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/tool"

	"github.com/GizClaw/opencraft/internal/interact"
)

// Name is the canonical request_permissions tool name.
const Name = "request_permissions"

// Policy is the dynamic command permission surface a host may expose.
// The opencraft runtime implements it with the sandbox execpolicy
// manager (approvals are persisted to .opencraft/approvals.yaml).
type Policy interface {
	// AlwaysAllow adds a command prefix rule to the session allowlist
	// and persists it to the project approvals file.
	AlwaysAllow(rule string) error
}

// PolicyProvider is an optional Host capability exposing the exec
// policy to tools.
type PolicyProvider interface {
	ExecPolicy() Policy
}

// Tool asks the user to grant command permissions through the core
// prompt protocol.
type Tool struct{}

// New creates the request_permissions tool.
func New() *Tool { return &Tool{} }

// Definition implements tool.Tool.
func (t *Tool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		Name,
		"Request the user to grant additional command permissions. "+
			"Pass the command prefixes the task needs (e.g. "+
			"\"npm install\", \"git push\") plus a reason; the user "+
			"approves or denies the batch. Approved rules are added to "+
			"the session allowlist and persisted to the project's "+
			".opencraft/approvals.yaml, so later exec calls using them "+
			"run without a per-command prompt. Call this before running "+
			"commands you expect to be outside the allowlist. Returns "+
			"JSON: {granted, scope, permissions, cancelled}.",
		message.ToolArrayProperty("permissions",
			"Command prefix rules to grant, e.g. [\"npm install\", "+
				"\"go test\"]. A rule matches commands whose tokens "+
				"start with these tokens.",
			message.Items("string")),
		message.ToolProperty("reason", "string",
			"Why these permissions are needed, shown to the user "+
				"(recommended)."),
	).Required("permissions").DisallowAdditionalProperties().Build()
}

// Metadata implements tool.ToolMetadata.
func (t *Tool) Metadata() tool.ToolMeta {
	return tool.ToolMeta{MutatesState: true}
}

// Execute implements tool.Tool.
func (t *Tool) Execute(ctx context.Context, arguments string) (string, error) {
	var args struct {
		Permissions []string `json:"permissions"`
		Reason      string   `json:"reason"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", errdefs.Validationf(
			"%s: parse arguments: %v", Name, err)
	}
	rules := make([]string, 0, len(args.Permissions))
	for _, rule := range args.Permissions {
		rule = strings.Join(strings.Fields(rule), " ")
		if rule == "" {
			continue
		}
		rules = append(rules, rule)
	}
	if len(rules) == 0 {
		return "", errdefs.Validationf(
			"%s: at least one non-empty permission is required", Name)
	}

	host, ok := agent.HostFromContext(ctx)
	if !ok {
		return "", errdefs.NotAvailablef(
			"%s: no host in tool context", Name)
	}
	provider, ok := agent.CapabilityFromHost[PolicyProvider](host)
	if !ok || provider.ExecPolicy() == nil {
		return "", errdefs.NotAvailablef(
			"%s: runtime has no exec policy on the host", Name)
	}

	listed := strings.Join(rules, "\n  • ")
	prompt := "Grant these command permissions?\n  • " + listed
	if args.Reason != "" {
		prompt += "\n\nReason: " + args.Reason
	}
	opts, _ := json.Marshal([]interact.Option{
		{Label: "Grant", Value: "grant"},
		{Label: "Deny", Value: "deny"},
	})
	reply, err := host.AskUser(ctx, agent.UserPrompt{
		Parts:  []message.Part{message.TextPart{Text: prompt}},
		Source: "opencraft.request_permissions",
		Metadata: map[string]string{
			interact.MetaKind:       string(interact.KindSelect),
			interact.MetaTitle:      "Grant permissions?",
			interact.MetaOptions:    string(opts),
			interact.MetaAllowOther: "false",
		},
	})
	if err != nil {
		return "", err
	}
	cancelled := reply.Metadata[interact.MetaStatus] ==
		string(interact.ReplyCancelled)
	granted := !cancelled && reply.Metadata[interact.MetaChoice] == "grant"
	applied := make([]string, 0, len(rules))
	if granted {
		for _, rule := range rules {
			if err := provider.ExecPolicy().AlwaysAllow(rule); err != nil {
				return "", err
			}
			applied = append(applied, rule)
		}
	}
	payload, err := json.Marshal(map[string]any{
		"granted":     granted,
		"scope":       "session",
		"permissions": applied,
		"cancelled":   cancelled,
	})
	if err != nil {
		return "", errdefs.Internalf(
			"%s: encode result: %v", Name, err)
	}
	return string(payload), nil
}
