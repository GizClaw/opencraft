package host

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/delegation"
	"github.com/GizClaw/flowcraft/core/delegation/kanban"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions/state"
	"github.com/GizClaw/opencraft/internal/capabilities/subagents"
	"github.com/GizClaw/opencraft/internal/foundation/ids"
	"github.com/GizClaw/opencraft/internal/testing/sessionstore"
)

// notificationLog records the session-updated signals a Host emits. The
// reflow watcher runs on its own goroutine, so the log is locked.
type notificationLog struct {
	mu     sync.Mutex
	seen   []string
	notify int
}

func (l *notificationLog) record(_ context.Context, conversationID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seen = append(l.seen, conversationID)
	l.notify++
}

func (l *notificationLog) conversations() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.seen...)
}

// newReflowHost returns a Host wired to one fresh workspace store, plus
// the notifications it emitted.
func newReflowHost(t *testing.T) (*Host, *notificationLog) {
	t.Helper()
	store, err := sessionstore.Open(t, t.TempDir(), 40)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	notified := &notificationLog{}
	h := &Host{store: store}
	h.sessionUpd = notified.record
	return h, notified
}

func reflowResult(conversationID string) subagents.Result {
	return subagents.Result{
		CardID:         "card-1",
		Target:         "researcher",
		RunID:          "run-child",
		Status:         delegation.StatusSucceeded,
		Output:         "the report",
		ParentRunID:    "run-parent",
		ConversationID: conversationID,
		At:             time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC),
	}
}

