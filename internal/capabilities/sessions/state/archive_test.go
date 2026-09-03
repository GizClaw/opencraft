package state

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"
)

func TestConversationArchiveCommitRoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	conv := Conversation{
		ID:        "s-1",
		Title:     "hello",
		CreatedAt: time.Now().UTC(),
	}
	if err := s.CommitConversationTurn(ctx, conv, ArchiveTurn{
		RunID:       "run-1",
		At:          time.Now().UTC(),
		RequestedAt: time.Now().UTC(),
		StartedAt:   time.Now().UTC(),
		FinishedAt:  time.Now().UTC(),
	}, []ArchiveMessage{
		{Role: string(message.RoleUser), Content: message.Content{
			Parts: []message.Part{message.TextPart{Text: "hi"}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	// Duplicate run id is a no-op.
	if err := s.CommitConversationTurn(ctx, conv, ArchiveTurn{RunID: "run-1"}, []ArchiveMessage{
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
	if len(turns) != 1 || turns[0].RunID != "run-1" {
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

func TestConversationDeleteRemovesAllRows(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	if err := s.CommitConversationTurn(ctx, Conversation{ID: "s-1"}, ArchiveTurn{
		RunID: "r1",
	}, []ArchiveMessage{
		{Role: string(message.RoleUser), Content: message.Content{
			Parts: []message.Part{message.TextPart{Text: "x"}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetConversationState(ctx, "s-1", "plans", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteConversationRows(ctx, "s-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Conversation(ctx, "s-1"); err != ErrNotFound {
		t.Fatalf("conversation after delete: %v", err)
	}
	if _, err := s.GetConversationState(ctx, "s-1", "plans"); err != ErrNotFound {
		t.Fatalf("state after delete: %v", err)
	}
}

func TestConversationByImportSource(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	c := Conversation{ID: "s-1", ImportSource: "src-a"}
	if err := s.EnsureConversation(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.ConversationByImportSource(ctx, "src-a")
	if err != nil || !ok || got.ID != "s-1" {
		t.Fatalf("by source = %+v, %v, %v", got, ok, err)
	}
}
