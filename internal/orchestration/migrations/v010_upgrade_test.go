package migrations

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// TestWorkspaceUpgradesV010 verifies the migration chain can upgrade an
// official v0.1.0 workspace: that version already recorded migrations
// 1..7 and created session_settings with model but no mode column.
func TestWorkspaceUpgradesV010(t *testing.T) {
	ctx := context.Background()
	handle, err := db.OpenWithOptions(
		filepath.Join(t.TempDir(), "session.db"),
		db.OpenOptions{ForeignKeys: false},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = handle.Close() }()

	if _, err := handle.SQLDB().ExecContext(ctx, `
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`); err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 7; version++ {
		if _, err := handle.SQLDB().ExecContext(ctx,
			`INSERT INTO schema_migrations(version, applied_at)
			 VALUES (?, '2026-01-01T00:00:00Z')`, version,
		); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := handle.SQLDB().ExecContext(ctx, `
		CREATE TABLE session_settings (
			context_id TEXT PRIMARY KEY,
			think_level TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			model TEXT NOT NULL DEFAULT ''
		)`); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.SQLDB().ExecContext(ctx, `
		CREATE TABLE items (
			id TEXT PRIMARY KEY,
			thread_id TEXT NOT NULL,
			turn_id TEXT NOT NULL,
			seq INTEGER NOT NULL,
			item_type TEXT NOT NULL,
			role TEXT,
			payload TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.SQLDB().ExecContext(ctx, `
		INSERT INTO items(
			id, thread_id, turn_id, seq, item_type, role, payload, created_at
		) VALUES ('s-legacy:turn-1:0', 's-legacy', 'turn-1', 0, 'text', 'user',
		          '{"text":"legacy memory"}', '2026-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatal(err)
	}

	sessionsRoot := filepath.Join(t.TempDir(), "sessions")
	historyDir := filepath.Join(sessionsRoot, "s-legacy", "history")
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

	if err := Workspace(ctx, handle, sessionsRoot); err != nil {
		t.Fatalf("Workspace upgrade from v0.1.0: %v", err)
	}

	var hasMode int
	if err := handle.SQLDB().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM pragma_table_info('session_settings')
		WHERE name = 'mode'`).Scan(&hasMode); err != nil {
		t.Fatal(err)
	}
	if hasMode != 1 {
		t.Fatal("mode column was not added by migration 008")
	}

	var applied8 int
	if err := handle.SQLDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = 8`,
	).Scan(&applied8); err != nil {
		t.Fatal(err)
	}
	if applied8 != 1 {
		t.Fatal("migration 8 was not recorded")
	}

	var copied int
	if err := handle.SQLDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memory_items WHERE thread_id = 's-legacy'`,
	).Scan(&copied); err != nil {
		t.Fatal(err)
	}
	if copied != 1 {
		t.Fatalf("memory_items rows = %d, want 1 after v0.1.0 upgrade", copied)
	}
	var oldItems int
	if err := handle.SQLDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master
		 WHERE type = 'table' AND name = 'items'`,
	).Scan(&oldItems); err != nil {
		t.Fatal(err)
	}
	if oldItems != 0 {
		t.Fatal("old items table was not dropped by migration 009")
	}
	var archived int
	if err := handle.SQLDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM archive_messages
		 WHERE conversation_id = 's-legacy'`,
	).Scan(&archived); err != nil {
		t.Fatal(err)
	}
	if archived != 1 {
		t.Fatalf("archive messages = %d, want 1 after JSON import", archived)
	}
	if _, err := os.Stat(historyDir); !os.IsNotExist(err) {
		t.Fatalf("legacy history dir was not removed: %v", err)
	}
}
