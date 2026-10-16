package host

import (
	"context"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/delegation"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"go.opentelemetry.io/otel/attribute"

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
			attribute.String("conversation", ev.ConversationID),
			attribute.String("run", ev.RunID),
			attribute.String("type", ev.Type),
			attribute.String("error", err.Error()))
	}
}

// rolloutFor lazily opens the conversation's recorder.
//
// At most maxOpenRollouts recorders stay open: each one pins a file
// descriptor for the Host's lifetime, so a long-lived Host streaming
// many conversations would otherwise walk into the process fd limit
// and start failing writes silently. An evicted conversation reopens
// on its next event — the file is append-only and Open recovers the
// sequence number from its tail, so nothing is lost.
func (h *Host) rolloutFor(
	ctx context.Context, conversationID string,
) *rollout.Recorder {
	id := ConversationID(conversationID)
	h.mu.Lock()
	if rec := h.rollouts[id]; rec != nil {
		h.touchRollout(id)
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
			attribute.String("conversation.id", conversationID))
		return nil
	}
	rec, err := rollout.Open(path)
	if err != nil {
		telemetry.WarnErr(ctx, "rollout: open recorder failed", err,
			attribute.String("conversation.id", conversationID))
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
	announce := !h.rolloutOpened[id]
	h.rolloutOpened[id] = true
	h.touchRollout(id)
	h.evictRolloutsLocked(ctx)
	h.mu.Unlock()
	if announce {
		h.recordRollout(ctx, rec, rollout.Event{
			Type:           rollout.TypeThreadStarted,
			ConversationID: conversationID,
		}, "thread started")
	}
	return rec
}

// maxOpenRollouts bounds how many rollout recorders stay open on one
// Host. It is a variable so tests can exercise eviction without
// opening dozens of conversations.
var maxOpenRollouts = 16

// touchRollout moves id to the front of the open order (most recently
// used first). Caller holds h.mu.
func (h *Host) touchRollout(id ConversationID) {
	for i, cur := range h.rolloutOrder {
		if cur == id {
			h.rolloutOrder = append(h.rolloutOrder[:i], h.rolloutOrder[i+1:]...)
			break
		}
	}
	h.rolloutOrder = append([]ConversationID{id}, h.rolloutOrder...)
}

// evictRolloutsLocked closes the least recently used recorders while
// more than maxOpenRollouts remain. The recorder being opened was just
// touched to the front, so eviction never closes the caller's handle.
// Close flushes the file, and the next event for an evicted
// conversation reopens it. Caller holds h.mu.
func (h *Host) evictRolloutsLocked(ctx context.Context) {
	for len(h.rollouts) > maxOpenRollouts && len(h.rolloutOrder) > 0 {
		oldest := h.rolloutOrder[len(h.rolloutOrder)-1]
		h.rolloutOrder = h.rolloutOrder[:len(h.rolloutOrder)-1]
		rec := h.rollouts[oldest]
		delete(h.rollouts, oldest)
		telemetry.WarnErr(ctx, "rollout: close evicted recorder failed",
			rec.Close())
	}
}

// dropRolloutLocked forgets one conversation's recorder without
// closing it — the delete path closes it outside the lock, before the
// session's rows and files go away — and drops it from the open order.
// Caller holds h.mu.
func (h *Host) dropRolloutLocked(id ConversationID) *rollout.Recorder {
	rec := h.rollouts[id]
	delete(h.rollouts, id)
	delete(h.rolloutOpened, id)
	for i, cur := range h.rolloutOrder {
		if cur == id {
			h.rolloutOrder = append(h.rolloutOrder[:i], h.rolloutOrder[i+1:]...)
			break
		}
	}
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
	h.rolloutOrder = nil
}
