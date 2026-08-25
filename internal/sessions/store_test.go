package sessions

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"
)

func TestAppendAndHistory(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	msgs := []message.Message{
		message.NewTextMessage(message.RoleUser, "你好"),
		{Role: message.RoleAssistant, Content: message.Content{Parts: []message.Part{
			message.TextPart{Text: "你好！"},
		}}},
	}
	if err := store.AppendTurn(context.Background(), id, msgs); err != nil {
		t.Fatal(err)
	}
	hist, err := store.History(context.Background(), id, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 ||
		hist[0].Content.Text() != "你好" ||
		hist[1].Content.Text() != "你好！" {
		t.Errorf("history = %+v", hist)
	}
}

func TestHistoryWindow(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := store.Create()
	for i := 0; i < 5; i++ {
		_ = store.AppendTurn(context.Background(), id, []message.Message{
			message.NewTextMessage(message.RoleUser, "msg"),
		})
	}
	hist, _ := store.History(context.Background(), id, 3)
	if len(hist) != 3 {
		t.Fatalf("windowed history = %d, want 3", len(hist))
	}
}

func TestRemoveRejectsTraversalID(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(victim, "keep.txt"),
		[]byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Crafted ids must be rejected before any filesystem access, even
	// when enough ".." segments would otherwise resolve outside root.
	for _, id := range []string{
		"s-../victim",
		"s-../../../../" + filepath.Base(victim),
		"../" + filepath.Base(victim),
		"not-a-session",
	} {
		if err := store.Remove(id); err == nil {
			t.Fatalf("Remove(%q) accepted", id)
		}
	}
	if _, err := os.Stat(filepath.Join(victim, "keep.txt")); err != nil {
		t.Fatalf("victim directory was touched: %v", err)
	}

	// A valid generated id still removes its own directory.
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurn(context.Background(), id, []message.Message{
		message.NewTextMessage(message.RoleUser, "x"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Remove(id); err != nil {
		t.Fatalf("Remove(valid id): %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.root, id)); !os.IsNotExist(err) {
		t.Fatalf("valid session dir still exists: %v", err)
	}
}

func TestListMeta(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := store.Create()
	_ = store.AppendTurn(context.Background(), id, []message.Message{
		message.NewTextMessage(message.RoleUser, "这是第一条消息"),
	})
	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != id || list[0].Messages != 1 {
		t.Errorf("list = %+v", list)
	}
	if list[0].Title != "这是第一条消息" {
		t.Errorf("title = %q", list[0].Title)
	}
}

func TestRecordAndLoadUsage(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	// A fresh session reports no usage.
	got, err := store.LoadUsage(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got != (Usage{}) {
		t.Errorf("fresh usage = %+v, want zero", got)
	}
	want := Usage{
		InputTokens:      1000,
		OutputTokens:     500,
		TotalTokens:      1500,
		CacheReadTokens:  600,
		CacheWriteTokens: 50,
		ReasoningTokens:  200,
		LatencyMs:        1234,
	}
	if err := store.RecordUsage(context.Background(), id, want); err != nil {
		t.Fatal(err)
	}
	got, err = store.LoadUsage(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("usage = %+v, want %+v", got, want)
	}
	// List exposes the recorded usage so the /resume picker can show it.
	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Usage != want {
		t.Errorf("list usage = %+v, want %+v", list, want)
	}
}

func TestAppendSkipsEmpty(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := store.Create()
	if err := store.AppendTurn(context.Background(), id, []message.Message{
		{Role: message.RoleAssistant, Content: message.Content{Parts: nil}},
	}); err != nil {
		t.Fatal(err)
	}
	hist, _ := store.History(context.Background(), id, 0)
	if len(hist) != 0 {
		t.Errorf("empty turn should not be archived: %+v", hist)
	}
}
