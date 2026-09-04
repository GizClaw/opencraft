package migrations

import (
	"context"
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

	if err := Workspace(ctx, handle, filepath.Join(t.TempDir(), "sessions")); err != nil {
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
}
