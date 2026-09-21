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
	// repeatDefaultThreshold means "the third call in a row of the same
	// call is refused" when the deployment leaves the number out.
	repeatDefaultThreshold = 3
	// repeatMaxSessions bounds how many runs keep counters. A session is
	// one run's view, so the bound is really "how many runs are tracked
	// at once"; the oldest entry is dropped when a new one arrives.
	repeatMaxSessions = 64
)

// RepeatSettings is the no-progress guard's configuration: a call (tool
// name plus canonical arguments) repeated back to back by the same run
// is refused on its Threshold-th consecutive occurrence, so it executes
// at most Threshold-1 times (twice by default). Counting only the tail —
// rather than repeats spread over a window — leaves ordinary work
// untouched: a read → edit → read → test interleaving is progress and
// never trips the guard, only re-issuing the identical call with nothing
// in between does. A call the guard refuses still counts as the run's
// last call, so retrying it unchanged stays refused; any different call
// resets the streak. Polling tools (waiting on a build, tailing a log)
// belong in Exempt.
type RepeatSettings struct {
	Enabled   bool     `json:"enabled"`
	Threshold int      `json:"threshold,omitempty"`
	Exempt    []string `json:"exempt,omitempty"`
}

// repeatMiddleware refuses a call that repeats the run's immediately
// preceding call again, once the streak reaches the threshold. The goal
// is not to forbid repetition but to keep a looping agent cheap and
// self-correcting: the model receives an error result that says what
// loop it is in and what to do instead, so the turn ends by decision
// rather than by burning the run timeout on the same no-op call.
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
	threshold := s.Threshold
	if threshold == 0 {
		threshold = repeatDefaultThreshold
	}
	if threshold < 2 {
		return nil, errdefs.Validationf(
			"tool middleware: repeat.threshold must be at least 2, got %d",
			s.Threshold)
	}
	tracker := &repeatTracker{
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
	threshold int
	exempt    map[string]bool

	mu       sync.Mutex
	sessions map[tool.Session]*repeatSession
	order    []tool.Session
}

// repeatSession is one run's tail: the canonical key of the call the run
// issued last and how many times in a row that key has been issued. Any
// different call replaces the key and resets the streak to one.
type repeatSession struct {
	lastKey string
	streak  int
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
				otellog.Int("tool.repeat_count", seen))
			return message.NewErrorToolResult(call.ID, fmt.Sprintf(
				"%s was not executed: this exact call, with identically "+
					"canonicalized arguments, has now been attempted %d times "+
					"in a row. Repeating it again cannot produce a different "+
					"result. Change the arguments or the approach, use another "+
					"tool, or state what is blocking you so the user can decide.",
				call.Name, seen))
		}
	}
}

// observe records one attempt and reports whether the guard refuses it.
// Refused attempts are recorded too, so a model that keeps emitting the
// same call keeps seeing the same answer instead of resetting its own
// streak by retrying.
func (t *repeatTracker) observe(session tool.Session, call message.ToolCall) (bool, int) {
	key := repeatCallKey(call)
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.sessions[session]
	if st == nil {
		st = &repeatSession{}
		t.sessions[session] = st
		t.order = append(t.order, session)
		if len(t.order) > repeatMaxSessions {
			delete(t.sessions, t.order[0])
			t.order = t.order[1:]
		}
	}
	if key == st.lastKey {
		st.streak++
	} else {
		st.lastKey = key
		st.streak = 1
	}
	return st.streak >= t.threshold, st.streak
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
