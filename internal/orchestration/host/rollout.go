package host

import (
	"context"
	"strings"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/capabilities/rollout"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

func (h *Host) observeSink(next agent.StreamSink) agent.StreamSink {
	if next == nil {
		return nil
	}
	return agent.StreamSinkFunc(func(
		ctx context.Context,
		env event.Envelope,
		delta agent.StreamDeltaPayload,
	) error {
		if agent.IsStreamDelta(env.Subject) {
			h.onStreamRollout(ctx, streamRunID(env.Subject), delta)
		}
		return next.OnDelta(ctx, env, delta)
	})
}

func streamRunID(subject event.Subject) string {
	parts := strings.Split(string(subject), ".")
	if len(parts) >= 3 &&
		parts[1] == "run" &&
		(parts[0] == "agent" || parts[0] == "engine") {
		return parts[2]
	}
	return ""
}

func (h *Host) recordRollout(
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

// rolloutFor lazily opens the conversation's recorder.
func (h *Host) rolloutFor(
	ctx context.Context, conversationID string,
) *rollout.Recorder {
	id := ConversationID(conversationID)
	h.mu.Lock()
	if rec := h.rollouts[id]; rec != nil {
		h.mu.Unlock()
		return rec
	}
	store := h.store
	h.mu.Unlock()
	if store == nil {
		return nil
	}
	path, err := store.RolloutPath(conversationID)
	if err != nil {
		return nil
	}
	rec, err := rollout.Open(path)
	if err != nil {
		return nil
	}
	h.mu.Lock()
	if existing := h.rollouts[id]; existing != nil {
		h.mu.Unlock()
		_ = rec.Close()
		return existing
	}
	h.rollouts[id] = rec
	h.mu.Unlock()
	h.recordRollout(ctx, rec, rollout.Event{
		Type:           rollout.TypeThreadStarted,
		ConversationID: conversationID,
	}, "thread started")
	return rec
}

// onStreamRollout synthesizes item events from stream deltas.
func (h *Host) onStreamRollout(
	ctx context.Context, runID string, delta agent.StreamDeltaPayload,
) {
	h.mu.Lock()
	d := h.runs[RunID(runID)]
	conv := ""
	if d != nil {
		conv = d.contextID
	}
	h.mu.Unlock()
	if conv == "" {
		return
	}
	rec := h.rolloutFor(ctx, conv)
	if rec == nil {
		return
	}
	switch delta.Type {
	case agent.StreamDeltaPart:
		switch p := delta.Part.(type) {
		case message.ToolCallPart:
			h.recordRollout(ctx, rec, rollout.Event{
				Type:           rollout.TypeItemToolCall,
				ConversationID: conv,
				RunID:          runID,
				ItemID:         p.Call.ID,
				Tool:           p.Call.Name,
				CallID:         p.Call.ID,
				Arguments:      p.Call.Arguments,
			}, "tool call")
		case message.ToolResultPart:
			h.recordRollout(ctx, rec, rollout.Event{
				Type:           rollout.TypeItemToolResult,
				ConversationID: conv,
				RunID:          runID,
				CallID:         p.Result.CallID,
				Content:        p.Result.Content,
				IsError:        p.Result.IsError,
			}, "tool result")
		case message.ReasoningPart:
			h.rolloutBufferAppend(runID, true, p.Text)
		case message.TextPart:
			h.rolloutBufferAppend(runID, false, p.Text)
		}
	case agent.StreamDeltaFinish:
		buf := h.rolloutBufferTake(runID)
		if buf == nil {
			return
		}
		if buf.reasoning.Len() > 0 {
			h.recordRollout(ctx, rec, rollout.Event{
				Type: rollout.TypeItemReasoning, ConversationID: conv,
				RunID: runID, Content: buf.reasoning.String(),
			}, "reasoning")
		}
		if buf.text.Len() > 0 {
			h.recordRollout(ctx, rec, rollout.Event{
				Type: rollout.TypeItemAssistantMsg, ConversationID: conv,
				RunID: runID, Content: buf.text.String(),
			}, "assistant message")
		}
	}
}

func (h *Host) rolloutBufferAppend(runID string, reasoning bool, text string) {
	if text == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	d := h.runs[RunID(runID)]
	if d == nil {
		return
	}
	if d.buffer == nil {
		d.buffer = &rolloutBuffer{}
	}
	if reasoning {
		d.buffer.reasoning.WriteString(text)
	} else {
		d.buffer.text.WriteString(text)
	}
}

func (h *Host) rolloutBufferTake(runID string) *rolloutBuffer {
	h.mu.Lock()
	defer h.mu.Unlock()
	d := h.runs[RunID(runID)]
	if d == nil {
		return nil
	}
	buf := d.buffer
	d.buffer = nil
	return buf
}

func (h *Host) recordTurnEnd(
	ctx context.Context,
	conversationID, runID, typ, status, errText string,
	usage ocsessions.Usage,
) {
	rec := h.rolloutFor(ctx, conversationID)
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
	h.recordRollout(ctx, rec, ev, "turn end")
}

func (h *Host) closeRollouts() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, rec := range h.rollouts {
		_ = rec.Close()
		delete(h.rollouts, id)
	}
}
