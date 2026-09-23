// Package subagents carries the app-owned halves of delegation: where
// a delegated run's stream is delivered, and how a finished async
// delegation is routed back into the conversation that asked for it.
//
// The vocabulary (target kinds, the exporter/resolver pair) belongs to
// flowcraft's delegation contract; what lives here is the opencraft
// implementation of that contract over the desktop's conversation
// sinks.
package subagents

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/delegation"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/runtime/session"
)

// StreamTargets is the registry of live conversation sinks that makes
// a delegated run's stream destination durable.
//
// A sync delegation inherits the caller's sinks directly; an async one
// crosses a queue, so the caller's runtime describes its sink as a
// serializable target (exporter) and the worker's runtime turns that
// description back into a live sink (resolver). Without a registry the
// worker can only reach sinks that are still held by the in-process
// escrow: once the escrow is released or its TTL expires, the stream
// has nowhere to go.
//
// Membership is the ownership proof the resolver needs. Entries exist
// only while a conversation of a workspace this app serves has a live
// turn — a target persisted by a backend can therefore never construct
// a sink the app did not already have.
type StreamTargets struct {
	mu    sync.RWMutex
	sinks map[string]agent.StreamSink
}

// ConversationSink is a live sink that can describe its durable
// destination: the conversation it belongs to. It is what the exporter
// recognizes, and what the desktop wraps its per-turn sink in.
type ConversationSink struct {
	ConversationID string
	Next           agent.StreamSink
}

// OnDelta implements agent.StreamSink.
func (s *ConversationSink) OnDelta(
	ctx context.Context, env event.Envelope, delta agent.StreamDeltaPayload,
) error {
	if s == nil || s.Next == nil {
		return nil
	}
	return s.Next.OnDelta(ctx, env, delta)
}

// StreamTarget implements delegation.StreamTargetProvider. The kind is
// spelled here rather than derived from the sink: a decorator that
// forwards this call must forward the same destination, and the
// resolver whitelists exactly this vocabulary.
func (s *ConversationSink) StreamTarget() (delegation.StreamTarget, bool) {
	if s == nil || strings.TrimSpace(s.ConversationID) == "" {
		return delegation.StreamTarget{}, false
	}
	return delegation.StreamTarget{
		Kind: delegation.StreamTargetKindConversation,
		ID:   s.ConversationID,
	}, true
}

var (
	_ agent.StreamSink                = (*ConversationSink)(nil)
	_ delegation.StreamTargetProvider = (*ConversationSink)(nil)
)

// NewStreamTargets returns an empty registry.
func NewStreamTargets() *StreamTargets {
	return &StreamTargets{sinks: make(map[string]agent.StreamSink)}
}

// Wrap attaches a durable destination to a live sink. The returned
// sink forwards every delta unchanged; only the description is added,
// so wrapping is safe in front of any sink the app already has.
func Wrap(conversationID string, next agent.StreamSink) agent.StreamSink {
	if strings.TrimSpace(conversationID) == "" || next == nil {
		return next
	}
	if _, ok := next.(*ConversationSink); ok {
		return next
	}
	return &ConversationSink{ConversationID: conversationID, Next: next}
}

// Register makes a conversation's live sink resolvable, returning the
// function that removes it again. Registering the same conversation
// twice replaces the entry: the newest turn owns the sink, and the
// previous run's unregister must not remove the new entry — which is
// what the returned closer checks for.
func (t *StreamTargets) Register(
	conversationID string, sink agent.StreamSink,
) func() {
	id := strings.TrimSpace(conversationID)
	if id == "" || sink == nil {
		return func() {}
	}
	if _, ok := sink.(*ConversationSink); !ok {
		sink = Wrap(id, sink)
	}
	t.mu.Lock()
	t.sinks[id] = sink
	t.mu.Unlock()
	return func() {
		t.mu.Lock()
		if current, ok := t.sinks[id]; ok && current == sink {
			delete(t.sinks, id)
		}
		t.mu.Unlock()
	}
}

// Registered reports whether a conversation currently holds a live
// sink. It is the resolver's ownership check.
func (t *StreamTargets) Registered(conversationID string) bool {
	if t == nil {
		return false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.sinks[strings.TrimSpace(conversationID)]
	return ok
}

// Sink returns the conversation's live sink, if any.
func (t *StreamTargets) Sink(conversationID string) (agent.StreamSink, bool) {
	if t == nil {
		return nil, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	sink, ok := t.sinks[strings.TrimSpace(conversationID)]
	return sink, ok
}

// Exporter describes an inherited sink as a serializable target at
// async submit time. Recognition is by capability
// ([delegation.StreamTargetProvider]), not by concrete type, so a
// decorator that forwards the description is recognized too.
func (t *StreamTargets) Exporter() delegation.StreamTargetExporter {
	return func(spec session.SinkSpec) (delegation.StreamTarget, bool) {
		provider, ok := spec.Sink.(delegation.StreamTargetProvider)
		if !ok {
			return delegation.StreamTarget{}, false
		}
		target, ok := provider.StreamTarget()
		if !ok {
			return delegation.StreamTarget{}, false
		}
		return target, true
	}
}

// Resolver materializes a persisted target back into the live sink of
// the conversation it names.
//
// Only the conversation kind is accepted, and only for a conversation
// that currently holds a live sink: the resolver runs over records a
// backend persisted, so anything wider would let that data name an
// arbitrary destination.
func (t *StreamTargets) Resolver() delegation.StreamTargetResolver {
	return func(
		_ context.Context, target delegation.StreamTarget,
	) (agent.StreamSink, error) {
		switch target.Kind {
		case delegation.StreamTargetKindConversation:
		default:
			return nil, fmt.Errorf(
				"subagents: unknown stream target kind %q", target.Kind)
		}
		id := strings.TrimSpace(target.ID)
		if id == "" {
			return nil, fmt.Errorf(
				"subagents: conversation stream target without an id")
		}
		sink, ok := t.Sink(id)
		if !ok {
			return nil, fmt.Errorf(
				"subagents: conversation %s has no live stream sink", id)
		}
		return sink, nil
	}
}
