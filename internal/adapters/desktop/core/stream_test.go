package core

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"
)

// sinkEvent is one recorded (type, data) pair a shell sink received.
type sinkEvent struct {
	typ  string
	data any
}

// shellSink records every event a shell delivers, and signals arrivals
// for the tests that exercise the timer path.
type shellSink struct {
	mu     sync.Mutex
	events []sinkEvent
	arrive chan struct{}
}

func (s *shellSink) on(typ string, data any) {
	s.mu.Lock()
	s.events = append(s.events, sinkEvent{typ: typ, data: data})
	s.mu.Unlock()
	if s.arrive != nil {
		select {
		case s.arrive <- struct{}{}:
		default:
		}
	}
}

func (s *shellSink) snapshot() []sinkEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sinkEvent(nil), s.events...)
}

// newStreamShell returns a shell with a recording sink and a window far
// beyond the test lifetime, so only explicit flushes or the size caps
// ship anything.
func newStreamShell(t *testing.T) (*Shell, *shellSink) {
	t.Helper()
	shell := NewShell(t.TempDir())
	shell.stream.window = time.Hour
	sink := &shellSink{}
	shell.SetPetSink(sink.on)
	return shell, sink
}

func textEvent(run, text string) StreamEvent {
	return StreamEvent{
		RunID:          run,
		ConversationID: "conv-1",
		AgentID:        AssistantAgentID,
		Delta: agent.StreamDeltaPayload{
			Type: agent.StreamDeltaPart,
			Part: message.TextPart{Text: text},
		},
	}
}

func reasoningEvent(run, text string) StreamEvent {
	return StreamEvent{
		RunID:          run,
		ConversationID: "conv-1",
		AgentID:        AssistantAgentID,
		Delta: agent.StreamDeltaPayload{
			Type: agent.StreamDeltaPart,
			Part: message.ReasoningPart{Text: text},
		},
	}
}

func toolCallEvent(run string) StreamEvent {
	return StreamEvent{
		RunID:          run,
		ConversationID: "conv-1",
		AgentID:        AssistantAgentID,
		Delta: agent.StreamDeltaPayload{
			Type: agent.StreamDeltaPart,
			Part: message.ToolCallPart{Call: message.ToolCall{Name: "web_fetch"}},
		},
	}
}

func finishEvent(run string) StreamEvent {
	return StreamEvent{
		RunID:          run,
		ConversationID: "conv-1",
		AgentID:        AssistantAgentID,
		Delta: agent.StreamDeltaPayload{
			Type:         agent.StreamDeltaFinish,
			FinishReason: "stop",
		},
	}
}

// eventDelta unwraps one recorded stream event.
func eventDelta(t *testing.T, ev sinkEvent) agent.StreamDeltaPayload {
	t.Helper()
	if ev.typ != "stream" {
		t.Fatalf("event type = %q, want stream", ev.typ)
	}
	data, ok := ev.data.(map[string]any)
	if !ok {
		t.Fatalf("stream event data is %T, want map", ev.data)
	}
	delta, ok := data["delta"].(agent.StreamDeltaPayload)
	if !ok {
		t.Fatalf("stream event delta is %T, want StreamDeltaPayload", data["delta"])
	}
	return delta
}

func eventText(t *testing.T, ev sinkEvent) string {
	t.Helper()
	delta := eventDelta(t, ev)
	switch part := delta.Part.(type) {
	case message.TextPart:
		return part.Text
	case message.ReasoningPart:
		return part.Text
	}
	t.Fatalf("delta part is %T, want text or reasoning", eventDelta(t, ev).Part)
	return ""
}

