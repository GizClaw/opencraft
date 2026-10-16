package compat_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions/state"
	"github.com/GizClaw/opencraft/internal/foundation/compat"
	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// The archive is what the counters mean. A legacy meta.json carried its
// own tally, but the adopt path counts the turns it writes, so seeding
// the conversation row from the metadata counted every turn twice. The
// title is the other half: legacy metadata often had none, and the
// conversation is then named from the first line a person wrote.
func TestLegacyImportCountsAndNamesFromTheTranscript(t *testing.T) {
	ctx := context.Background()
	handle, err := db.OpenWithOptions(
		filepath.Join(t.TempDir(), "session.db"),
		db.OpenOptions{ForeignKeys: false})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = handle.Close() }()

	root := filepath.Join(t.TempDir(), "sessions")
	dir := filepath.Join(root, "s-legacy")
	history := filepath.Join(dir, "history")
	if err := os.MkdirAll(history, 0o700); err != nil {
		t.Fatal(err)
	}
	// The metadata claims nine turns and forty messages and carries no
	// title; the transcript holds two turns and three messages.
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(`{
		"turn_count": 9,
		"message_count": 40,
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-02T00:00:00Z"
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(history, "000001.json"),
		[]byte(`{"seq":1,"at":"2026-01-01T00:00:00Z","messages":[
			{"role":"user","content":{"parts":[{"type":"text","text":"迁移计数为什么要信归档"}]}},
			{"role":"assistant","content":{"parts":[{"type":"text","text":"因为归档是事实"}]}}
		]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(history, "000002.json"),
		[]byte(`{"seq":2,"at":"2026-01-02T00:00:00Z","messages":[
			{"role":"user","content":{"parts":[{"type":"text","text":"第二条"}]}}
		]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := compat.Workspace(ctx, handle, root, state.Importer(handle)); err != nil {
		t.Fatalf("Workspace: %v", err)
	}

	var title string
	var turnCount, messageCount int
	if err := handle.SQLDB().QueryRowContext(ctx, `
		SELECT title, turn_count, message_count FROM conversations
		WHERE id = 's-legacy'`).Scan(
		&title, &turnCount, &messageCount); err != nil {
		t.Fatal(err)
	}
	if turnCount != 2 || messageCount != 3 {
		t.Fatalf("counts = %d turns / %d messages, want 2 / 3 (the archive, not the metadata's 9 / 40)",
			turnCount, messageCount)
	}
	if title != "迁移计数为什么要信归档" {
		t.Fatalf("title = %q, want the first line the user wrote", title)
	}

	// Re-running the migration must not import the session a second
	// time: the counts stay put.
	if err := compat.Workspace(ctx, handle, root, state.Importer(handle)); err != nil {
		t.Fatalf("Workspace (second run): %v", err)
	}
	if err := handle.SQLDB().QueryRowContext(ctx, `
		SELECT turn_count, message_count FROM conversations
		WHERE id = 's-legacy'`).Scan(&turnCount, &messageCount); err != nil {
		t.Fatal(err)
	}
	if turnCount != 2 || messageCount != 3 {
		t.Fatalf("second run counted again: %d turns / %d messages",
			turnCount, messageCount)
	}
}
