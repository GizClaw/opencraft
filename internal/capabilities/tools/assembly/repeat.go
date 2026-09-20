package assembly

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"github.com/GizClaw/flowcraft/core/tool"
	otellog "go.opentelemetry.io/otel/log"
)

const (
	// repeatDefaultWindow / repeatDefaultThreshold mean "the same call
	// three times inside the last six tool calls of a run" when the
	// deployment leaves the numbers out.
	repeatDefaultWindow    = 6
	repeatDefaultThreshold = 3
	// repeatMaxSessions bounds how many runs keep counters. A session is
	// one run's view, so the bound is really "how many runs are tracked
	// at once"; the oldest entry is dropped when a new one arrives.
	repeatMaxSessions = 64
	// repeatSweepAt bounds one run's key map. Past this many distinct
	// calls the keys that slid out of the window are swept, so a long
	// run with hundreds of one-off calls cannot grow the map without
	// bound.
	repeatSweepAt = 256
)

// RepeatSettings is the no-progress guard's configuration: one call
// (tool name plus canonical arguments) may appear Threshold times inside
// a sliding window of Window tool calls of the same run; the call that
// would exceed it is refused with an actionable error instead of being
// executed again. A call the guard refuses still counts towards the
// window, so retrying it unchanged stays refused. Polling tools (waiting
// on a build, tailing a log) belong in Exempt.
type RepeatSettings struct {
	Enabled   bool     `json:"enabled"`
	Window    int      `json:"window,omitempty"`
	Threshold int      `json:"threshold,omitempty"`
	Exempt    []string `json:"exempt,omitempty"`
}

// repeatMiddleware refuses a call that repeats identically too often in
// the recent call window. The goal is not to forbid repetition but to
// keep a looping agent cheap and self-correcting: the model receives an
// error result that says what loop it is in and what to do instead, so
// the turn ends by decision rather than by burning the run timeout on
// the same no-op call.
//
// Counters are per tool session. One session is one run's tool view
// (flowcraft attaches it to the run context), so state never leaks
// across runs or conversations and is dropped once the run stops. Calls
// outside a session are not tracked at all: without a run there is
// nothing to scope them to, and a process-wide counter would block
// unrelated conversations against each other.
func repeatMiddleware(s *RepeatSettings) (tool.Middleware, error) {
	if s == nil || !s.Enabled {
		return nil, nil
	}
	window := s.Window
	if window == 0 {
		window = repeatDefaultWindow
	}
	threshold := s.Threshold
	if threshold == 0 {
		threshold = repeatDefaultThreshold
	}
	if window < 1 {
		return nil, errdefs.Validationf(
			"tool middleware: repeat.window must be positive, got %d", s.Window)
	}
	if threshold < 2 {
		return nil, errdefs.Validationf(
			"tool middleware: repeat.threshold must be at least 2, got %d",
			s.Threshold)
	}
	tracker := &repeatTracker{
		window:    window,
		threshold: threshold,
		exempt:    make(map[string]bool, len(s.Exempt)),
		sessions:  make(map[tool.Session]*repeatSession),
	}
	for _, name := range s.Exempt {
		if name != "" {
			tracker.exempt[name] = true
		}
	}
	return tracker.middleware(), nil
}

type repeatTracker struct {
	window    int
	threshold int
	exempt    map[string]bool

	mu       sync.Mutex
	sessions map[tool.Session]*repeatSession
	order    []tool.Session
}

// repeatSession is one run's sliding window: seq counts every tracked
// call (not only repeats) so the window slides with the agent's work,
// and calls maps a canonical call key to the sequence numbers at which
// it was attempted.
type repeatSession struct {
	seq   int
	calls map[string][]int
}

func (t *repeatTracker) middleware() tool.Middleware {
	return func(next tool.Dispatch) tool.Dispatch {
		return func(ctx context.Context, call message.ToolCall) message.ToolResult {
			session, ok := tool.SessionFromContext(ctx)
			if !ok || t.exempt[call.Name] {
				return next(ctx, call)
			}
			blocked, seen := t.observe(session, call)
			if !blocked {
				return next(ctx, call)
			}
			telemetry.Warn(ctx, "tool assembly: repeated call blocked",
				otellog.String("tool.name", call.Name),
				otellog.Int("tool.repeat_count", seen),
				otellog.Int("tool.repeat_window", t.window))
			return message.NewErrorToolResult(call.ID, fmt.Sprintf(
				"%s was not executed: this exact call, with identically "+
					"canonicalized arguments, has now been attempted %d times "+
					"within the last %d tool calls. Repeating it again cannot "+
					"produce a different result. Change the arguments or the "+
					"approach, use another tool, or state what is blocking you "+
					"so the user can decide.",
				call.Name, seen, t.window))
		}
	}
}

// observe records one attempt and reports whether the guard refuses it.
// Refused attempts are recorded too, so a model that keeps emitting the
// same call keeps seeing the same answer instead of sliding the window
// past its own retries.
func (t *repeatTracker) observe(session tool.Session, call message.ToolCall) (bool, int) {
	key := repeatCallKey(call)
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.sessions[session]
	if st == nil {
		st = &repeatSession{calls: make(map[string][]int)}
		t.sessions[session] = st
		t.order = append(t.order, session)
		if len(t.order) > repeatMaxSessions {
			delete(t.sessions, t.order[0])
			t.order = t.order[1:]
		}
	}
	st.seq++
	if len(st.calls) > repeatSweepAt {
		t.sweepLocked(st)
	}
	floor := st.seq - t.window
	kept := make([]int, 0, len(st.calls[key])+1)
	for _, seq := range st.calls[key] {
		if seq > floor {
			kept = append(kept, seq)
		}
	}
	seen := len(kept) + 1
	st.calls[key] = append(kept, st.seq)
	return seen >= t.threshold, seen
}

// sweepLocked drops the keys whose attempts all slid out of the window,
// keeping the per-run map proportional to the calls that can still
// trigger. Entries are appended in increasing order, so the last one is
// the newest.
func (t *repeatTracker) sweepLocked(st *repeatSession) {
	floor := st.seq - t.window
	for key, seqs := range st.calls {
		if len(seqs) == 0 || seqs[len(seqs)-1] <= floor {
			delete(st.calls, key)
		}
	}
}

// repeatCallKey canonicalizes one call for comparison. The model may
// re-serialize the same object with different key order or whitespace
// (and drivers may re-encode it), so the raw argument bytes are decoded
// and re-encoded before hashing; undecodable arguments fall back to the
// raw bytes.
func repeatCallKey(call message.ToolCall) string {
	canonical := []byte(call.Arguments)
	if len(canonical) > 0 {
		var decoded any
		if err := json.Unmarshal(canonical, &decoded); err == nil {
			if encoded, err := json.Marshal(decoded); err == nil {
				canonical = encoded
			}
		}
	}
	sum := sha256.Sum256(canonical)
	return call.Name + ":" + hex.EncodeToString(sum[:16])
}
