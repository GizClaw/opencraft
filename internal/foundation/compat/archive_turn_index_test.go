package compat_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions/state"
	"github.com/GizClaw/opencraft/internal/foundation/compat"
	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// Migration 023 gives the turn-scoped message read its own index. The
// query is per-turn (ArchiveTurnByRun runs on every turn_end and again
// during recovery), so without the index it scans the conversation's
// whole message history each time.
func TestArchiveMessagesHaveATurnIndex(t *testing.T) {
	ctx := context.Background()
	ws, err := db.OpenWithOptions(
		filepath.Join(t.TempDir(), "session.db"),
		db.OpenOptions{ForeignKeys: false})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ws.Close() }()
	if err := compat.Workspace(ctx, ws, filepath.Join(t.TempDir(), "sessions"),
		state.Importer(ws)); err != nil {
		t.Fatal(err)
	}

	var name string
	if err := ws.SQLDB().QueryRowContext(ctx, `
		SELECT name FROM sqlite_master
		WHERE type = 'index' AND name = 'idx_archive_messages_turn'`,
	).Scan(&name); err != nil {
		t.Fatalf("idx_archive_messages_turn missing after migrations: %v", err)
	}

	rows, err := ws.SQLDB().QueryContext(ctx, `
		EXPLAIN QUERY PLAN
		SELECT id FROM archive_messages WHERE turn_id = ? ORDER BY seq`, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plan []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "idx_archive_messages_turn") {
		t.Fatalf("turn-scoped read does not use the new index:\n%s", joined)
	}
}