// waitForTurns waits until the conversation holds want archive turns,
// so the watcher goroutine's write is observed without polling state
// from the test's own goroutine.
func waitForTurns(t *testing.T, h *Host, conversationID string, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		turns, err := h.SessionsStore().Turns(context.Background(), conversationID)
		if err == nil && len(turns) >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("conversation %s never reached %d turns (err %v)",
				conversationID, want, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestReflowDelegationAppendsNoteTurn pins the delivery itself: the
// note lands as a finished turn of the conversation the delegation was
// bound to, carrying the card as its run id so the reflow is
// addressable, and the UI is told to reload that conversation.
func TestReflowDelegationAppendsNoteTurn(t *testing.T) {
	h, notified := newReflowHost(t)
	ctx := context.Background()
	conversationID, err := h.SessionsStore().Create()
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if err := h.SessionsStore().AppendTurn(ctx, conversationID, []message.Message{
		message.NewTextMessage(message.RoleUser, "ask the researcher"),
	}); err != nil {
		t.Fatalf("append turn: %v", err)
	}

	result := reflowResult(conversationID)
	h.reflowDelegation(ctx, result)

	turns, err := h.SessionsStore().Turns(ctx, conversationID)
	if err != nil {
		t.Fatalf("turns: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("turns = %d, want the note appended after the original turn", len(turns))
	}
	note := turns[1]
	if note.RunID != result.Key() {
		t.Fatalf("note run id = %q, want the card key %q", note.RunID, result.Key())
	}
	if len(note.Messages) != 1 ||
		note.Messages[0].Role != message.RoleUser ||
		!strings.Contains(note.Messages[0].Content.Text(), "researcher") {
		t.Fatalf("note = %+v", note.Messages)
	}
	// A note is a finished turn: recovery and the UI both read the
	// status, and a running one would look like a turn that never
	// settled.
	if note.Status != string(agent.StatusCompleted) {
		t.Fatalf("note status = %q, want completed", note.Status)
	}
	// The author and the fields are archived with the text: the
	// transcript renders the card from this payload, and title
	// derivation goes by the kind instead of reading the prose.
	if note.Kind != subagents.KindDelegationNote {
		t.Fatalf("note kind = %q, want %q", note.Kind, subagents.KindDelegationNote)
	}
	var payload subagents.NotePayload
	if err := json.Unmarshal(note.Payload, &payload); err != nil {
		t.Fatalf("decode note payload %s: %v", note.Payload, err)
	}
	if payload != result.Payload() {
		t.Fatalf("note payload = %+v, want %+v", payload, result.Payload())
	}
	if got := notified.conversations(); len(got) != 1 || got[0] != conversationID {
		t.Fatalf("notifications = %v, want the one conversation", got)
	}
}

// TestReflowDelegationIsIdempotent pins the dedupe: a redelivered
// terminal event (a restart re-reading the board, a backend publishing
// the transition twice) must not append the note twice.
func TestReflowDelegationIsIdempotent(t *testing.T) {
	h, notified := newReflowHost(t)
	ctx := context.Background()
	conversationID, err := h.SessionsStore().Create()
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	result := reflowResult(conversationID)
	h.reflowDelegation(ctx, result)
	h.reflowDelegation(ctx, result)

	turns, err := h.SessionsStore().Turns(ctx, conversationID)
	if err != nil {
		t.Fatalf("turns: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("turns = %d, want the replay deduped to one note", len(turns))
	}
	if got := notified.conversations(); len(got) != 1 {
		t.Fatalf("notifications = %v, want no second reload", got)
	}
}

// TestReflowDelegationRefusesForeignConversations pins the archive
// guard: a stream target names a conversation, it does not create one.
func TestReflowDelegationRefusesForeignConversations(t *testing.T) {
	h, notified := newReflowHost(t)
	ctx := context.Background()

	h.reflowDelegation(ctx, reflowResult("s-not-ours"))
	if got := notified.conversations(); len(got) != 0 {
		t.Fatalf("notifications = %v, want the unknown conversation dropped", got)
	}
	if h.SessionsStore().Exists("s-not-ours") {
		t.Fatal("the reflow created a conversation for a target it does not own")
	}
}

// TestReflowDelegationSkipsEphemeralConversations pins the second
// guard: delegated runs execute under "ctx-" conversations whose
// contexts are never archived, so a note addressed there would create
// an archive row for a conversation the app hides.
func TestReflowDelegationSkipsEphemeralConversations(t *testing.T) {
	h, notified := newReflowHost(t)
	ctx := context.Background()
	ephemeral := ids.ContextPrefix + "run-child"
	if err := h.SessionsStore().State().EnsureConversation(ctx,
		state.Conversation{ID: ephemeral, Title: "delegated run"}); err != nil {
		t.Fatalf("ensure ephemeral conversation: %v", err)
	}

	h.reflowDelegation(ctx, reflowResult(ephemeral))
	if got := notified.conversations(); len(got) != 0 {
		t.Fatalf("notifications = %v, want the ephemeral conversation skipped", got)
	}
	// The store refuses to read turns for a "ctx-" id, so the archive
	// itself is the witness: a note there would be a row.
	var rows int
	if err := h.SessionsStore().Database().SQLDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM archive_turns WHERE conversation_id = ?`,
		ephemeral,
	).Scan(&rows); err != nil {
		t.Fatalf("count archive turns: %v", err)
	}
	if rows != 0 {
		t.Fatalf("ephemeral conversation holds %d archived turns", rows)
	}
}

// TestReflowDelegationSurvivesWithoutStore pins the degraded assembly:
// a runtime without a session store has no archive to write to, and
// the watcher must not panic on the way out.
func TestReflowDelegationSurvivesWithoutStore(t *testing.T) {
	h := &Host{}
	h.reflowDelegation(context.Background(), reflowResult("s-1"))
}

// TestReflowLoopRoutesTerminalBoardEvents drives the watcher end to
// end: board events arrive through the loop, and only the terminal
// conversation-bound one reaches the archive.
func TestReflowLoopRoutesTerminalBoardEvents(t *testing.T) {
	h, notified := newReflowHost(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conversationID, err := h.SessionsStore().Create()
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	events := make(chan event.Envelope, 4)
	request := &delegation.AsyncRequest{
		Request: delegation.Request{
			Mode: delegation.ModeAsync, Target: "researcher", Input: "dig",
		},
		Stream: &delegation.StreamRef{Target: &delegation.StreamTarget{
			Kind: delegation.StreamTargetKindConversation, ID: conversationID,
		}},
	}
	for _, card := range []kanban.CardEvent{
		{
			CardID: "card-2", Status: kanban.StatusClaimed,
			Consumer: "researcher", Request: request,
		},
		{
			CardID: "card-2", Status: kanban.StatusDone,
			Consumer: "researcher", RunID: "run-child", Request: request,
			Response: &delegation.Response{
				ID: "card-2", Status: delegation.StatusSucceeded, Output: "done",
			},
		},
	} {
		env, err := event.NewEnvelope(ctx, "delegation.kanban.card", card)
		if err != nil {
			t.Fatalf("envelope: %v", err)
		}
		events <- env
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.reflowLoop(ctx, events)
	}()
	waitForTurns(t, h, conversationID, 1)
	cancel()
	<-done

	turns, err := h.SessionsStore().Turns(context.Background(), conversationID)
	if err != nil {
		t.Fatalf("turns: %v", err)
	}
	if len(turns) != 1 || turns[0].RunID != "subagent:card-2" {
		t.Fatalf("turns = %+v, want the terminal event's note", turns)
	}
	if turns[0].Status != string(agent.StatusCompleted) {
		t.Fatalf("note status = %q", turns[0].Status)
	}
	if got := notified.conversations(); len(got) != 1 || got[0] != conversationID {
		t.Fatalf("notifications = %v", got)
	}
}
