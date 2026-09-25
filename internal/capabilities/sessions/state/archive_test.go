package state_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions/state"
)

// TestUpdateConversationUsageIsUpdateOnly pins the state-level half of
// the late-write rule: the usage column is written by an UPDATE, so a
// conversation that is gone stays gone, and the columns the archive owns
// are left alone.
func TestUpdateConversationUsageIsUpdateOnly(t *testing.T) {
	s := openState(t, filepath.Join(t.TempDir(), "session.db"))
	ctx := context.Background()
	const id = "s-usage"
	if err := s.EnsureConversation(ctx, state.Conversation{
		ID:    id,
		Title: "kept",
	}); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}

	if err := s.UpdateConversationUsage(ctx, id,
		[]byte(`{"total_tokens":42}`), time.Now().UTC()); err != nil {
		t.Fatalf("update usage: %v", err)
	}
	c, err := s.Conversation(ctx, id)
	if err != nil {
		t.Fatalf("conversation after usage update: %v", err)
	}
	if string(c.UsageJSON) != `{"total_tokens":42}` || c.Title != "kept" {
		t.Fatalf("conversation after usage update = %+v", c)
	}

	if err := s.DeleteConversationRows(ctx, id); err != nil {
		t.Fatalf("delete conversation: %v", err)
	}
	err = s.UpdateConversationUsage(ctx, id,
		[]byte(`{"total_tokens":1}`), time.Now().UTC())
	if !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("update after delete = %v, want ErrNotFound", err)
	}
	if _, err := s.Conversation(ctx, id); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("conversation after rejected update = %v, want ErrNotFound", err)
	}
}

