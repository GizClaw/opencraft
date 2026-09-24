package core

import (
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"
)

// Stream coalescing bounds what a token stream costs the desktop: the
// shell folds adjacent text and reasoning deltas of one stream into a
// single "stream" event per window instead of one event per delta. The
// window-side coalescer (frontend/src/lib/stream.ts) merges by the same
// rule, so a coalesced stream passes through it unchanged; everything
// else keeps arrival order, which the renderer relies on to fold a
// delta into the right phase of the right transcript.
const (
	// streamWindow is the coalescing window: the first buffered delta
	// starts it, everything folded in before it fires ships together
	// (one IPC event instead of one per token).
	streamWindow = 25 * time.Millisecond
	// streamMaxBytes and streamMaxDeltas bound one window so a large
	// burst does not wait for the timer: crossing either flushes
	// immediately.
	streamMaxBytes  = 64 << 10
	streamMaxDeltas = 128
	// streamHeartbeat is how often the merge counters are logged while
	// streaming. One bounded line a minute is what makes "is
	// coalescing actually happening" answerable from the log alone.
	streamHeartbeat = time.Minute
)

// StreamEvent is one stream delta on its way to the window.
type StreamEvent struct {
	RunID          string
	ConversationID string
	AgentID        string
	ParentRunID    string
	Delta          agent.StreamDeltaPayload
}

// streamKey identifies the stream a delta belongs to: only adjacent
// deltas of one stream merge, and only ever into the same stream. The
// window-side coalescer keys on conversation+run; the agent columns
// keep the shell stricter than the window, never looser, so a merged
// event stays one the window would have merged too.
func (ev StreamEvent) streamKey() string {
	return ev.ConversationID + "\x00" + ev.RunID + "\x00" +
		ev.AgentID + "\x00" + ev.ParentRunID
}

// mergeablePart reports the class and text of a delta that may be
// folded into the buffer: a "part" delta carrying a non-empty text or
// reasoning part. Everything else — tool calls and results, finish,
// provider payloads, custom payloads, empty text — is an ordering
// boundary the renderer must see in place, so it flushes the buffer and
// ships on its own.
func mergeablePart(ev StreamEvent) (message.PartKind, string, bool) {
	if ev.Delta.Type != agent.StreamDeltaPart {
		return "", "", false
	}
	switch part := ev.Delta.Part.(type) {
	case message.TextPart:
		return message.PartText, part.Text, part.Text != ""
	case *message.TextPart:
		if part == nil {
			return "", "", false
		}
		return message.PartText, part.Text, part.Text != ""
	case message.ReasoningPart:
		return message.PartReasoning, part.Text, part.Text != ""
	case *message.ReasoningPart:
		if part == nil {
			return "", "", false
		}
		return message.PartReasoning, part.Text, part.Text != ""
	}
	return "", "", false
}

// streamEntry is one buffered run of mergeable deltas: the first delta
// of the run (its identity plus the fields a merged event keeps) and
// the text accumulated so far. kind is message.PartText or
// message.PartReasoning.
type streamEntry struct {
	key  string
	kind message.PartKind
	text string
	ev   StreamEvent
}

// streamBuffer is the Shell's pending window. The lock is held across
// delivery (sinks included) so that buffering, flushing and the
// ordering barrier in Emit cannot interleave between two events: wire
// order equals arrival order. Sinks must therefore not call back into
// the shell.
type streamBuffer struct {
	mu      sync.Mutex
	pending []streamEntry
	bytes   int
	folded  int
	timer   *time.Timer

	// window overrides streamWindow. Zero means the constant; tests set
	// it to pin the timer behavior (or to disable the timer so only an
	// explicit flush ships).
	window time.Duration

	// Counters reported by the heartbeat, reset on every log line.
	deltasIn   int
	boundaries int
	eventsOut  int
	maxBytes   int
	lastLog    time.Time
}

