// Package confirm provides the shared user-confirmation gate for
// mutating agent tools (skill install/create/modify, subagent
// lifecycle). Tools that change durable host state must not fire
// without an explicit user yes, so a prompt-injected model cannot
// silently persist skills or agents.
package confirm

import (
	"context"
	"encoding/json"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/foundation/interact"
)

// Confirm presents a Yes/No interaction and reports whether the user
// approved. Ask failures are fail-closed. A cancelled reply is a
// "no", never an error.
func Confirm(ctx context.Context, title, body string) (bool, error) {
	host, ok := agent.HostFromContext(ctx)
	if !ok {
		return false, errdefs.NotAvailablef(
			"no host in tool context; user confirmation unavailable")
	}
	rawOpts, err := json.Marshal([]interact.Option{
		{Label: "Yes", Value: "yes"},
		{Label: "No", Value: "no"},
	})
	if err != nil {
		return false, errdefs.Validationf("confirm: marshal options: %v", err)
	}
	reply, err := host.AskUser(ctx, agent.UserPrompt{
		Parts:  []message.Part{message.TextPart{Text: body}},
		Source: "opencraft.confirm",
		Metadata: map[string]string{
			interact.MetaKind:    string(interact.KindConfirm),
			interact.MetaTitle:   title,
			interact.MetaOptions: string(rawOpts),
		},
	})
	if err != nil {
		return false, err
	}
	if reply.Metadata[interact.MetaStatus] == string(interact.ReplyCancelled) {
		return false, nil
	}
	return reply.Metadata[interact.MetaChoice] == "yes", nil
}
