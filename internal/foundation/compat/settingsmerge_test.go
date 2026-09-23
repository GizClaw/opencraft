package compat

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// seedPreSessionSettingsMergeWorkspace opens a workspace database
// migrated up to the step before 019 and seeds the legacy settings rows
// the tests below read.
func seedPreSessionSettingsMergeWorkspace(t *testing.T) *db.DB {
	t.Helper()
	ctx := context.Background()
	handle, err := db.Open(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })

	all := workspaceMigrations()
	pre := make([]db.Migration, 0, len(all))
	for _, m := range all {
		if m.Version < mergeSessionSettingsVersion {
			pre = append(pre, m)
		}
	}
	if err := handle.Migrate(ctx, pre); err != nil {
		t.Fatalf("apply pre-019 migrations: %v", err)
	}
	for _, stmt := range []string{
		// Every column set.
		`INSERT INTO session_settings
			(context_id, think_level, model, mode, updated_at) VALUES
			('s-full', 'high', 'anthropic/claude', 'yolo', '2026-09-10T08:00:00Z')`,
		// Only the reasoning effort (the shape 006 created).
		`INSERT INTO session_settings
			(context_id, think_level, model, mode, updated_at) VALUES
			('s-think', 'low', '', 'workspace', '2026-09-10T08:01:00Z')`,
		// Only the model hint.
		`INSERT INTO session_settings
			(context_id, think_level, model, mode, updated_at) VALUES
			('s-model', '', 'openai/gpt', 'workspace', '2026-09-10T08:02:00Z')`,
		// Nothing at all. The columns are NOT NULL with their own
		// defaults, so a row a build wrote always carries a mode; this
		// is the hand-edited shape the merge must not invent a value
		// for.
		`INSERT INTO session_settings
			(context_id, think_level, model, mode, updated_at) VALUES
			('s-empty', '', '', '', '2026-09-10T08:03:00Z')`,
		// A conversation that already holds a settings document. No build
		// that wrote session_settings also wrote one, but the merge must
		// not clobber it if it is there.
		`INSERT INTO conversation_state
			(conversation_id, name, value_json, updated_at) VALUES
			('s-live', 'settings', '{"mode":"read-only"}', '2026-09-10T08:04:00Z')`,
		`INSERT INTO session_settings
			(context_id, think_level, model, mode, updated_at) VALUES
			('s-live', 'xhigh', 'x/y', 'yolo', '2026-09-10T08:04:00Z')`,
	} {
		if _, err := handle.SQLDB().ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed pre-019 rows: %v", err)
		}
	}
	return handle
}

// settingsDocument reads one merged document, reporting whether it
// exists at all.
func settingsDocument(t *testing.T, handle *db.DB, conversationID string) (string, bool) {
	t.Helper()
	var raw string
	err := handle.SQLDB().QueryRowContext(context.Background(),
		`SELECT value_json FROM conversation_state
		 WHERE conversation_id = ? AND name = 'settings'`,
		conversationID).Scan(&raw)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return "", false
		}
		t.Fatalf("read settings document %s: %v", conversationID, err)
	}
	return raw, true
}

// TestSessionSettingsMergeMovesRowsIntoTheDocument is the migration's
// happy path: every legacy key survives into the conversation_state
// document, a key the row never carried stays absent, and a row that
// held nothing leaves no document behind.
func TestSessionSettingsMergeMovesRowsIntoTheDocument(t *testing.T) {
	ctx := context.Background()
	handle := seedPreSessionSettingsMergeWorkspace(t)
	if err := WorkspaceSchema(ctx, handle); err != nil {
		t.Fatalf("apply schema including 019: %v", err)
	}
	// A second run must be a no-op, not a re-merge.
	if err := WorkspaceSchema(ctx, handle); err != nil {
		t.Fatalf("schema must be idempotent: %v", err)
	}
	var applied int
	if err := handle.SQLDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`,
		mergeSessionSettingsVersion).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("step %d recorded %d times, want once",
			mergeSessionSettingsVersion, applied)
	}
	var table int
	if err := handle.SQLDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master
		 WHERE type = 'table' AND name = 'session_settings'`).Scan(&table); err != nil {
		t.Fatal(err)
	}
	if table != 0 {
		t.Fatal("session_settings still exists after the merge")
	}

	want := map[string]string{
		"s-full":  `{"think_level":"high","model":"anthropic/claude","mode":"yolo"}`,
		"s-think": `{"think_level":"low","mode":"workspace"}`,
		"s-model": `{"model":"openai/gpt","mode":"workspace"}`,
		// The document that was already there wins.
		"s-live": `{"mode":"read-only"}`,
	}
	for id, expected := range want {
		got, ok := settingsDocument(t, handle, id)
		if !ok {
			t.Errorf("%s: no settings document written", id)
			continue
		}
		if got != expected {
			t.Errorf("%s: document = %s, want %s", id, got, expected)
		}
	}
	if _, ok := settingsDocument(t, handle, "s-empty"); ok {
		t.Error("a row with no values left a document behind")
	}
}

// TestSessionSettingsMergeWithoutLegacyTable records the step in a
// database that never created session_settings, so the merge neither
// runs twice nor fails on a database the old build never touched.
func TestSessionSettingsMergeWithoutLegacyTable(t *testing.T) {
	ctx := context.Background()
	handle, err := db.Open(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = handle.Close() }()
	if _, err := handle.SQLDB().ExecContext(ctx, `
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			applied_at TEXT NOT NULL
		)`); err != nil {
		t.Fatal(err)
	}
	if err := mergeSessionSettings(ctx, handle); err != nil {
		t.Fatalf("merge without the legacy table: %v", err)
	}
	var applied int
	if err := handle.SQLDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`,
		mergeSessionSettingsVersion).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("step %d recorded %d times, want once",
			mergeSessionSettingsVersion, applied)
	}
}
