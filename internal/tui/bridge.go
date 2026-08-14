package tui

import (
	"context"
	"sync"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/runtime/session"

	"github.com/GizClaw/opencraft/internal/interact"
)

// Bridge converts runtime streams and prompt protocol events into
// domain Events on a channel the UI consumes, and implements the
// interactive interact.Backend for user questions.
type Bridge struct {
	events chan Event

	mu      sync.RWMutex
	started bool
}

// NewBridge creates a bridge with the given event buffer.
func NewBridge(buffer int) *Bridge {
	if buffer <= 0 {
		buffer = 64
	}
	return &Bridge{events: make(chan Event, buffer)}
}

// Events returns the domain event channel.
func (b *Bridge) Events() <-chan Event {
	return b.events
}

// Start marks the bridge as consumed (the UI read loop is running).
func (b *Bridge) Start() {
	b.mu.Lock()
	b.started = true
	b.mu.Unlock()
}

// Sink adapts a stream delta into a domain event.
func (b *Bridge) Sink(
	_ context.Context,
	_ event.Envelope,
	delta agent.StreamDeltaPayload,
) error {
	b.events <- Event{Stream: &StreamEvent{Delta: delta}}
	return nil
}

// Ask implements interact.Backend: it queues the spec for the UI and
// blocks until the UI delivers an answer or ctx ends.
func (b *Bridge) Ask(
	ctx context.Context,
	spec interact.Spec,
) (interact.Reply, error) {
	replyCh := make(chan interact.Reply, 1)
	select {
	case b.events <- Event{Interact: &InteractEvent{
		Spec: spec, ReplyCh: replyCh,
	}}:
	case <-ctx.Done():
		return interact.Reply{}, ctx.Err()
	}
	select {
	case reply := <-replyCh:
		return reply, nil
	case <-ctx.Done():
		return interact.Reply{}, ctx.Err()
	}
}

// Resolve implements interact.Resolver: it notifies the UI that a
// pending interaction was closed externally.
func (b *Bridge) Resolve(
	_ context.Context,
	id string,
	status session.PromptStatus,
) error {
	b.events <- Event{Resolved: &ResolvedEvent{ID: id, Status: status}}
	return nil
}

// Status publishes a status event.
func (b *Bridge) Status(text string, busy bool) {
	b.events <- Event{Status: &StatusEvent{Text: text, Busy: busy}}
}

// Usage publishes an inference usage report (tokens, latency, and the
// model that produced it).
func (b *Bridge) Usage(usage inference.Usage) {
	ev := UsageEvent{
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		TotalTokens:  usage.TotalTokens,
		LatencyMs:    usage.LatencyMs,
	}
	if usage.Output.ReasoningTokens != nil {
		ev.ReasoningTokens = *usage.Output.ReasoningTokens
	}
	if usage.Input.CacheReadTokens != nil {
		ev.CacheReadTokens = *usage.Input.CacheReadTokens
	}
	if usage.Input.CacheWriteTokens != nil {
		ev.CacheWriteTokens = *usage.Input.CacheWriteTokens
	}
	id := usage.Model.ID
	if id.Provider != "" && id.Name != "" {
		ev.Model = id.Provider + "/" + id.Name
	}
	b.events <- Event{Usage: &ev}
}

var _ interact.Backend = (*Bridge)(nil)
var _ interact.Resolver = (*Bridge)(nil)
