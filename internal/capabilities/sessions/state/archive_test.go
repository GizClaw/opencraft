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
// conversation that is gone answers ErrNotFound and keeps its delta
// dropped, and the columns the archive owns — the title, the counts,
// the creation time — are left alone.
func TestUpdateConversationUsageIsUpdateOnly(t *testing.T) {
	s := openState(t, filepath.Join(t.TempDir(), "session.db"))
	ctx := context.Background()
	const id = "s-usage"
	created := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	if err := s.EnsureConversation(ctx, state.Conversation{
		ID:           id,
		Title:        "kept",
		CreatedAt:    created,
		UpdatedAt:    created,
		TurnCount:    2,
		MessageCount: 4,
	}); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}

	// The sidebar lists by updated_at, so the write has to carry the
	// caller's timestamp rather than one of its own.
	at := time.Date(2026, 9, 25, 9, 30, 0, 0, time.UTC)
	if err := s.UpdateConversationUsage(ctx, id,
		[]byte(`{"total_tokens":42}`), at); err != nil {
		t.Fatalf("update usage: %v", err)
	}
	c, err := s.Conversation(ctx, id)
	if err != nil {
		t.Fatalf("conversation after usage update: %v", err)
	}
	if string(c.UsageJSON) != `{"total_tokens":42}` || c.Title != "kept" {
		t.Fatalf("conversation after usage update = %+v", c)
	}
	if !c.UpdatedAt.Equal(at) {
		t.Fatalf("updated_at = %s, want %s", c.UpdatedAt, at)
	}
	if !c.CreatedAt.Equal(created) {
		t.Fatalf("created_at = %s, want %s", c.CreatedAt, created)
	}
	if c.TurnCount != 2 || c.MessageCount != 4 {
		t.Fatalf("counts after usage update = %d/%d, want 2/4 (the archive owns them)",
			c.TurnCount, c.MessageCount)
	}

	if err := s.DeleteConversationRows(ctx, id); err != nil {
		t.Fatalf("delete conversation: %v", err)
	}
	err = s.UpdateConversationUsage(ctx, id,
		[]byte(`{"total_tokens":1}`), time.Now().UTC())
	if !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("update after delete = %v, want ErrNotFound", err)
	}
}

// TestRetiredConversationRefusesEveryWriter pins the fence that makes a
// delete final: the delete records the id in deleted_conversations, and
// every writer that could recreate the conversation refuses that id by
// name — from the database, so a second process and a later generation
// refuse it too. The regression the fence closes is a writer landing
// after the delete (a detached review, a folded condensation, a
// delegation note) and rebuilding the row it was writing to.
func TestRetiredConversationRefusesEveryWriter(t *testing.T) {
	s := openState(t, filepath.Join(t.TempDir(), "session.db"))
	ctx := context.Background()
	const id = "s-retired"
	commitTurn(t, s, id, "doomed", "run-1", time.Now().UTC(), userText("hello"))
	if err := s.SetConversationState(ctx, id, "plan", []byte("{}")); err != nil {
		t.Fatalf("seed state document: %v", err)
	}
	if err := s.DeleteConversationRows(ctx, id); err != nil {
		t.Fatalf("delete conversation: %v", err)
	}
	retired, err := s.Retired(ctx, id)
	if err != nil || !retired {
		t.Fatalf("Retired after delete = %v, %v; want true", retired, err)
	}

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"EnsureConversation", func() error {
			return s.EnsureConversation(ctx, state.Conversation{ID: id})
		}},
		{"UpsertConversation", func() error {
			return s.UpsertConversation(ctx, state.Conversation{
				ID: id, Title: "back again",
			})
		}},
		{"CommitConversationTurn", func() error {
			return s.CommitConversationTurn(ctx,
				state.Conversation{ID: id},
				state.ArchiveTurn{RunID: "run-2"},
				[]state.ArchiveMessage{{
					Role: string(message.RoleUser),
					Content: message.Content{
						Parts: []message.Part{message.TextPart{Text: "again"}},
					},
				}})
		}},
		{"SetConversationState", func() error {
			return s.SetConversationState(ctx, id, "plan", []byte(`{"late":true}`))
		}},
		{"SetModel", func() error {
			return s.SetModel(ctx, id, "m")
		}},
	} {
		if err := tc.call(); !errors.Is(err, state.ErrRetired) {
			t.Errorf("%s after delete = %v, want ErrRetired", tc.name, err)
		}
	}

	// Nothing came back: not the row, not a document for it, not the
	// state the refused writes would have replaced.
	if _, err := s.Conversation(ctx, id); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("conversation after the refused writes = %v, want ErrNotFound", err)
	}
	if _, err := s.GetConversationState(ctx, id, "plan"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("state document after the refused writes = %v, want ErrNotFound", err)
	}
	// The fence is per id: a live conversation is written as before.
	const live = "s-live"
	if err := s.EnsureConversation(ctx, state.Conversation{ID: live}); err != nil {
		t.Fatalf("ensure a live conversation: %v", err)
	}
	if err := s.SetModel(ctx, live, "m"); err != nil {
		t.Fatalf("write a live conversation's settings: %v", err)
	}
}

// TestUpdateConversationUsageDefaultsTheCallersGaps pins the two
// defensive branches of the write: an empty usage block is stored as an
// empty JSON object, and a caller that passes no timestamp — the zero
// time, which would land in a column the sidebar sorts by — gets the
// write's own moment.
func TestUpdateConversationUsageDefaultsTheCallersGaps(t *testing.T) {
	s := openState(t, filepath.Join(t.TempDir(), "session.db"))
	ctx := context.Background()
	const id = "s-usage-defaults"
	if err := s.EnsureConversation(ctx, state.Conversation{ID: id}); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}

	before := time.Now().UTC().Add(-time.Second)
	if err := s.UpdateConversationUsage(ctx, id, nil, time.Time{}); err != nil {
		t.Fatalf("update usage: %v", err)
	}
	c, err := s.Conversation(ctx, id)
	if err != nil {
		t.Fatalf("conversation after usage update: %v", err)
	}
	if string(c.UsageJSON) != "{}" {
		t.Fatalf("empty usage stored as %s, want {}", c.UsageJSON)
	}
	if c.UpdatedAt.Before(before) || c.UpdatedAt.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("updated_at = %s, want the write's own moment", c.UpdatedAt)
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
