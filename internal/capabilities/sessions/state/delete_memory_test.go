package state_test

import (
	"context"
	"path/filepath"
	"testing"
)

// TestDeleteConversationRowsRemovesMemory verifies that deleting a
// conversation also removes the memory rows registered on the same
// workspace DB, so a deleted session cannot leave orphaned context.
func TestDeleteConversationRowsRemovesMemory(t *testing.T) {
	ctx := context.Background()
	s := openState(t, filepath.Join(t.TempDir(), "session.db"))
	sqlDB := s.Handle().SQLDB()
	if _, err := sqlDB.ExecContext(ctx, `
		INSERT INTO memory_items (
			id, thread_id, turn_id, seq, item_type, role, payload, created_at
		) VALUES (?, ?, ?, ?, 'text', 'user', ?, ?)`,
		"s-1:run-1:0", "s-1", "run-1", 0, `{"text":"hello"}`,
		"2026-09-04T00:00:00Z",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(ctx, `
		INSERT INTO summary_nodes (
			id, thread_id, level, parent_ids, source_ids, summary,
			created_at, updated_at, metadata
		) VALUES (?, ?, 0, '[]', '[]', ?, ?, ?, '{}')`,
		"node-1", "s-1", "folded text",
		"2026-09-04T00:00:00Z", "2026-09-04T00:00:00Z",
	); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteConversationRows(ctx, "s-1"); err != nil {
		t.Fatal(err)
	}

	var memCount, summaryCount int
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memory_items WHERE thread_id = ?`, "s-1",
	).Scan(&memCount); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM summary_nodes WHERE thread_id = ?`, "s-1",
	).Scan(&summaryCount); err != nil {
		t.Fatal(err)
	}
	if memCount != 0 || summaryCount != 0 {
		t.Fatalf("memory rows after delete: items=%d summaries=%d, want 0",
			memCount, summaryCount)
	}
}
