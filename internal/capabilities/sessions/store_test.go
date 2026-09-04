package sessions

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/message/media"
)

// TestSaveAttachment verifies URL-sourced attachments land in the
// session's media/files directories with the source extension.
func TestSaveAttachment(t *testing.T) {
	store, err := newMigratedStore(t.TempDir(), 40)
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
	store, err := newMigratedStore(t.TempDir(), 40)
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
	store, err := newMigratedStore(filepath.Join(t.TempDir(), "sessions"), 40)
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
	store, err := newMigratedStore(root, 40)
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
	if _, err := os.Stat(filepath.Join(root, "session.db")); err != nil {
		t.Fatalf("session.db: %v", err)
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
	store, err := newMigratedStore(filepath.Join(t.TempDir(), "sessions"), 40)
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

func TestListSkipsArchiveWithoutMeta(t *testing.T) {
	store, err := newMigratedStore(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	// A created-but-never-used conversation must not appear in the
	// resume list.
	_, _ = store.Create()
	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("empty conversation listed = %+v, want empty", list)
	}
}

func TestHistoryWindow(t *testing.T) {
	store, err := newMigratedStore(filepath.Join(t.TempDir(), "sessions"), 40)
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
	store, err := newMigratedStore(filepath.Join(t.TempDir(), "sessions"), 40)
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
		if err := store.Remove(context.Background(), id); err == nil {
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
	if err := store.Remove(context.Background(), id); err != nil {
		t.Fatalf("Remove(valid id): %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.root, id)); !os.IsNotExist(err) {
		t.Fatalf("valid session dir still exists: %v", err)
	}
}

func TestListMeta(t *testing.T) {
	store, err := newMigratedStore(filepath.Join(t.TempDir(), "sessions"), 40)
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
	store, err := newMigratedStore(filepath.Join(t.TempDir(), "sessions"), 40)
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
	store, err := newMigratedStore(filepath.Join(t.TempDir(), "sessions"), 40)
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
	store, err := newMigratedStore(filepath.Join(t.TempDir(), "sessions"), 40)
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
	store, err := newMigratedStore(filepath.Join(dir, "sessions"), 40)
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

// TestBufferArtifactMergesIntoTurn verifies buffered artifacts are
// attached to the next archived turn (deduped by path with the latest
// byte count), cleared after append, and readable back through Turns.
func TestBufferArtifactMergesIntoTurn(t *testing.T) {
	store, err := newMigratedStore(t.TempDir(), 40)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BufferArtifact(id, "docs/report.md", 100); err != nil {
		t.Fatalf("BufferArtifact: %v", err)
	}
	// Re-writing the same path refreshes bytes in place.
	if err := store.BufferArtifact(id, "docs/report.md", 250); err != nil {
		t.Fatal(err)
	}
	if err := store.BufferArtifact(id, "slides.pptx", 5000); err != nil {
		t.Fatal(err)
	}
	// Invalid ids are rejected before touching state.
	if err := store.BufferArtifact("bad-id", "x.md", 1); err == nil {
		t.Fatal("BufferArtifact accepted invalid session id")
	}

	if err := store.AppendTurn(context.Background(), id, []message.Message{
		message.NewTextMessage(message.RoleUser, "x"),
	}); err != nil {
		t.Fatalf("AppendTurn: %v", err)
	}
	turns, err := store.Turns(context.Background(), id)
	if err != nil {
		t.Fatalf("Turns: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("Turns = %d records, want 1", len(turns))
	}
	got := turns[0].Artifacts
	want := []Artifact{{Path: "docs/report.md", Bytes: 250}, {Path: "slides.pptx", Bytes: 5000}}
	if len(got) != len(want) {
		t.Fatalf("artifacts = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("artifacts[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	// The buffer is consumed: the next turn archives no artifacts.
	if err := store.AppendTurn(context.Background(), id, []message.Message{
		message.NewTextMessage(message.RoleUser, "y"),
	}); err != nil {
		t.Fatalf("AppendTurn #2: %v", err)
	}
	turns, err = store.Turns(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("Turns = %d records, want 2", len(turns))
	}
	if len(turns[1].Artifacts) != 0 {
		t.Fatalf("second turn artifacts = %+v, want none", turns[1].Artifacts)
	}
	if turns[0].Seq != 1 || turns[1].Seq != 2 {
		t.Fatalf("seqs = %d,%d want 1,2", turns[0].Seq, turns[1].Seq)
	}
}

// TestRecordTurnTimingPersistsWithArchivedTurn verifies the request and
// agent-start timestamps captured when a run begins are attached to the
// matching archived turn and readable back through Turns.
func TestRecordTurnTimingPersistsWithArchivedTurn(t *testing.T) {
	store, err := newMigratedStore(t.TempDir(), 40)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	requested := time.Date(2026, 9, 3, 1, 2, 3, 0, time.UTC)
	started := requested.Add(350 * time.Millisecond)
	if err := store.RecordTurnTiming(id, "run-timed", requested, started); err != nil {
		t.Fatalf("RecordTurnTiming: %v", err)
	}
	if err := store.RecordTurnTiming("bad-id", "run-timed", requested, started); err == nil {
		t.Fatal("RecordTurnTiming accepted invalid session id")
	}
	if err := store.AppendTurnWithRunID(context.Background(), id, "run-timed", []message.Message{
		message.NewTextMessage(message.RoleUser, "timed"),
	}); err != nil {
		t.Fatalf("AppendTurnWithRunID: %v", err)
	}
	turns, err := store.Turns(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 {
		t.Fatalf("Turns = %d records, want 1", len(turns))
	}
	if !turns[0].RequestedAt.Equal(requested) {
		t.Fatalf("RequestedAt = %v, want %v", turns[0].RequestedAt, requested)
	}
	if !turns[0].StartedAt.Equal(started) {
		t.Fatalf("StartedAt = %v, want %v", turns[0].StartedAt, started)
	}
	finished := started.Add(4 * time.Second)
	if err := store.RecordTurnFinished(id, "run-timed", finished); err != nil {
		t.Fatalf("RecordTurnFinished: %v", err)
	}
	if err := store.RecordTurnFinished("bad-id", "run-timed", finished); err == nil {
		t.Fatal("RecordTurnFinished accepted invalid session id")
	}
	turns, err = store.Turns(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !turns[0].FinishedAt.Equal(finished) {
		t.Fatalf("FinishedAt = %v, want %v", turns[0].FinishedAt, finished)
	}
	// A retried commit for the same run id is idempotent: the turn is
	// not archived twice.
	if err := store.AppendTurnWithRunID(context.Background(), id, "run-timed", []message.Message{
		message.NewTextMessage(message.RoleUser, "again"),
	}); err != nil {
		t.Fatalf("second AppendTurnWithRunID: %v", err)
	}
	turns, err = store.Turns(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 {
		t.Fatalf("Turns = %d records, want 1 after idempotent retry", len(turns))
	}
}

// TestLegacyJSONHistoryMigratedIntoSQLite verifies old history JSON is
// imported once and then removed.
func TestLegacyJSONHistoryMigratedIntoSQLite(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	id := "s-legacy1"
	historyDir := filepath.Join(root, id, "history")
	if err := os.MkdirAll(historyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(historyDir, "000001.json"),
		[]byte(`{"seq":1,"at":"2026-01-01T00:00:00Z","messages":[{"role":"user","content":{"parts":[{"type":"text","text":"legacy hello"}]}}]}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	store, err := newMigratedStore(root, 40)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	hist, err := store.History(context.Background(), id, -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 || hist[0].Content.Text() != "legacy hello" {
		t.Fatalf("migrated history = %+v", hist)
	}
	if _, err := os.Stat(historyDir); !os.IsNotExist(err) {
		t.Fatalf("legacy history dir was not removed: %v", err)
	}
}

// TestAppendTurnArtifactsMergesIntoLatestTurn verifies post-turn
// reconciliation merges into the turn that carried the run id (not
// just the latest file), refreshing matching paths in place, and is a
// no-op without an archived turn.
func TestAppendTurnArtifactsMergesIntoLatestTurn(t *testing.T) {
	store, err := newMigratedStore(t.TempDir(), 40)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BufferArtifact(id, "a.md", 100); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurnWithRunID(context.Background(), id, "run-1", []message.Message{
		message.NewTextMessage(message.RoleUser, "one"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.BufferArtifact(id, "b.md", 200); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurnWithRunID(context.Background(), id, "run-2", []message.Message{
		message.NewTextMessage(message.RoleUser, "two"),
	}); err != nil {
		t.Fatal(err)
	}

	// Reconciliation targeting run-1 lands on turn 1 even though turn 2
	// is the latest file (the next turn can start before waitTurn's
	// post-turn scan finishes).
	merged, err := store.AppendTurnArtifacts(id, "run-1", []Artifact{
		{Path: "a.md", Bytes: 150},
		{Path: "early.pptx", Bytes: 300},
	})
	if err != nil {
		t.Fatalf("AppendTurnArtifacts: %v", err)
	}
	wantMerged := []Artifact{{Path: "a.md", Bytes: 150}, {Path: "early.pptx", Bytes: 300}}
	if len(merged) != len(wantMerged) {
		t.Fatalf("merged = %+v, want %+v", merged, wantMerged)
	}

	// Reconciliation for run-2 refreshes b.md and adds c.pptx.
	merged, err = store.AppendTurnArtifacts(id, "run-2", []Artifact{
		{Path: "b.md", Bytes: 250},
		{Path: "c.pptx", Bytes: 300},
	})
	if err != nil {
		t.Fatalf("AppendTurnArtifacts: %v", err)
	}
	wantMerged = []Artifact{{Path: "b.md", Bytes: 250}, {Path: "c.pptx", Bytes: 300}}
	if len(merged) != len(wantMerged) {
		t.Fatalf("merged = %+v, want %+v", merged, wantMerged)
	}
	turns, err := store.Turns(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("Turns = %d, want 2", len(turns))
	}
	got := turns[0].Artifacts
	want := []Artifact{{Path: "a.md", Bytes: 150}, {Path: "early.pptx", Bytes: 300}}
	if len(got) != len(want) {
		t.Fatalf("turn 1 artifacts = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("turn 1 artifacts[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	got = turns[1].Artifacts
	want = []Artifact{{Path: "b.md", Bytes: 250}, {Path: "c.pptx", Bytes: 300}}
	if len(got) != len(want) {
		t.Fatalf("turn 2 artifacts = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("turn 2 artifacts[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	// A conversation with no archived turn is a silent no-op.
	empty, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendTurnArtifacts(empty, "run-x", []Artifact{{Path: "x.md", Bytes: 1}}); err != nil {
		t.Fatalf("AppendTurnArtifacts without turn: %v", err)
	}
	if _, err := store.AppendTurnArtifacts("bad-id", "run-x", []Artifact{{Path: "x.md", Bytes: 1}}); err == nil {
		t.Fatal("AppendTurnArtifacts accepted invalid session id")
	}
}
