package migrations

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// TestMemoryPayloadMigration011 verifies the text-only memory payloads
// written before migration 011 are rewritten to canonical content
// parts, that legacy tool rows degrade to user role (their call ids
// were never stored), that canonical rows are left untouched, and that
// a canonical tool row with a text preamble before its tool_result keeps
// the tool role (only rows without any tool_result part are demoted).
func TestMemoryPayloadMigration011(t *testing.T) {
	ctx := context.Background()
	handle, err := db.Open(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = handle.Close() }()

	all := workspaceMigrations()
	// Reproduce a pre-011 database: schema only up to migration 010.
	pre011 := make([]db.Migration, 0, len(all))
	for _, m := range all {
		if m.Version <= 10 {
			pre011 = append(pre011, m)
		}
	}
	if err := handle.Migrate(ctx, pre011); err != nil {
		t.Fatalf("apply pre-011 migrations: %v", err)
	}
	for _, stmt := range []string{
		`INSERT INTO memory_items (
			id, thread_id, turn_id, seq, item_type, role, payload, created_at
		) VALUES
			('legacy-user', 's-1', 't-1', 0, 'text', 'user',
			 '{"text":"hello"}', '2026-09-01T10:00:00Z'),
			('legacy-tool', 's-1', 't-1', 1, 'text', 'tool',
			 '{"text":"tool_result: ok"}', '2026-09-01T10:00:00Z'),
			('canonical-tool', 's-1', 't-1', 2, 'text', 'tool',
			 '{"parts":[{"type":"tool_result","result":{"call_id":"c1","content":"ok"}}]}',
			 '2026-09-01T10:00:00Z'),
			('canonical-text-first', 's-1', 't-1', 3, 'text', 'tool',
			 '{"parts":[{"type":"text","text":"preface"},{"type":"tool_result","result":{"call_id":"c2","content":"ok"}}]}',
			 '2026-09-01T10:00:00Z'),
			('canonical-text-only', 's-1', 't-1', 4, 'text', 'tool',
			 '{"parts":[{"type":"text","text":"tool_result: ok"}]}',
			 '2026-09-01T10:00:00Z')`,
	} {
		if _, err := handle.SQLDB().ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed legacy memory rows: %v", err)
		}
	}

	if err := handle.Migrate(ctx, all); err != nil {
		t.Fatalf("apply migration 011: %v", err)
	}
	if err := handle.Migrate(ctx, all); err != nil {
		t.Fatalf("migrations must be idempotent: %v", err)
	}

	rows, err := handle.SQLDB().QueryContext(ctx, `
		SELECT id, role, json_extract(payload, '$.parts[0].type'),
		       COALESCE(json_extract(payload, '$.parts[0].text'), ''),
		       COALESCE(json_extract(payload, '$.parts[0].result.call_id'), '')
		FROM memory_items ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()

	want := []struct {
		id, role, partType, text, callID string
	}{
		{"legacy-user", "user", "text", "hello", ""},
		{"legacy-tool", "user", "text", "tool_result: ok", ""},
		{"canonical-tool", "tool", "tool_result", "", "c1"},
		{"canonical-text-first", "tool", "text", "preface", ""},
		{"canonical-text-only", "user", "text", "tool_result: ok", ""},
	}
	for i := range want {
		if !rows.Next() {
			t.Fatalf("row %d missing after migration", i)
		}
		var id, role, partType, text, callID string
		if err := rows.Scan(&id, &role, &partType, &text, &callID); err != nil {
			t.Fatal(err)
		}
		if id != want[i].id {
			t.Fatalf("id = %q, want %q", id, want[i].id)
		}
		if role != want[i].role {
			t.Fatalf("%s role = %q, want %q", id, role, want[i].role)
		}
		if partType != want[i].partType {
			t.Fatalf("%s part type = %q, want %q", id, partType, want[i].partType)
		}
		if text != want[i].text {
			t.Fatalf("%s text = %q, want %q", id, text, want[i].text)
		}
		if callID != want[i].callID {
			t.Fatalf("%s call id = %q, want %q", id, callID, want[i].callID)
		}
	}
	if rows.Next() {
		t.Fatal("unexpected extra row after migration")
	}
}
