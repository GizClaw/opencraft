package core

import (
	"context"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/orchestration/interact"
)

// confirmPromptSource is the prompt source of the shared confirmation
// gate for durable side effects (skill and plugin installs, subagent
// lifecycle, automation creation; see capabilities/tools/confirm). It
// is the narrow identity a YOLO conversation may answer itself:
// questions, permission requests and sandbox approvals carry their own
// sources and still reach the user.
const confirmPromptSource = "opencraft.confirm"

// autoApproveConfirm answers one confirmation without presenting it
// when the run's conversation is in YOLO mode: the mode is the user's
// standing "do not ask", and a confirmation card is the same
// interruption in a different dress. Every way of failing to prove
// "YOLO conversation" falls back to asking, so nothing is approved by
// accident: an unknown source, a run this process did not mint, a
// workspace whose Host is not pooled right now, or a conversation with
// no readable settings.
func (c *Core) autoApproveConfirm(
	ctx context.Context,
	spec interact.Spec,
	conversationID string,
) bool {
	if spec.Source != confirmPromptSource || conversationID == "" {
		return false
	}
	if c.Conversation == nil || c.Runtime == nil {
		return false
	}
	workDir := c.Conversation.WorkspaceForRun(spec.RunID)
	if workDir == "" {
		return false
	}
	h := c.Runtime.HostFor(workDir)
	if h == nil {
		return false
	}
	store := h.Sessions()
	if store == nil {
		return false
	}
	mode, err := store.Mode(ctx, conversationID)
	if err != nil || !mode.IsYOLO() {
		return false
	}
	telemetry.Info(ctx, "confirm auto-approved in yolo conversation",
		otellog.String("conversation", conversationID),
		otellog.String("run", spec.RunID),
		otellog.String("title", spec.Title))
	return true
}
