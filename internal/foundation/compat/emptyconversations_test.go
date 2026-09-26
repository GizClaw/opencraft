package compat

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// seedPurgeWorkspace opens a workspace database with every versioned
// migration applied — including the deleted_conversations table the
// purge retires ids into — and seeds one conversation per shape the
// purge has to tell apart.
func seedPurgeWorkspace(t *testing.T) *db.DB {
	t.Helper()
	ctx := context.Background()
	handle, err := db.Open(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := handle.Migrate(ctx, workspaceMigrations()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	// The step that drops summary_nodes' foreign key to the dropped
	// threads table (012). A database old enough to hold the rows this
	// purge looks for has run it, and the summary fixture below needs
	// the table to accept a row.
	if err := cleanupSummaryNodesFK(ctx, handle); err != nil {
		t.Fatalf("drop summary foreign key: %v", err)
	}
	for _, stmt := range []string{
		// The shape to remove: zero turns, no title anywhere, and the
		// usage block only the old write-back put there. It is the
		// "(empty)" entry the sidebar kept listing.
		`INSERT INTO conversations
			(id, title, created_at, updated_at, turn_count, message_count, usage_json)
		 VALUES ('s-resurrected', '', '2026-09-01T00:00:00Z', '2026-09-01T00:00:00Z',
			0, 0, '{"total_tokens":911}')`,
		`INSERT INTO conversation_state
			(conversation_id, name, value_json, updated_at)
		 VALUES ('s-resurrected', 'plan', '{}', '2026-09-01T00:00:00Z')`,
		`INSERT INTO summary_nodes
			(id, thread_id, level, parent_ids, source_ids, summary,
			 created_at, updated_at, metadata)
		 VALUES ('node-1', 's-resurrected', 0, '[]', '[]', 'stale summary',
			'2026-09-01T00:00:00Z', '2026-09-01T00:00:00Z', '{}')`,
		// Zero turns and usage, but the row carries a title: a start
		// that seeded one, which a run could still be writing to.
		`INSERT INTO conversations
			(id, title, created_at, updated_at, turn_count, message_count, usage_json)
		 VALUES ('s-titled', 'kept', '2026-09-01T00:00:00Z', '2026-09-01T00:00:00Z',
			0, 0, '{"total_tokens":12}')`,
		// No title on the row, but the user renamed it: the override
		// lives in the state document.
		`INSERT INTO conversations
			(id, title, created_at, updated_at, turn_count, message_count, usage_json)
		 VALUES ('s-renamed', '', '2026-09-01T00:00:00Z', '2026-09-01T00:00:00Z',
			0, 0, '{"total_tokens":13}')`,
		`INSERT INTO conversation_state
			(conversation_id, name, value_json, updated_at)
		 VALUES ('s-renamed', 'title', '"my chat"', '2026-09-01T00:00:00Z')`,
		// Zero turns, no counters: the draft the usage shape shares its
		// silence with.
		`INSERT INTO conversations
			(id, title, created_at, updated_at, turn_count, message_count, usage_json)
		 VALUES ('s-draft', '', '2026-09-01T00:00:00Z', '2026-09-01T00:00:00Z',
			0, 0, '{}')`,
		// An imported conversation, which is never the old write-back's
		// product: the import path seeds a title before it records
		// usage, and this one is complete.
		`INSERT INTO conversations
			(id, title, created_at, updated_at, turn_count, message_count,
			 usage_json, import_source, import_ready)
		 VALUES ('s-imported', '(imported)', '2026-09-01T00:00:00Z',
			'2026-09-01T00:00:00Z', 3, 6, '{"total_tokens":14}', 'codex/rollout/1', 1)`,
	} {
		if _, err := handle.SQLDB().ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed pre-022 rows: %v", err)
		}
	}
	return handle
}

// countRows reads one table's row count for one conversation.
func countRows(t *testing.T, handle *db.DB, query, id string) int {
	t.Helper()
	var n int
	if err := handle.SQLDB().QueryRowContext(context.Background(), query, id).
		Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

// TestWorkspaceSchemaPurgesConversationsAnOlderBuildResurrected is the
// upgrade's half of "a deleted conversation stays deleted": rows the
// old usage write-back left behind — zero turns, no title, usage and
// nothing else — are removed, and the ids are retired so the upgrade
// cannot be undone by a later write. The shapes the predicate must
// leave alone are seeded beside it.
func TestWorkspaceSchemaPurgesConversationsAnOlderBuildResurrected(t *testing.T) {
	ctx := context.Background()
	handle := seedPurgeWorkspace(t)
	if err := WorkspaceSchema(ctx, handle); err != nil {
		t.Fatalf("apply schema including 022: %v", err)
	}
	// A second run must be a no-op, not a re-purge.
	if err := WorkspaceSchema(ctx, handle); err != nil {
		t.Fatalf("schema must be idempotent: %v", err)
	}
	var applied int
	if err := handle.SQLDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`,
		purgeEmptyConversationsVersion).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("step %d recorded %d times, want once",
			purgeEmptyConversationsVersion, applied)
	}

	// Gone: the row, and everything that hung off it.
	for _, tc := range []struct{ name, query string }{
		{"conversation", `SELECT COUNT(*) FROM conversations WHERE id = ?`},
		{"state", `SELECT COUNT(*) FROM conversation_state WHERE conversation_id = ?`},
		{"summary", `SELECT COUNT(*) FROM summary_nodes WHERE thread_id = ?`},
	} {
		if n := countRows(t, handle, tc.query, "s-resurrected"); n != 0 {
			t.Errorf("s-resurrected %s rows after the purge = %d, want 0", tc.name, n)
		}
	}
	// Retired: the same predicate that makes the store refuse a late
	// write to a conversation the user deleted in this build.
	if n := countRows(t, handle,
		`SELECT COUNT(*) FROM deleted_conversations WHERE id = ?`,
		"s-resurrected"); n != 1 {
		t.Errorf("s-resurrected retirement rows = %d, want 1", n)
	}

	// Kept: every shape the purge must not mistake for the old
	// write-back's product.
	for _, id := range []string{"s-titled", "s-renamed", "s-draft", "s-imported"} {
		if n := countRows(t, handle,
			`SELECT COUNT(*) FROM conversations WHERE id = ?`, id); n != 1 {
			t.Errorf("conversation %s rows after the purge = %d, want 1", id, n)
		}
		if n := countRows(t, handle,
			`SELECT COUNT(*) FROM deleted_conversations WHERE id = ?`, id); n != 0 {
			t.Errorf("conversation %s was retired by the purge", id)
		}
	}
}
