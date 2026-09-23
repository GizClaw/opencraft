package core

import (
	"context"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/delegation"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/runtime/session"
)

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
