package core

import (
	"context"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/capabilities/tools/confirm"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
)

// confirmPromptSource is the prompt source of the shared confirmation
// gate — capabilities/tools/confirm.Confirm, the fixed yes/no card in
// front of durable side effects. Its consumers today are skill and
// plugin changes, the subagent lifecycle, automation removal and
// long-term memory writes, and every one of them raises that same
// pair through this one source. It is the only identity a YOLO
// conversation may answer itself: questions, permission requests and
// the sandbox prompts carry their own sources and never match.
const confirmPromptSource = "opencraft.confirm"

// autoApproveConfirm answers one confirmation without presenting it
// when the run's conversation is in YOLO mode: the mode is the user's
// standing "do not ask", and a confirmation card is the same
// interruption in a different dress. The answer is the value a click
// on Yes carries (confirm.OptionYes). Every way of failing to prove
// "the fixed yes/no card, in a YOLO conversation" falls back to
// asking, so nothing is approved by accident: another source, another
// shape sharing this source, a run this process did not mint, a
// workspace whose Host is not pooled right now, or a conversation with
// no readable settings.
func (c *Core) autoApproveConfirm(
	ctx context.Context,
	spec interact.Spec,
	conversationID string,
) (string, bool) {
	if spec.Source != confirmPromptSource || conversationID == "" {
		return "", false
	}
	// The source alone is not the shape: the reply below is a "yes",
	// so the spec must be the fixed yes/no card, options included. A
	// prompt sharing the source without it (a select, say) asks.
	if spec.Kind != interact.KindConfirm ||
		!offersOption(spec.Options, confirm.OptionYes) {
		return "", false
	}
	if c.Conversation == nil || c.Runtime == nil {
		return "", false
	}
	workDir := c.Conversation.WorkspaceForRun(spec.RunID)
	if workDir == "" {
		return "", false
	}
	h := c.Runtime.HostFor(workDir)
	if h == nil {
		return "", false
	}
	store := h.Sessions()
	if store == nil {
		return "", false
	}
	mode, err := store.Mode(ctx, conversationID)
	if err != nil || !mode.IsYOLO() {
		return "", false
	}
	telemetry.Info(ctx, "confirm auto-approved in yolo conversation",
		otellog.String("conversation", conversationID),
		otellog.String("run", spec.RunID),
		otellog.String("title", spec.Title))
	return confirm.OptionYes, true
}

// offersOption reports whether a prompt offers the given option value:
// an auto-answer must be one a click could have produced.
func offersOption(options []interact.Option, value string) bool {
	for _, option := range options {
		if option.Value == value {
			return true
		}
	}
	return false
}
