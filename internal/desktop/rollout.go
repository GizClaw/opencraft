package desktop

import (
	"context"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/rollout"
	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
)

// recordRollout writes one event and surfaces write failures through
// telemetry instead of silently dropping the event stream.
func (a *App) recordRollout(
	ctx context.Context,
	rec *rollout.Recorder,
	ev rollout.Event,
	what string,
) {
	if rec == nil {
		return
	}
	if err := rec.Record(ev); err != nil {
		telemetry.Warn(ctx, "rollout: "+what+" write failed",
			otellog.String("conversation", ev.ConversationID),
			otellog.String("run", ev.RunID),
			otellog.String("type", ev.Type),
			otellog.String("error", err.Error()))
	}
}

// rolloutFor lazily opens the conversation's recorder and records
// thread.started on first open. Nil when the session store is absent
// or the path is invalid.
func (a *App) rolloutFor(
	ctx context.Context,
	conversationID string,
) *rollout.Recorder {
	a.mu.Lock()
	defer a.mu.Unlock()
	if rec, ok := a.rollouts[conversationID]; ok {
		return rec
	}
	if a.sessions == nil {
		return nil
	}
	path, err := a.sessions.RolloutPath(conversationID)
	if err != nil {
		return nil
	}
	rec, err := rollout.Open(path)
	if err != nil {
		return nil
	}
	a.rollouts[conversationID] = rec
	a.recordRollout(ctx, rec, rollout.Event{
		Type:           rollout.TypeThreadStarted,
		ConversationID: conversationID,
	}, "thread started")
	return rec
}

// onStreamRollout synthesizes item events from stream deltas: tool
// calls/results are recorded as they arrive; text and reasoning parts
// are buffered per run and flushed as whole items on the finish delta.
func (a *App) onStreamRollout(
	ctx context.Context,
	runID string,
	delta agent.StreamDeltaPayload,
) {
	a.mu.Lock()
	conv := a.runConvs[runID]
	a.mu.Unlock()
	if conv == "" {
		return
	}
	rec := a.rolloutFor(ctx, conv)
	if rec == nil {
		return
	}
	switch delta.Type {
	case agent.StreamDeltaPart:
		switch p := delta.Part.(type) {
		case message.ToolCallPart:
			a.recordRollout(ctx, rec, rollout.Event{
				Type:           rollout.TypeItemToolCall,
				ConversationID: conv,
				RunID:          runID,
				ItemID:         p.Call.ID,
				Tool:           p.Call.Name,
				CallID:         p.Call.ID,
				Arguments:      p.Call.Arguments,
			}, "tool call")
		case message.ToolResultPart:
			a.recordRollout(ctx, rec, rollout.Event{
				Type:           rollout.TypeItemToolResult,
				ConversationID: conv,
				RunID:          runID,
				CallID:         p.Result.CallID,
				Content:        p.Result.Content,
				IsError:        p.Result.IsError,
			}, "tool result")
		case message.ReasoningPart:
			a.rolloutBufferAppend(runID, true, p.Text)
		case message.TextPart:
			a.rolloutBufferAppend(runID, false, p.Text)
		}
	case agent.StreamDeltaFinish:
		buf := a.rolloutBufferTake(runID)
		if buf == nil {
			return
		}
		if buf.reasoning.Len() > 0 {
			a.recordRollout(ctx, rec, rollout.Event{
				Type: rollout.TypeItemReasoning, ConversationID: conv,
				RunID: runID, Content: buf.reasoning.String(),
			}, "reasoning")
		}
		if buf.text.Len() > 0 {
			a.recordRollout(ctx, rec, rollout.Event{
				Type: rollout.TypeItemAssistantMsg, ConversationID: conv,
				RunID: runID, Content: buf.text.String(),
			}, "assistant message")
		}
	}
}

func (a *App) rolloutBufferAppend(runID string, reasoning bool, text string) {
	if text == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	buf := a.rolloutBufs[runID]
	if buf == nil {
		buf = &rolloutBuffer{}
		a.rolloutBufs[runID] = buf
	}
	if reasoning {
		buf.reasoning.WriteString(text)
	} else {
		buf.text.WriteString(text)
	}
}

func (a *App) rolloutBufferTake(runID string) *rolloutBuffer {
	a.mu.Lock()
	defer a.mu.Unlock()
	buf := a.rolloutBufs[runID]
	delete(a.rolloutBufs, runID)
	return buf
}

// recordTurnEnd writes turn.completed or turn.failed with usage.
func (a *App) recordTurnEnd(
	ctx context.Context,
	conversationID, runID, typ, status, errText string,
	usage ocsessions.Usage,
) {
	rec := a.rolloutFor(ctx, conversationID)
	if rec == nil {
		return
	}
	ev := rollout.Event{
		Type: typ, ConversationID: conversationID, RunID: runID,
		Status: status, Error: errText,
	}
	if usage.TotalTokens > 0 {
		ev.Usage = &rollout.Usage{
			InputTokens:     usage.InputTokens,
			OutputTokens:    usage.OutputTokens,
			CacheReadTokens: usage.CacheReadTokens,
			ReasoningTokens: usage.ReasoningTokens,
			TotalTokens:     usage.TotalTokens,
			LatencyMs:       usage.LatencyMs,
		}
	}
	a.recordRollout(ctx, rec, ev, "turn end")
}

// closeRollouts closes every open recorder.
func (a *App) closeRollouts() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, rec := range a.rollouts {
		_ = rec.Close()
		delete(a.rollouts, id)
	}
}
