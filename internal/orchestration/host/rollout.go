package host

import (
	"context"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/delegation"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/capabilities/rollout"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
)

// observeSink wraps a run's stream sink with the host's per-delta
// observation: the rollout recorder's item events, and the steer queue,
// where a boundary's drain is visible in the round it opened (see
// observeSteerQueue). It returns nil for a nil sink, so callers keep the
// "no sink, no streaming" contract.
func (h *Host) observeSink(next agent.StreamSink) agent.StreamSink {
	if next == nil {
		return nil
	}
	return &observedSink{host: h, next: next}
}

// observedSink is the wrapper observeSink installs. Beyond the
// per-delta observation it forwards the wrapped sink's delegation
// description (StreamTarget): core's exporter matches by capability,
// so a decorator that swallowed the description would silently disable
// cross-process streaming for delegated runs.
type observedSink struct {
	host *Host
	next agent.StreamSink
}

var (
	_ agent.StreamSink                = (*observedSink)(nil)
	_ delegation.StreamTargetProvider = (*observedSink)(nil)
)

// OnDelta implements agent.StreamSink.
func (s *observedSink) OnDelta(
	ctx context.Context,
	env event.Envelope,
	delta agent.StreamDeltaPayload,
) error {
	if agent.IsStreamDelta(env.Subject) {
		runID := interact.StreamRunID(env.Subject)
		s.host.onStreamRollout(ctx, runID, delta)
		s.host.observeSteerQueue(ctx, RunID(runID))
	}
	return s.next.OnDelta(ctx, env, delta)
}

// StreamTarget forwards the wrapped sink's description, or reports that
// there is none.
func (s *observedSink) StreamTarget() (delegation.StreamTarget, bool) {
	provider, ok := s.next.(delegation.StreamTargetProvider)
	if !ok {
		return delegation.StreamTarget{}, false
	}
	return provider.StreamTarget()
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
	// A delete may be in flight: reopening the recorder would recreate
	// the file under the removal and can make RemoveAll fail (Windows
	// refuses to delete open files).
	if h.deleting[id] || h.deleted[id] {
		h.mu.Unlock()
		return nil
	}
	store := h.store
	h.mu.Unlock()
	if store == nil {
		return nil
	}
	path, err := store.RolloutPath(conversationID)
	if err != nil {
		telemetry.WarnErr(ctx, "rollout: resolve path failed", err,
			otellog.String("conversation.id", conversationID))
		return nil
	}
	rec, err := rollout.Open(path)
	if err != nil {
		telemetry.WarnErr(ctx, "rollout: open recorder failed", err,
			otellog.String("conversation.id", conversationID))
		return nil
	}
	h.mu.Lock()
	if existing := h.rollouts[id]; existing != nil {
		h.mu.Unlock()
		telemetry.WarnErr(ctx, "rollout: close duplicate recorder failed",
			rec.Close())
		return existing
	}
	if h.deleting[id] || h.deleted[id] {
		h.mu.Unlock()
		telemetry.WarnErr(ctx, "rollout: close recorder after delete race",
			rec.Close())
		return nil
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
	if d != nil && delta.Type == agent.StreamDeltaFinish {
		d.requestID = delta.RequestID
		d.responseID = delta.ResponseID
	}
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
	for _, ev := range rollout.ItemEventsFromStream(conv, runID, delta) {
		h.recordRollout(ctx, rec, ev, "stream")
	}
	switch delta.Type {
	case agent.StreamDeltaPart:
		switch p := delta.Part.(type) {
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
		for _, ev := range rollout.FlushItemEvents(
			conv, runID, buf.reasoning.String(), buf.text.String(),
		) {
			h.recordRollout(ctx, rec, ev, "stream finish")
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
		u := rollout.FromUsage(usage)
		ev.Usage = &u
	}
	h.recordRollout(ctx, rec, ev, "turn end")
}

func (h *Host) closeRollouts() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, rec := range h.rollouts {
		telemetry.WarnErr(context.Background(),
			"rollout: close recorder failed", rec.Close())
		delete(h.rollouts, id)
	}
}
