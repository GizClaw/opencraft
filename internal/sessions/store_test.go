package sessions

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/message/media"
)

// TestSaveAttachment verifies URL-sourced attachments land in the
// session's media/files directories with the source extension.
func TestSaveAttachment(t *testing.T) {
	store, err := New(t.TempDir(), 40)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	id, err := store.Create()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	src := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(src, []byte("fake-png"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	dst, err := store.SaveAttachment(id, "media", src)
	if err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}
	rel, err := filepath.Rel(store.dir(id), dst)
	if err != nil || !strings.HasPrefix(rel, "media"+string(filepath.Separator)) {
		t.Fatalf("stored path %q not under session media dir", dst)
	}
	if !strings.HasSuffix(dst, ".png") {
		t.Errorf("stored name lost extension: %q", dst)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read stored attachment: %v", err)
	}
	if string(data) != "fake-png" {
		t.Errorf("stored content = %q, want source bytes", data)
	}
	if _, err := store.SaveAttachment(id, "other", src); err == nil {
		t.Error("SaveAttachment accepted unknown kind")
	}
	if _, err := store.SaveAttachment(id, "files", filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("SaveAttachment accepted missing source")
	}
}

// TestAppendTurnKeepsMediaURL verifies multimodal user parts survive
// the archive in URL form (the session persists the stored path, not
// the inline bytes), so /resume can re-render attachments.
func TestAppendTurnKeepsMediaURL(t *testing.T) {
	store, err := New(t.TempDir(), 40)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	const stored = "/Users/test/Workspace/proj/.opencraft/sessions/s-abc/media/1234-photo.png"
	var imgSource media.ImageSource
	if err := json.Unmarshal(
		[]byte(`{"kind":"url","url":`+strconv.Quote(stored)+`,"media_type":"image/png"}`),
		&imgSource,
	); err != nil {
		t.Fatalf("build image source: %v", err)
	}
	msgs := []message.Message{{
		Role: message.RoleUser,
		Content: message.Content{Parts: []message.Part{
			message.TextPart{Text: "look"},
			message.ImagePart{Source: imgSource},
			message.FilePart{URI: "/Users/test/notes.txt", MediaType: "text/plain", Name: "notes.txt"},
		}},
	}}
	if err := store.AppendTurn(context.Background(), id, msgs); err != nil {
		t.Fatalf("AppendTurn: %v", err)
	}
	hist, err := store.History(context.Background(), id, -1)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 1 || len(hist[0].Content.Parts) != 3 {
		t.Fatalf("history = %+v, want 1 message with 3 parts", hist)
	}
	img, ok := hist[0].Content.Parts[1].(message.ImagePart)
	if !ok {
		t.Fatalf("part 1 is %T, want ImagePart", hist[0].Content.Parts[1])
	}
	if img.Source.Kind() != media.SourceURL || img.Source.URL() != stored {
		t.Errorf("image source = %s %q, want url %q", img.Source.Kind(), img.Source.URL(), stored)
	}
	file, ok := hist[0].Content.Parts[2].(message.FilePart)
	if !ok || file.URI != "/Users/test/notes.txt" {
		t.Errorf("file part = %+v", hist[0].Content.Parts[2])
	}
}

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

func TestSessionFilePermissions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	store, err := New(root, 40)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurn(context.Background(), id, []message.Message{
		message.NewTextMessage(message.RoleUser, "hi"),
	}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, id, "history", "000001.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("history file mode = %o, want 600", info.Mode().Perm())
	}
	dir, err := os.Stat(filepath.Join(root, id))
	if err != nil {
		t.Fatal(err)
	}
	if dir.Mode().Perm() != 0o700 {
		t.Errorf("session dir mode = %o, want 700", dir.Mode().Perm())
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if rootInfo.Mode().Perm() != 0o700 {
		t.Errorf("sessions root mode = %o, want 700", rootInfo.Mode().Perm())
	}
}

func TestMetaIndexSurvivesUsageRecord(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	id, _ := store.Create()
	if err := store.AppendTurn(context.Background(), id, []message.Message{
		message.NewTextMessage(message.RoleUser, "first"),
		message.NewTextMessage(message.RoleAssistant, "second"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordUsage(context.Background(), id, Usage{TotalTokens: 42}); err != nil {
		t.Fatal(err)
	}
	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Messages != 2 || list[0].Usage.TotalTokens != 42 {
		t.Fatalf("list = %+v, want 2 messages with recorded usage", list)
	}
	if list[0].Title != "first" {
		t.Errorf("title = %q, want first", list[0].Title)
	}
	title, err := store.Title(id)
	if err != nil || title != "first" {
		t.Errorf("Title = %q, %v", title, err)
	}
	first, err := store.FirstUserMessage(id)
	if err != nil || first != "first" {
		t.Errorf("FirstUserMessage = %q, %v", first, err)
	}
}

func TestListLegacyArchiveWithoutMeta(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	id, _ := store.Create()
	// Simulate a pre-index archive: write turn files but no meta.json.
	historyDir := filepath.Join(store.root, id, "history")
	for i := 1; i <= 3; i++ {
		data := `{"seq":` + string(rune('0'+i)) +
			`,"at":"2024-01-01T00:00:00Z","messages":[{"role":"user","content":{"parts":[{"type":"text","text":"legacy msg"}]}}]}`
		if err := os.WriteFile(
			filepath.Join(historyDir, fmt.Sprintf("%06d.json", i)),
			[]byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Messages != 3 {
		t.Fatalf("legacy list = %+v, want 3 turns listed", list)
	}
	if list[0].Title != "legacy msg" {
		t.Errorf("legacy title = %q", list[0].Title)
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

func TestAllIDMethodsRejectTraversal(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(victim, "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Crafted ids must be rejected before any filesystem access by
	// every method that resolves id against the store root.
	bad := "s-../victim"
	if err := store.AppendTurn(context.Background(), bad, []message.Message{
		message.NewTextMessage(message.RoleUser, "x"),
	}); err == nil {
		t.Error("AppendTurn accepted traversal id")
	}
	if _, err := store.History(context.Background(), bad, -1); err == nil {
		t.Error("History accepted traversal id")
	}
	if _, err := store.LoadUsage(context.Background(), bad); err == nil {
		t.Error("LoadUsage accepted traversal id")
	}
	if err := store.RecordUsage(context.Background(), bad, Usage{TotalTokens: 1}); err == nil {
		t.Error("RecordUsage accepted traversal id")
	}
	if err := store.WriteState(bad, "title", "x"); err == nil {
		t.Error("WriteState accepted traversal id")
	}
	if err := store.ReadState(bad, "title", new(string)); err == nil {
		t.Error("ReadState accepted traversal id")
	}
	if _, err := os.Stat(filepath.Join(victim, "keep.txt")); err != nil {
		t.Fatalf("victim directory was touched: %v", err)
	}

	// Valid generated ids still work through the same methods.
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurn(context.Background(), id, []message.Message{
		message.NewTextMessage(message.RoleUser, "x"),
	}); err != nil {
		t.Fatalf("AppendTurn valid id: %v", err)
	}
	if err := store.RecordUsage(context.Background(), id, Usage{TotalTokens: 1}); err != nil {
		t.Fatalf("RecordUsage valid id: %v", err)
	}
	if err := store.WriteState(id, "title", "hi"); err != nil {
		t.Fatalf("WriteState valid id: %v", err)
	}
}

// TestListEmptyWhenRootMissing verifies List returns an empty non-nil
// slice when the session directory does not exist, so the desktop UI
// never receives a JSON null for the session list.
func TestListEmptyWhenRootMissing(t *testing.T) {
	dir := t.TempDir()
	store, err := New(filepath.Join(dir, "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := os.RemoveAll(store.root); err != nil {
		t.Fatal(err)
	}
	metas, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if metas == nil {
		t.Fatal("List returned nil; want empty non-nil slice")
	}
	if len(metas) != 0 {
		t.Fatalf("List = %d entries, want 0", len(metas))
	}
}
