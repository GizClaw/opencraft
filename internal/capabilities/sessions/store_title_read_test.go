package sessions

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"
)

// A conversation is named by its first turn and keeps that name: the
// title CASE in CommitConversationTurn only takes the value while the
// row is untitled or still has no turns, so a later turn's opening line
// must not rename a chat that already has one.
func TestTitleNamesTheConversationOnce(t *testing.T) {
	store, err := newMigratedStore(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurn(ctx, id, []message.Message{
		message.NewTextMessage(message.RoleUser, "第一个问题：缓存为什么失效"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurn(ctx, id, []message.Message{
		message.NewTextMessage(message.RoleUser, "另一个话题：顺便看看测试"),
	}); err != nil {
		t.Fatal(err)
	}
	title, err := store.Title(id)
	if err != nil {
		t.Fatal(err)
	}
	if title != "第一个问题：缓存为什么失效" {
		t.Fatalf("title = %q, want the first turn's line", title)
	}
}

// FirstUserMessage reads the earliest user text and stops there. The
// turns the app wrote are skipped — a delegation note is the app
// reporting to the model, not speech — and a conversation whose early
// user rows carry no text (an attachment-only message) still finds the
// first thing a person actually typed.
func TestFirstUserMessageSkipsAppAuthoredTurns(t *testing.T) {
	store, err := newMigratedStore(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurnWithOriginAndRunID(ctx, id, "run-note",
		TurnOrigin{Kind: "delegation", Payload: []byte(`{"note":true}`)},
		[]message.Message{
			message.NewTextMessage(message.RoleUser, "delegation note for the model"),
		}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurnWithRunID(ctx, id, "run-empty", []message.Message{
		{Role: message.RoleUser, Content: message.Content{Parts: []message.Part{
			message.FilePart{URI: "file:///notes.txt", Name: "notes.txt"},
		}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurnWithRunID(ctx, id, "run-real", []message.Message{
		message.NewTextMessage(message.RoleUser, "真正的第一句话"),
		message.NewTextMessage(message.RoleAssistant, "好的"),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.FirstUserMessage(id)
	if err != nil {
		t.Fatal(err)
	}
	if got != "真正的第一句话" {
		t.Fatalf("FirstUserMessage = %q, want the user's own line", got)
	}
}