func TestEmitStreamCoalescesOneWindowIntoOneEvent(t *testing.T) {
	shell, sink := newStreamShell(t)

	shell.EmitStream(textEvent("run-1", "Hel"))
	shell.EmitStream(textEvent("run-1", "lo "))
	shell.EmitStream(textEvent("run-1", "world"))
	if got := sink.snapshot(); len(got) != 0 {
		t.Fatalf("buffered deltas shipped before the window closed: %d events", len(got))
	}

	shell.flushStreams()
	got := sink.snapshot()
	if len(got) != 1 {
		t.Fatalf("events = %d, want 1 merged event", len(got))
	}
	if text := eventText(t, got[0]); text != "Hello world" {
		t.Fatalf("merged text = %q, want %q", text, "Hello world")
	}
	data := got[0].data.(map[string]any)
	if data["run_id"] != "run-1" || data["conversation_id"] != "conv-1" {
		t.Fatalf("merged event lost its identity: %+v", data)
	}
}

func TestEmitStreamMergesReasoningSeparatelyFromText(t *testing.T) {
	shell, sink := newStreamShell(t)

	shell.EmitStream(reasoningEvent("run-1", "think "))
	shell.EmitStream(reasoningEvent("run-1", "hard"))
	shell.EmitStream(textEvent("run-1", "answer"))
	shell.flushStreams()

	got := sink.snapshot()
	if len(got) != 2 {
		t.Fatalf("events = %d, want reasoning and text", len(got))
	}
	if text := eventText(t, got[0]); text != "think hard" {
		t.Fatalf("reasoning text = %q", text)
	}
	if text := eventText(t, got[1]); text != "answer" {
		t.Fatalf("text = %q", text)
	}
	if _, ok := eventDelta(t, got[0]).Part.(message.ReasoningPart); !ok {
		t.Fatalf("reasoning event part = %T", eventDelta(t, got[0]).Part)
	}
}

func TestEmitStreamBoundariesFlushFirstAndShipInPlace(t *testing.T) {
	shell, sink := newStreamShell(t)

	shell.EmitStream(textEvent("run-1", "before tool"))
	shell.EmitStream(toolCallEvent("run-1"))
	shell.EmitStream(textEvent("run-1", "after tool"))
	shell.EmitStream(finishEvent("run-1"))
	shell.flushStreams()

	got := sink.snapshot()
	if len(got) != 4 {
		t.Fatalf("events = %d, want text, tool call, text, finish", len(got))
	}
	if text := eventText(t, got[0]); text != "before tool" {
		t.Fatalf("first event text = %q", text)
	}
	if _, ok := eventDelta(t, got[1]).Part.(message.ToolCallPart); !ok {
		t.Fatalf("second event part = %T, want tool call", eventDelta(t, got[1]).Part)
	}
	if text := eventText(t, got[2]); text != "after tool" {
		t.Fatalf("third event text = %q", text)
	}
	if finish := eventDelta(t, got[3]); finish.FinishReason != "stop" {
		t.Fatalf("finish reason = %q, want stop", finish.FinishReason)
	}
}

func TestEmitStreamEmptyTextIsAnOrderingBoundary(t *testing.T) {
	shell, sink := newStreamShell(t)

	shell.EmitStream(textEvent("run-1", "x"))
	shell.EmitStream(textEvent("run-1", ""))
	shell.EmitStream(textEvent("run-1", "y"))
	shell.flushStreams()

	got := sink.snapshot()
	if len(got) != 3 {
		t.Fatalf("events = %d, want three", len(got))
	}
	for i, want := range []string{"x", "", "y"} {
		if text := eventText(t, got[i]); text != want {
			t.Fatalf("event %d text = %q, want %q", i, text, want)
		}
	}
}

func TestEmitStreamInterleavedStreamsKeepArrivalOrder(t *testing.T) {
	shell, sink := newStreamShell(t)

	shell.EmitStream(textEvent("run-a", "1"))
	shell.EmitStream(textEvent("run-b", "2"))
	shell.EmitStream(textEvent("run-a", "3"))
	shell.flushStreams()

	got := sink.snapshot()
	if len(got) != 3 {
		t.Fatalf("events = %d, want one per arrival (3)", len(got))
	}
	for i, want := range []string{"1", "2", "3"} {
		if text := eventText(t, got[i]); text != want {
			t.Fatalf("event %d text = %q, want %q", i, text, want)
		}
	}
	if run := got[2].data.(map[string]any)["run_id"]; run != "run-a" {
		t.Fatalf("last event run = %v, want run-a", run)
	}
}

