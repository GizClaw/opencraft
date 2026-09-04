// This file owns the Host-level side of OpenCraft's external lifecycle
// hooks. The capabilities/hooks package implements parsing and command
// execution; Host is the facade that resolves the runtime's hooks
// manager once and fires turn/session events through it, so UI,
// headless and automation runs share one set of lifecycle semantics.
package host

import (
	"context"

	"github.com/GizClaw/opencraft/internal/capabilities/hooks"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

// FireHook dispatches one external lifecycle event through the
// runtime's hooks manager. The event field is filled in when the
// payload omits it. Hook execution never returns an error: failures
// are telemetry-logged by the hooks package and skipped.
func (h *Host) FireHook(ctx context.Context, event string, payload map[string]any) {
	if h == nil {
		return
	}
	mgr := h.hooks
	if mgr == nil || mgr.Empty() {
		return
	}
	if payload == nil {
		payload = make(map[string]any, 1)
	}
	if _, ok := payload["event"]; !ok {
		payload["event"] = event
	}
	mgr.Fire(ctx, event, payload)
}

// fireUserPromptSubmit fires the UserPromptSubmit hook for one turn
// request. It runs before the session lease opens, mirroring the old
// desktop adapter behavior where the hook observed the prompt before
// any workspace snapshot or engine turn.
func (h *Host) fireUserPromptSubmit(
	ctx context.Context, conversationID, prompt string,
) {
	h.FireHook(ctx, hooks.EventUserPromptSubmit, map[string]any{
		"conversation_id": conversationID,
		"prompt":          prompt,
	})
}

// fireTurnEnd fires the TurnEnd hook after the run's usage, rollout,
// and artifact post-processing have settled.
func (h *Host) fireTurnEnd(
	ctx context.Context,
	conversationID, runID, status, errText string,
	usage ocsessions.Usage,
) {
	h.FireHook(ctx, hooks.EventTurnEnd, map[string]any{
		"conversation_id": conversationID,
		"run_id":          runID,
		"status":          status,
		"error":           errText,
		"usage": map[string]int64{
			"input_tokens":     usage.InputTokens,
			"output_tokens":    usage.OutputTokens,
			"total_tokens":     usage.TotalTokens,
			"reasoning_tokens": usage.ReasoningTokens,
		},
	})
}
