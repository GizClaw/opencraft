package host

import (
	"context"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/delegation"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/runtime/session"

	"github.com/GizClaw/opencraft/internal/capabilities/subagents"
)

// TestObservedSinkStaysDescribable pins that the host's per-delta
// observation layer does not swallow a wrapped conversation sink's
// delegation description: the exporter matches by capability, so a
// decorator that stopped forwarding would silently drop cross-process
// streaming (the async submit path logs "stream exporter matched no
// sink" and persists no target).
func TestObservedSinkStaysDescribable(t *testing.T) {
	wrapped := subagents.Wrap("ctx-1", agent.StreamSinkFunc(
		func(context.Context, event.Envelope, agent.StreamDeltaPayload) error {
			return nil
		}))
	sink := (&Host{}).observeSink(wrapped)
	target, ok := subagents.NewStreamTargets().Exporter()(session.SinkSpec{Sink: sink})
	if !ok {
		t.Fatalf("exporter does not recognize the observed sink (%T)", sink)
	}
	if target.Kind != delegation.StreamTargetKindConversation || target.ID != "ctx-1" {
		t.Fatalf("stream target = %+v, want conversation/ctx-1", target)
	}
}

// TestObservedSinkEdges pins the two boundaries: a nil sink stays nil,
// a plain closure still forwards deltas but describes no target.
func TestObservedSinkEdges(t *testing.T) {
	if got := (&Host{}).observeSink(nil); got != nil {
		t.Fatalf("observeSink(nil) = %v, want nil", got)
	}
	var forwarded int
	plain := (&Host{}).observeSink(agent.StreamSinkFunc(
		func(context.Context, event.Envelope, agent.StreamDeltaPayload) error {
			forwarded++
			return nil
		}))
	if err := plain.OnDelta(
		context.Background(), event.Envelope{}, agent.StreamDeltaPayload{},
	); err != nil {
		t.Fatalf("OnDelta: %v", err)
	}
	if forwarded != 1 {
		t.Fatalf("forwarded deltas = %d, want 1", forwarded)
	}
	if _, ok := subagents.NewStreamTargets().Exporter()(
		session.SinkSpec{Sink: plain},
	); ok {
		t.Fatal("a plain closure must not describe a stream target")
	}
}