func TestEmitBarrierShipsTurnEndAfterBufferedDeltas(t *testing.T) {
	shell, sink := newStreamShell(t)

	shell.EmitStream(textEvent("run-1", "final words"))
	shell.Emit("turn_end", TurnEndEvent{RunID: "run-1", Status: "completed"})

	got := sink.snapshot()
	if len(got) != 2 {
		t.Fatalf("events = %d, want stream then turn_end", len(got))
	}
	if got[0].typ != "stream" || got[1].typ != "turn_end" {
		t.Fatalf("order = %q, %q; want stream then turn_end", got[0].typ, got[1].typ)
	}
	if ev, ok := got[1].data.(TurnEndEvent); !ok || ev.RunID != "run-1" {
		t.Fatalf("turn_end payload = %#v", got[1].data)
	}
}

func TestEmitStreamSizeCapsFlushWithoutTimer(t *testing.T) {
	t.Run("bytes", func(t *testing.T) {
		shell, sink := newStreamShell(t)
		blob := strings.Repeat("a", streamMaxBytes/2+1)

		shell.EmitStream(textEvent("run-1", blob))
		if got := sink.snapshot(); len(got) != 0 {
			t.Fatalf("one delta below the cap flushed early: %d events", len(got))
		}
		shell.EmitStream(textEvent("run-1", blob))

		got := sink.snapshot()
		if len(got) != 1 {
			t.Fatalf("events = %d, want the byte cap to flush once", len(got))
		}
		if text := eventText(t, got[0]); len(text) != 2*len(blob) {
			t.Fatalf("merged text length = %d, want %d", len(text), 2*len(blob))
		}
	})

	t.Run("deltas", func(t *testing.T) {
		shell, sink := newStreamShell(t)

		for i := 0; i < streamMaxDeltas; i++ {
			shell.EmitStream(textEvent("run-1", "t"))
		}
		got := sink.snapshot()
		if len(got) != 1 {
			t.Fatalf("events = %d, want the delta cap to flush once", len(got))
		}
		if text := eventText(t, got[0]); len(text) != streamMaxDeltas {
			t.Fatalf("merged text length = %d, want %d", len(text), streamMaxDeltas)
		}
	})
}

func TestEmitStreamTimerShipsTheWindow(t *testing.T) {
	shell := NewShell(t.TempDir())
	shell.stream.window = 5 * time.Millisecond
	sink := &shellSink{arrive: make(chan struct{}, 4)}
	shell.SetPetSink(sink.on)

	shell.EmitStream(textEvent("run-1", "tick"))

	select {
	case <-sink.arrive:
	case <-time.After(2 * time.Second):
		t.Fatal("the coalescing window never flushed")
	}
	got := sink.snapshot()
	if len(got) != 1 {
		t.Fatalf("events = %d, want 1", len(got))
	}
	if text := eventText(t, got[0]); text != "tick" {
		t.Fatalf("text = %q", text)
	}
}

func TestClearPendingStreamsDropsTheWindow(t *testing.T) {
	shell, sink := newStreamShell(t)

	shell.EmitStream(textEvent("run-1", "dropped"))
	shell.ClearPendingStreams()
	shell.flushStreams()

	if got := sink.snapshot(); len(got) != 0 {
		t.Fatalf("cleared window still shipped %d events", len(got))
	}
	// The buffer is reusable after a clear.
	shell.EmitStream(textEvent("run-1", "kept"))
	shell.flushStreams()
	got := sink.snapshot()
	if len(got) != 1 || eventText(t, got[0]) != "kept" {
		t.Fatalf("post-clear events = %#v", got)
	}
}