// EmitStream delivers one stream delta to the window, coalescing
// adjacent text/reasoning deltas of the same stream. The buffer is
// deliberately short-lived: a buffered delta ships at the next
// boundary, at the next non-stream Emit, or when the window closes, so
// nothing can outlive the turn it belongs to.
func (s *Shell) EmitStream(ev StreamEvent) {
	b := &s.stream
	kind, text, mergeable := mergeablePart(ev)

	b.mu.Lock()
	defer b.mu.Unlock()

	if !mergeable {
		b.boundaries++
		s.flushStreamLocked()
		s.deliverStream(ev, "", "")
		s.heartbeatLocked()
		return
	}

	b.deltasIn++
	key := ev.streamKey()
	if n := len(b.pending); n > 0 &&
		b.pending[n-1].key == key && b.pending[n-1].kind == kind {
		b.pending[n-1].text += text
	} else {
		b.pending = append(b.pending, streamEntry{
			key: key, kind: kind, text: text, ev: ev,
		})
	}
	b.folded++
	b.bytes += len(text)
	if b.bytes > b.maxBytes {
		b.maxBytes = b.bytes
	}

	switch {
	case b.bytes >= streamMaxBytes || b.folded >= streamMaxDeltas:
		s.flushStreamLocked()
	case b.timer == nil:
		b.timer = time.AfterFunc(b.windowSize(), s.flushStreams)
	}
	s.heartbeatLocked()
}

// ClearPendingStreams drops every buffered delta without emitting. A
// window that is going away must not receive a half-flushed stream, and
// the next EmitStream starts a fresh window.
func (s *Shell) ClearPendingStreams() {
	b := &s.stream
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	b.pending = nil
	b.bytes = 0
	b.folded = 0
}

// flushStreams is the timer callback.
func (s *Shell) flushStreams() {
	b := &s.stream
	b.mu.Lock()
	defer b.mu.Unlock()
	s.flushStreamLocked()
}

// flushStreamLocked emits the buffered entries in arrival order. The
// caller holds the buffer lock.
func (s *Shell) flushStreamLocked() {
	b := &s.stream
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	if len(b.pending) == 0 {
		return
	}
	entries := b.pending
	b.pending = nil
	b.bytes = 0
	b.folded = 0
	for i := range entries {
		s.deliverStream(entries[i].ev, entries[i].kind, entries[i].text)
	}
}

// deliverStream emits one delta. kind is empty for a boundary delta,
// which ships with its part untouched; otherwise the part is rewritten
// with the merged text, exactly like the window-side coalescer does.
func (s *Shell) deliverStream(ev StreamEvent, kind message.PartKind, text string) {
	delta := ev.Delta
	switch kind {
	case message.PartText:
		delta.Part = message.TextPart{Text: text}
	case message.PartReasoning:
		delta.Part = message.ReasoningPart{Text: text}
	}
	s.stream.eventsOut++
	s.deliver(EventStream, map[string]any{
		"run_id":          ev.RunID,
		"conversation_id": ev.ConversationID,
		"agent_id":        ev.AgentID,
		"parent_run_id":   ev.ParentRunID,
		"delta":           delta,
	})
}

func (b *streamBuffer) windowSize() time.Duration {
	if b.window > 0 {
		return b.window
	}
	return streamWindow
}

// heartbeatLocked logs the merge counters at most once per
// streamHeartbeat while a stream is flowing, so the coalescing ratio is
// visible in the log without a debugger. The caller holds the buffer
// lock.
func (s *Shell) heartbeatLocked() {
	b := &s.stream
	now := time.Now()
	if b.lastLog.IsZero() {
		b.lastLog = now
		return
	}
	if now.Sub(b.lastLog) < streamHeartbeat {
		return
	}
	if b.deltasIn > 0 || b.boundaries > 0 || b.eventsOut > 0 {
		telemetry.Info(s.Context(), "stream: coalesced",
			otellog.Int("deltas", b.deltasIn),
			otellog.Int("boundaries", b.boundaries),
			otellog.Int("events", b.eventsOut),
			otellog.Int("buffered_bytes_max", b.maxBytes),
		)
	}
	b.deltasIn, b.boundaries, b.eventsOut, b.maxBytes = 0, 0, 0, 0
	b.lastLog = now
}