func TestConversationArchiveCommitRoundTrip(t *testing.T) {
	s := openState(t, filepath.Join(t.TempDir(), "session.db"))
	ctx := context.Background()
	conv := state.Conversation{
		ID:        "s-1",
		Title:     "hello",
		CreatedAt: time.Now().UTC(),
	}
	if err := s.CommitConversationTurn(ctx, conv, state.ArchiveTurn{
		RunID:       "run-1",
		At:          time.Now().UTC(),
		RequestedAt: time.Now().UTC(),
		StartedAt:   time.Now().UTC(),
		FinishedAt:  time.Now().UTC(),
		Status:      "canceled",
		Error:       "context canceled",
	}, []state.ArchiveMessage{
		{Role: string(message.RoleUser), Content: message.Content{
			Parts: []message.Part{message.TextPart{Text: "hi"}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	// Duplicate run id is a no-op.
	if err := s.CommitConversationTurn(ctx, conv, state.ArchiveTurn{RunID: "run-1"}, []state.ArchiveMessage{
		{Role: string(message.RoleUser), Content: message.Content{
			Parts: []message.Part{message.TextPart{Text: "again"}},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	turns, err := s.ListArchiveTurns(ctx, "s-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || turns[0].RunID != "run-1" ||
		turns[0].Status != "canceled" ||
		turns[0].Error != "context canceled" {
		t.Fatalf("turns = %+v", turns)
	}
	msgs, err := s.ListArchiveMessages(ctx, "s-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Content.Text() != "hi" {
		t.Fatalf("messages = %+v", msgs)
	}
	got, err := s.Conversation(ctx, "s-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.TurnCount != 1 || got.MessageCount != 1 {
		t.Fatalf("conversation counts = %+v", got)
	}
	if err := s.SetConversationState(ctx, "s-1", "plans", []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	raw, err := s.GetConversationState(ctx, "s-1", "plans")
	if err != nil || string(raw) != `{"ok":true}` {
		t.Fatalf("state = %q, %v", raw, err)
	}
}

func TestArchiveTurnByRunReturnsOneTurnAndMessages(t *testing.T) {
	s := openState(t, filepath.Join(t.TempDir(), "session.db"))
	ctx := context.Background()
	conv := state.Conversation{ID: "s-1"}
	if err := s.CommitConversationTurn(ctx, conv, state.ArchiveTurn{
		RunID:  "run-1",
		Status: "completed",
	}, []state.ArchiveMessage{
		{Role: string(message.RoleUser), Content: message.Content{
			Parts: []message.Part{message.TextPart{Text: "one"}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitConversationTurn(ctx, conv, state.ArchiveTurn{
		RunID:  "run-2",
		Status: "failed",
		Error:  "boom",
	}, []state.ArchiveMessage{
		{Role: string(message.RoleUser), Content: message.Content{
			Parts: []message.Part{message.TextPart{Text: "two"}},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	turn, msgs, err := s.ArchiveTurnByRun(ctx, "s-1", "run-2")
	if err != nil {
		t.Fatalf("ArchiveTurnByRun: %v", err)
	}
	if turn.RunID != "run-2" || turn.Status != "failed" ||
		turn.Error != "boom" {
		t.Fatalf("turn = %+v", turn)
	}
	if len(msgs) != 1 || msgs[0].Content.Text() != "two" {
		t.Fatalf("messages = %+v", msgs)
	}

	if _, _, err := s.ArchiveTurnByRun(ctx, "s-1", "missing"); err != state.ErrNotFound {
		t.Fatalf("missing run error = %v, want ErrNotFound", err)
	}
	if _, _, err := s.ArchiveTurnByRun(ctx, "s-1", ""); err == nil {
		t.Fatal("ArchiveTurnByRun accepted an empty run id")
	}
}

// TestConversationDeleteRemovesAllRows pins what a conversation delete
// takes with it: the transcript (turns, messages, their search-index
// rows), the per-conversation state documents, and the summary tree
// derived from those rows — while the conversations beside it keep
// everything they own. The second physical copy of the history is gone
// (migration 020), so there is no third table to keep in step.
func TestConversationDeleteRemovesAllRows(t *testing.T) {
	s := openState(t, filepath.Join(t.TempDir(), "session.db"))
	ctx := context.Background()
	at := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	for _, conv := range []struct{ id, word string }{
		{"s-1", "alpha"},
		{"s-2", "beta"},
	} {
		commitTurn(t, s, conv.id, "title-"+conv.id, "run-"+conv.id, at,
			userText(conv.word+" hello"))
		if err := s.SetConversationState(ctx, conv.id, "plans", []byte("{}")); err != nil {
			t.Fatal(err)
		}
		insertSummaryNode(t, s, "node-"+conv.id, conv.id, at)
	}

	if err := s.DeleteConversationRows(ctx, "s-1"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Conversation(ctx, "s-1"); err != state.ErrNotFound {
		t.Fatalf("conversation after delete: %v", err)
	}
	if _, err := s.GetConversationState(ctx, "s-1", "plans"); err != state.ErrNotFound {
		t.Fatalf("state after delete: %v", err)
	}
	if msgs, err := s.ListArchiveMessages(ctx, "s-1"); err != nil || len(msgs) != 0 {
		t.Fatalf("transcript after delete = %d messages, %v", len(msgs), err)
	}
	if n := countSummaryNodes(t, s, "s-1"); n != 0 {
		t.Fatalf("summary nodes after delete = %d, want 0", n)
	}
	if hits, err := s.SearchMessages(ctx, "alpha", state.SearchOptions{}); err != nil {
		t.Fatalf("search after delete: %v", err)
	} else if len(hits.Hits) != 0 {
		t.Fatalf("index hits after delete = %+v, want none", hits.Hits)
	}

	// The conversation that was not deleted is untouched.
	if msgs, err := s.ListArchiveMessages(ctx, "s-2"); err != nil || len(msgs) != 1 {
		t.Fatalf("surviving transcript = %d messages, %v", len(msgs), err)
	}
	if n := countSummaryNodes(t, s, "s-2"); n != 1 {
		t.Fatalf("surviving summary nodes = %d, want 1", n)
	}
	if _, err := s.GetConversationState(ctx, "s-2", "plans"); err != nil {
		t.Fatalf("surviving state: %v", err)
	}
	if hits, err := s.SearchMessages(ctx, "beta", state.SearchOptions{}); err != nil {
		t.Fatalf("search for the survivor: %v", err)
	} else if len(hits.Hits) != 1 || hits.Hits[0].ConversationID != "s-2" {
		t.Fatalf("survivor hits = %+v", hits.Hits)
	}
}

// insertSummaryNode writes one summary row directly: the summary tree is
// owned by the memory capability, whose store sits above this layer, so
// a state-level test seeds the table it asserts the delete cleans up.
func insertSummaryNode(
	t *testing.T, s *state.Store, id, conversationID string, at time.Time,
) {
	t.Helper()
	if _, err := s.Handle().SQLDB().ExecContext(context.Background(), `
		INSERT INTO summary_nodes(
			id, thread_id, level, parent_ids, source_ids, summary,
			created_at, updated_at, metadata
		) VALUES (?, ?, 0, '[]', '[]', 'folded', ?, ?, '{}')`,
		id, conversationID,
		at.UTC().Format(time.RFC3339Nano), at.UTC().Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert summary node: %v", err)
	}
}

func countSummaryNodes(t *testing.T, s *state.Store, conversationID string) int {
	t.Helper()
	var n int
	if err := s.Handle().SQLDB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM summary_nodes WHERE thread_id = ?`,
		conversationID).Scan(&n); err != nil {
		t.Fatalf("count summary nodes: %v", err)
	}
	return n
}

func TestListArchiveTurnsAfterKeepsOnlyNewerTurns(t *testing.T) {
	s := openState(t, filepath.Join(t.TempDir(), "session.db"))
	ctx := context.Background()
	conv := state.Conversation{ID: "s-1"}
	// Seq starts at 1, so the turns below carry seq 1, 2 and 3.
	for _, run := range []string{"run-1", "run-2", "run-3"} {
		if err := s.CommitConversationTurn(ctx, conv, state.ArchiveTurn{
			RunID: run,
		}, []state.ArchiveMessage{
			{Role: string(message.RoleUser), Content: message.Content{
				Parts: []message.Part{message.TextPart{Text: run}},
			}},
		}); err != nil {
			t.Fatal(err)
		}
	}

	turns, err := s.ListArchiveTurnsAfter(ctx, "s-1", 1, 0)
	if err != nil {
		t.Fatalf("ListArchiveTurnsAfter: %v", err)
	}
	if len(turns) != 2 || turns[0].RunID != "run-2" || turns[1].RunID != "run-3" {
		t.Fatalf("turns after seq 1 = %+v", turns)
	}
	// Oldest first, and the limit keeps the turns closest to the cursor:
	// this is a tail read, not a page.
	turns, err = s.ListArchiveTurnsAfter(ctx, "s-1", 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 || turns[0].RunID != "run-1" || turns[1].RunID != "run-2" {
		t.Fatalf("limited tail = %+v", turns)
	}
	turns, err = s.ListArchiveTurnsAfter(ctx, "s-1", 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 0 {
		t.Fatalf("turns past the end = %+v", turns)
	}
}

func TestConversationByImportSource(t *testing.T) {
	s := openState(t, filepath.Join(t.TempDir(), "session.db"))
	ctx := context.Background()
	c := state.Conversation{ID: "s-1", ImportSource: "src-a"}
	if err := s.EnsureConversation(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.ConversationByImportSource(ctx, "src-a")
	if err != nil || !ok || got.ID != "s-1" {
		t.Fatalf("by source = %+v, %v, %v", got, ok, err)
	}
}
