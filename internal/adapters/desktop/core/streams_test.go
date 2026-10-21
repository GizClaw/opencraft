package core

import (
	"context"
	"sync"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/delegation"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/runtime/session"
)

// countingSink counts the deltas that reached it, so a test can tell a
// dropped delta from a forwarded one.
type countingSink struct {
	mu     sync.Mutex
	deltas int
}

func (s *countingSink) OnDelta(
	context.Context, event.Envelope, agent.StreamDeltaPayload,
) error {
	s.mu.Lock()
	s.deltas++
	s.mu.Unlock()
	return nil
}

func (s *countingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deltas
}

// TestRegisterConversationSinkReturnsDescribableSink pins the contract
// the delegation exporter relies on: registration hands back the
// wrapped sink, it is the object stored in the registry (the resolver
// returns that very object), and the exporter recognizes it.
func TestRegisterConversationSinkReturnsDescribableSink(t *testing.T) {
	c := &Core{}
	raw := agent.StreamSinkFunc(
		func(context.Context, event.Envelope, agent.StreamDeltaPayload) error {
			return nil
		})
	sink, release := c.RegisterConversationSink("ctx-1", raw)
	defer release()
	if sink == nil {
		t.Fatal("registration returned no sink")
	}
	registered, ok := c.StreamTargets().Sink("ctx-1")
	if !ok {
		t.Fatal("the conversation sink is not registered")
	}
	if registered != sink {
		t.Fatalf("registered sink = %T %p, want the returned sink %T %p",
			registered, registered, sink, sink)
	}
	target, ok := c.StreamTargets().Exporter()(session.SinkSpec{Sink: sink})
	if !ok {
		t.Fatal("the exporter does not recognize the registered sink")
	}
	if target.Kind != delegation.StreamTargetKindConversation || target.ID != "ctx-1" {
		t.Fatalf("stream target = %+v, want conversation/ctx-1", target)
	}
}

// TestRegisterConversationSinkNil pins the empty edge: a nil sink stays
// nil (nothing to stream, nothing to describe) and the release is still
// callable.
func TestRegisterConversationSinkNil(t *testing.T) {
	c := &Core{}
	sink, release := c.RegisterConversationSink("ctx-1", nil)
	release()
	if sink != nil {
		t.Fatalf("registration of a nil sink returned %v, want nil", sink)
	}
}

// TestReleaseStopsDelegatedDelivery is the regression for a subagent
// that outlives its caller's turn. The worker resolves the conversation
// destination while the turn runs and keeps the object; once the turn
// ends, its deltas must not reach the window any more. Before the sink
// was closed on release, they kept arriving minutes later, and the
// window — which only ever gets a turn_end for the run this app started
// — drew them as a new turn that could never finish.
func TestReleaseStopsDelegatedDelivery(t *testing.T) {
	c := &Core{}
	inner := &countingSink{}
	sink, release := c.RegisterConversationSink("ctx-1", inner)
	// What the delegation resolver hands the worker mid-turn.
	resolved, ok := c.StreamTargets().Sink("ctx-1")
	if !ok || resolved != sink {
		t.Fatalf("resolved sink = %T %p ok=%v, want the registered one %T %p",
			resolved, resolved, ok, sink, sink)
	}
	if err := resolved.OnDelta(context.Background(), event.Envelope{}, agent.StreamDeltaPayload{}); err != nil {
		t.Fatalf("OnDelta while the turn runs: %v", err)
	}
	if inner.count() != 1 {
		t.Fatalf("deltas before release = %d, want 1", inner.count())
	}

	release()

	if err := resolved.OnDelta(context.Background(), event.Envelope{}, agent.StreamDeltaPayload{}); err != nil {
		t.Fatalf("OnDelta after the turn ended: %v", err)
	}
	if inner.count() != 1 {
		t.Fatalf("deltas after release = %d, want the turn's sink closed",
			inner.count())
	}
	if c.StreamTargets().Registered("ctx-1") {
		t.Fatal("the released conversation is still registered")
	}
}
