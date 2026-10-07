// Package confirm provides the shared user-confirmation gate for
// mutating agent tools — today skill, plugin, subagent, automation and
// long-term-memory changes. Tools that change durable user state must
// not fire without an explicit user yes, so a prompt-injected model
// cannot silently persist them. Every prompt it raises is the fixed
// yes/no pair below; nothing else in the tree produces the source
// "opencraft.confirm".
package confirm

import (
	"context"
	"encoding/json"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/foundation/interact"
)

// OptionYes and OptionNo are the values this gate's fixed reply pair
// carries. OptionYes is exported because a caller that answers the
// prompt without a click must reply with the value a click produces
// (the desktop prompt backend's auto-approver does), and the two
// places would otherwise be one fact spelled twice.
const (
	OptionYes = "yes"
	OptionNo  = "no"
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
		{Label: "Yes", Value: OptionYes},
		{Label: "No", Value: OptionNo},
	})
	if err != nil {
		return false, errdefs.Validationf("confirm: marshal options: %v", err)
	}
	reply, err := host.AskUser(ctx, agent.UserPrompt{
		Parts:  []message.Part{message.TextPart{Text: body}},
		Source: "opencraft.confirm",
		Metadata: map[string]string{
			interact.MetaKind:     string(interact.KindConfirm),
			interact.MetaTitle:    title,
			interact.MetaOptions:  string(rawOpts),
			interact.MetaSeverity: string(interact.SeverityNotice),
		},
	})
	if err != nil {
		return false, err
	}
	if reply.Metadata[interact.MetaStatus] == string(interact.ReplyCancelled) {
		return false, nil
	}
	return reply.Metadata[interact.MetaChoice] == OptionYes, nil
}
