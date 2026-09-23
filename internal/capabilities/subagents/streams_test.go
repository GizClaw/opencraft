package subagents

import (
	"context"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/delegation"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/runtime/session"
)

// countingSink records how many deltas reached it, so a test can prove
// the wrapper forwards rather than swallows.
type countingSink struct {
	deltas int
}

func (s *countingSink) OnDelta(
	context.Context, event.Envelope, agent.StreamDeltaPayload,
) error {
	s.deltas++
	return nil
}

// forwardingSink is a decorator in front of a sink: it implements the
// destination capability by forwarding, which is what the host's own
// stream wrappers do.
type forwardingSink struct {
	next agent.StreamSink
}

func (s *forwardingSink) OnDelta(
	ctx context.Context, env event.Envelope, delta agent.StreamDeltaPayload,
) error {
	return s.next.OnDelta(ctx, env, delta)
}

func (s *forwardingSink) StreamTarget() (delegation.StreamTarget, bool) {
	provider, ok := s.next.(delegation.StreamTargetProvider)
	if !ok {
		return delegation.StreamTarget{}, false
	}
	return provider.StreamTarget()
}

// TestConversationSinkDescribesAndForwards pins the two halves of the
// wrapper: it names the conversation as its durable destination, and it
// stays transparent for the deltas the app already handles.
func TestConversationSinkDescribesAndForwards(t *testing.T) {
	inner := &countingSink{}
	sink := Wrap("s-1", inner)
	target, ok := sink.(delegation.StreamTargetProvider)
	if !ok {
		t.Fatalf("wrapped sink %T cannot describe a target", sink)
	}
	got, ok := target.StreamTarget()
	if !ok || got.Kind != delegation.StreamTargetKindConversation ||
		got.ID != "s-1" {
		t.Fatalf("target = %+v ok=%v", got, ok)
	}
	if err := sink.OnDelta(context.Background(), event.Envelope{}, agent.StreamDeltaPayload{}); err != nil {
		t.Fatalf("OnDelta: %v", err)
	}
	if inner.deltas != 1 {
		t.Fatalf("inner delta count = %d, want the delta forwarded", inner.deltas)
	}
	// Wrapping twice must not stack layers, and a sink with no
	// conversation has no durable destination to describe.
	if again := Wrap("s-1", sink); again != sink {
		t.Fatalf("wrapping a wrapped sink stacked another layer: %T", again)
	}
	if got := Wrap("  ", inner); got != agent.StreamSink(inner) {
		t.Fatalf("blank conversation id wrapped the sink")
	}
}

// TestExporterRecognizesForwardingDecorators pins that recognition is by
// capability: a decorator that forwards the description must not hide
// the destination from the exporter.
func TestExporterRecognizesForwardingDecorators(t *testing.T) {
	targets := NewStreamTargets()
	export := targets.Exporter()

	target, ok := export(session.SinkSpec{
		Sink: &ConversationSink{ConversationID: "s-7", Next: &countingSink{}},
	})
	if !ok || target.ID != "s-7" {
		t.Fatalf("target = %+v ok=%v", target, ok)
	}
	target, ok = export(session.SinkSpec{Sink: &forwardingSink{
		next: &ConversationSink{ConversationID: "s-8", Next: &countingSink{}},
	}})
	if !ok || target.ID != "s-8" {
		t.Fatalf("decorated target = %+v ok=%v", target, ok)
	}

	// A plain sink and a wrapper without a conversation both have no
	// destination to export; the caller falls back to the escrow.
	if _, ok := export(session.SinkSpec{Sink: &countingSink{}}); ok {
		t.Fatal("exporter described a sink that has no destination")
	}
	if _, ok := export(session.SinkSpec{
		Sink: &ConversationSink{Next: &countingSink{}},
	}); ok {
		t.Fatal("exporter described a wrapper without a conversation")
	}
}

// TestResolverRequiresARegisteredConversation pins the import half: the
// target vocabulary is a whitelist and the sink has to be live, because
// the resolver runs over records a backend persisted.
func TestResolverRequiresARegisteredConversation(t *testing.T) {
	targets := NewStreamTargets()
	resolve := targets.Resolver()
	ctx := context.Background()

	if _, err := resolve(ctx, delegation.StreamTarget{
		Kind: "bus", ID: "s-1",
	}); err == nil || !strings.Contains(err.Error(), "bus") {
		t.Fatalf("resolver err = %v, want an unknown-kind refusal", err)
	}
	if _, err := resolve(ctx, delegation.StreamTarget{
		Kind: delegation.StreamTargetKindConversation,
	}); err == nil {
		t.Fatal("resolver accepted a conversation target without an id")
	}
	if _, err := resolve(ctx, delegation.StreamTarget{
		Kind: delegation.StreamTargetKindConversation, ID: "s-1",
	}); err == nil || !strings.Contains(err.Error(), "s-1") {
		t.Fatalf("resolver err = %v, want a no-live-sink refusal naming s-1", err)
	}

	release := targets.Register("s-1", &countingSink{})
	sink, err := resolve(ctx, delegation.StreamTarget{
		Kind: delegation.StreamTargetKindConversation, ID: "s-1",
	})
	if err != nil || sink == nil {
		t.Fatalf("resolve registered sink: %v", err)
	}
	release()
	if _, err := resolve(ctx, delegation.StreamTarget{
		Kind: delegation.StreamTargetKindConversation, ID: "s-1",
	}); err == nil {
		t.Fatal("resolver kept serving a released sink")
	}
}

// TestReleaseDoesNotRemoveTheNewerSink pins the turn-over-turn case: the
// next turn replaces the entry, and the previous turn's release (which
// arrives later, when its run ends) must not unregister the newcomer.
func TestReleaseDoesNotRemoveTheNewerSink(t *testing.T) {
	targets := NewStreamTargets()
	first := targets.Register("s-1", &countingSink{})
	second := targets.Register("s-1", &countingSink{})
	first()
	if !targets.Registered("s-1") {
		t.Fatal("the previous turn's release removed the new sink")
	}
	second()
	if targets.Registered("s-1") {
		t.Fatal("the last release left the conversation registered")
	}
	if _, ok := targets.Sink("s-1"); ok {
		t.Fatal("Sink reported a released conversation")
	}
}

// TestRegisterNormalizesAndWraps pins the two conveniences callers rely
// on: a plain sink is wrapped so it is resolvable, and a blank id or nil
// sink registers nothing but still returns a usable closer.
func TestRegisterNormalizesAndWraps(t *testing.T) {
	targets := NewStreamTargets()
	plain := &countingSink{}
	release := targets.Register(" s-9 ", plain)
	defer release()
	sink, ok := targets.Sink("s-9")
	if !ok {
		t.Fatal("a plain sink was registered unwrapped and unresolvable")
	}
	if _, ok := sink.(delegation.StreamTargetProvider); !ok {
		t.Fatalf("registered sink %T cannot describe a target", sink)
	}
	if targets.Registered(" ") || targets.Registered("s-missing") {
		t.Fatal("registry reported a conversation it never held")
	}
	targets.Register("", plain)()
	targets.Register("s-nil", nil)()
	if targets.Registered("s-nil") {
		t.Fatal("a nil sink was registered")
	}
}
