package sessions

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/message/media"
)

func TestForkCopiesHistoryThroughRun(t *testing.T) {
	store, err := newMigratedStore(t.TempDir(), 40)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseDB() })
	ctx := context.Background()

	sourceID, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	turnOne := []message.Message{
		message.NewTextMessage(message.RoleUser, "first question"),
		message.NewTextMessage(message.RoleAssistant, "first answer"),
	}
	turnTwo := []message.Message{
		message.NewTextMessage(message.RoleUser, "second question"),
		message.NewTextMessage(message.RoleAssistant, "second answer"),
	}
	if err := store.AppendTurnWithRunID(ctx, sourceID, "run-1", turnOne); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordTurnEnd(
		sourceID, "run-1", time.Now().UTC(), "completed", "",
	); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurnWithRunID(ctx, sourceID, "run-2", turnTwo); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordTurnEnd(
		sourceID, "run-2", time.Now().UTC(), "completed", "",
	); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMode(ctx, sourceID, ModeReadOnly); err != nil {
		t.Fatal(err)
	}
	if err := store.SetThink(ctx, sourceID, ThinkHigh); err != nil {
		t.Fatal(err)
	}
	if err := store.SetModel(ctx, sourceID, "provider/model"); err != nil {
		t.Fatal(err)
	}

	forked, err := store.Fork(ctx, sourceID, "run-1")
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if forked.ID == sourceID {
		t.Fatalf("fork reused source id %q", forked.ID)
	}
	if len(forked.Turns) != 1 {
		t.Fatalf("forked turns = %d, want 1", len(forked.Turns))
	}

	turns, err := store.Turns(ctx, forked.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 {
		t.Fatalf("new session turns = %d, want 1", len(turns))
	}
	if turns[0].RunID != "run-1" || turns[0].Status != "completed" {
		t.Fatalf("new turn = %+v", turns[0])
	}
	var userText, assistantText string
	for _, m := range turns[0].Messages {
		if m.Role == message.RoleUser {
			userText += m.Content.Text()
		}
		if m.Role == message.RoleAssistant {
			assistantText += m.Content.Text()
		}
	}
	if userText != "first question" || assistantText != "first answer" {
		t.Fatalf("forked text = %q / %q", userText, assistantText)
	}

	sourceTurns, err := store.Turns(ctx, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sourceTurns) != 2 {
		t.Fatalf("source turns = %d, want 2", len(sourceTurns))
	}
	if mode, err := store.Mode(ctx, forked.ID); err != nil || mode != ModeReadOnly {
		t.Fatalf("fork mode = %q, %v; want read-only", mode, err)
	}
	if think, err := store.Think(ctx, forked.ID); err != nil || think != ThinkHigh {
		t.Fatalf("fork think = %q, %v; want high", think, err)
	}
	if model, err := store.Model(ctx, forked.ID); err != nil || model != "provider/model" {
		t.Fatalf("fork model = %q, %v", model, err)
	}
}

func TestForkCopiesSessionAttachments(t *testing.T) {
	store, err := newMigratedStore(t.TempDir(), 40)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseDB() })
	ctx := context.Background()

	sourceID, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(src, []byte("fork-attachment"), 0o600); err != nil {
		t.Fatal(err)
	}
	stored, err := store.SaveAttachment(sourceID, "media", src)
	if err != nil {
		t.Fatal(err)
	}
	storedJSON, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"kind":"url","url":` + string(storedJSON) +
		`,"media_type":"image/png"}`)
	var imageSource media.ImageSource
	if err := json.Unmarshal(raw, &imageSource); err != nil {
		t.Fatal(err)
	}
	msg := message.Message{
		Role: message.RoleUser,
		Content: message.Content{
			Parts: []message.Part{
				message.ImagePart{Source: imageSource},
				message.ImagePart{Source: imageSource},
			},
		},
	}
	if err := store.AppendTurnWithRunID(ctx, sourceID, "run-img", []message.Message{msg}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordTurnEnd(
		sourceID, "run-img", time.Now().UTC(), "completed", "",
	); err != nil {
		t.Fatal(err)
	}

	forked, err := store.Fork(ctx, sourceID, "run-img")
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	forkedMsgs := forked.Turns[0].Messages
	if len(forkedMsgs) != 1 {
		t.Fatalf("forked messages = %d, want 1", len(forkedMsgs))
	}
	prefix := store.dir(forked.ID) + string(filepath.Separator) + "media"
	var newPath string
	for i := 0; i < 2; i++ {
		part, err := message.NormalizePart(forkedMsgs[0].Content.Parts[i])
		if err != nil {
			t.Fatal(err)
		}
		image, ok := part.(message.ImagePart)
		if !ok {
			t.Fatalf("forked part %d = %T, want ImagePart", i, part)
		}
		if i == 0 {
			newPath = image.Source.URL()
		}
		if !strings.HasPrefix(image.Source.URL(), prefix) {
			t.Fatalf("forked image path %q not under %q",
				image.Source.URL(), prefix)
		}
		if image.Source.URL() != newPath {
			t.Fatalf("duplicate attachment copied twice: %q != %q",
				image.Source.URL(), newPath)
		}
	}
	data, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("read forked image: %v", err)
	}
	if string(data) != "fork-attachment" {
		t.Fatalf("forked image content = %q", data)
	}
}

func TestForkRejectsUnfinishedSourceTurn(t *testing.T) {
	store, err := newMigratedStore(t.TempDir(), 40)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseDB() })
	ctx := context.Background()

	sourceID, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurnWithRunID(ctx, sourceID, "run-fail", []message.Message{
		message.NewTextMessage(message.RoleUser, "question"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordTurnEnd(
		sourceID, "run-fail", time.Now().UTC(), "failed", "boom",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Fork(ctx, sourceID, "run-fail"); err == nil {
		t.Fatal("Fork accepted a failed turn")
	} else if !strings.Contains(err.Error(), "cannot fork") {
		t.Fatalf("Fork error = %v", err)
	}
}
