package migrations

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// TestTurnCorrelationIDsMigration013 verifies migration 013 adds the
// archive_turns request_id/response_id columns to a populated pre-013
// database, leaves existing rows with the empty default, and keeps the
// migration idempotent.
func TestTurnCorrelationIDsMigration013(t *testing.T) {
	ctx := context.Background()
	handle, err := db.Open(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = handle.Close() }()

	all := workspaceMigrations()
	pre013 := make([]db.Migration, 0, len(all))
	for _, m := range all {
		if m.Version <= 12 {
			pre013 = append(pre013, m)
		}
	}
	if err := handle.Migrate(ctx, pre013); err != nil {
		t.Fatalf("apply pre-013 migrations: %v", err)
	}
	for _, stmt := range []string{
		`INSERT INTO conversations (id, created_at, updated_at) VALUES
			('s-1', '2026-09-01T10:00:00Z', '2026-09-01T10:00:00Z')`,
		`INSERT INTO archive_turns (
			conversation_id, seq, run_id, at, status, error
		) VALUES ('s-1', 1, 'run-1', '2026-09-01T10:00:00Z', 'completed', '')`,
	} {
		if _, err := handle.SQLDB().ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed pre-013 rows: %v", err)
		}
	}

	if err := handle.Migrate(ctx, all); err != nil {
		t.Fatalf("apply migration 013: %v", err)
	}
	if err := handle.Migrate(ctx, all); err != nil {
		t.Fatalf("migrations must be idempotent: %v", err)
	}

	cols, err := handle.SQLDB().QueryContext(ctx,
		`PRAGMA table_info(archive_turns)`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cols.Close() }()
	newCols := map[string]bool{}
	for cols.Next() {
		var (
			cid     int
			name    string
			typ     string
			notNull int
			dflt    *string
			pk      int
		)
		if err := cols.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		if name == "request_id" || name == "response_id" {
			newCols[name] = true
		}
	}
	for _, name := range []string{"request_id", "response_id"} {
		if !newCols[name] {
			t.Fatalf("column %s missing after migration 013", name)
		}
	}

	var existingReq, existingResp string
	if err := handle.SQLDB().QueryRowContext(ctx,
		`SELECT request_id, response_id FROM archive_turns
		 WHERE conversation_id = 's-1' AND seq = 1`,
	).Scan(&existingReq, &existingResp); err != nil {
		t.Fatalf("read migrated row: %v", err)
	}
	if existingReq != "" || existingResp != "" {
		t.Fatalf("migrated row ids = %q/%q, want empty defaults",
			existingReq, existingResp)
	}

	if _, err := handle.SQLDB().ExecContext(ctx,
		`INSERT INTO archive_turns (
			conversation_id, seq, run_id, at,
			request_id, response_id
		) VALUES ('s-1', 2, 'run-2', '2026-09-01T10:00:01Z',
			'req-2', 'resp-2')`); err != nil {
		t.Fatalf("insert with correlation ids after migration: %v", err)
	}
	var newReq, newResp string
	if err := handle.SQLDB().QueryRowContext(ctx,
		`SELECT request_id, response_id FROM archive_turns
		 WHERE conversation_id = 's-1' AND seq = 2`,
	).Scan(&newReq, &newResp); err != nil {
		t.Fatal(err)
	}
	if newReq != "req-2" || newResp != "resp-2" {
		t.Fatalf("new row ids = %q/%q, want req-2/resp-2", newReq, newResp)
	}
}
