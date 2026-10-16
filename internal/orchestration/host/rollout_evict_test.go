package host

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/capabilities/rollout"
	"github.com/GizClaw/opencraft/internal/foundation/ids"
	"github.com/GizClaw/opencraft/internal/testing/sessionstore"
)

// newRolloutHost builds the minimal Host surface rolloutFor touches: a
// session store plus the recorder bookkeeping maps.
func newRolloutHost(t *testing.T) *Host {
	t.Helper()
	store, err := sessionstore.Open(t, t.TempDir(), 40)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	h := &Host{store: store}
	h.rollouts = make(map[ConversationID]*rollout.Recorder)
	h.rolloutOpened = make(map[ConversationID]bool)
	h.deleting = make(map[ConversationID]bool)
	h.deleted = make(map[ConversationID]bool)
	return h
}

// TestRolloutRecordersStayBounded pins the fd discipline: a Host that
// streams many conversations keeps at most maxOpenRollouts recorders
// open, and an evicted conversation reopens on demand without losing
// its file's sequence or re-announcing its thread.
func TestRolloutRecordersStayBounded(t *testing.T) {
	old := maxOpenRollouts
	maxOpenRollouts = 3
	t.Cleanup(func() { maxOpenRollouts = old })
	h := newRolloutHost(t)
	ctx := context.Background()

	first := ids.NewSession()
	if rec := h.rolloutFor(ctx, first); rec == nil {
		t.Fatal("first recorder: nil")
	}
	for i := 0; i < maxOpenRollouts+2; i++ {
		if rec := h.rolloutFor(ctx, ids.NewSession()); rec == nil {
			t.Fatalf("recorder %d: nil", i)
		}
	}
	h.mu.Lock()
	open := len(h.rollouts)
	ordered := len(h.rolloutOrder)
	h.mu.Unlock()
	if open > maxOpenRollouts {
		t.Fatalf("open recorders = %d, want <= %d", open, maxOpenRollouts)
	}
	if ordered != open {
		t.Fatalf("open order entries = %d, want %d", ordered, open)
	}

	// The evicted conversation reopens on demand and continues the same
	// file's sequence.
	rec := h.rolloutFor(ctx, first)
	if rec == nil {
		t.Fatal("reopen of evicted conversation: nil")
	}
	h.recordRollout(ctx, rec, rollout.Event{
		Type:           rollout.TypeTurnStarted,
		ConversationID: first,
	}, "test")
	path, err := h.store.RolloutPath(first)
	if err != nil {
		t.Fatalf("rollout path: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rollout: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("rollout has %d lines, want 2:\n%s", len(lines), data)
	}
	if !strings.Contains(lines[0], `"type":"thread.started"`) ||
		!strings.Contains(lines[0], `"seq":0`) {
		t.Fatalf("first line = %s, want thread.started seq 0", lines[0])
	}
	if !strings.Contains(lines[1], `"type":"turn.started"`) ||
		!strings.Contains(lines[1], `"seq":1`) {
		t.Fatalf("second line = %s, want turn.started seq 1", lines[1])
	}

	// The delete path forgets the recorder and its order entry so a
	// later reopen cannot resurrect an id whose files are going away.
	dropped := h.dropRolloutLocked(ConversationID(first))
	if dropped == nil {
		t.Fatal("dropRolloutLocked returned nil for an open recorder")
	}
	if err := dropped.Close(); err != nil {
		t.Fatalf("close dropped recorder: %v", err)
	}
	h.mu.Lock()
	_, still := h.rollouts[ConversationID(first)]
	order := append([]ConversationID(nil), h.rolloutOrder...)
	_, announced := h.rolloutOpened[ConversationID(first)]
	h.mu.Unlock()
	if still {
		t.Fatal("delete left the recorder open")
	}
	if announced {
		t.Fatal("delete left the thread-announced flag behind")
	}
	for _, id := range order {
		if id == ConversationID(first) {
			t.Fatal("delete left the id in the open order")
		}
	}
}
