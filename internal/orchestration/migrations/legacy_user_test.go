package migrations

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

func TestUserLegacyUpgradesOldAutomationTable(t *testing.T) {
	ctx := context.Background()
	handle, err := db.Open(filepath.Join(t.TempDir(), "user.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = handle.Close() }()

	// Simulate an automations table created before notify and
	// conversation_id existed, plus a weekly task saved before the
	// phase origin field was added.
	if _, err := handle.SQLDB().ExecContext(ctx, `
		CREATE TABLE automations (
			id          TEXT PRIMARY KEY,
			name        TEXT NOT NULL,
			prompt      TEXT NOT NULL,
			schedule    TEXT NOT NULL,
			workspace   TEXT NOT NULL,
			mode        TEXT NOT NULL DEFAULT 'workspace',
			model       TEXT NOT NULL DEFAULT '',
			think       TEXT NOT NULL DEFAULT '',
			enabled     INTEGER NOT NULL DEFAULT 1,
			created_at  TEXT NOT NULL,
			updated_at  TEXT NOT NULL,
			last_run_at TEXT NOT NULL DEFAULT '',
			last_status TEXT NOT NULL DEFAULT '',
			next_run_at TEXT NOT NULL DEFAULT ''
		)`); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.SQLDB().ExecContext(ctx, `
		INSERT INTO automations (
			id, name, prompt, schedule, workspace, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"t-legacy", "brief", "run", `{"type":"weekly","days":["MO"],"time":"09:00"}`,
		"/tmp/ws", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z",
	); err != nil {
		t.Fatal(err)
	}

	if err := User(ctx, handle); err != nil {
		t.Fatalf("User migration: %v", err)
	}

	var notify, conversationID string
	if err := handle.SQLDB().QueryRowContext(ctx,
		`SELECT notify, conversation_id FROM automations WHERE id = ?`,
		"t-legacy",
	).Scan(&notify, &conversationID); err != nil {
		t.Fatalf("read legacy row after migration: %v", err)
	}
	if notify != "always" || conversationID != "" {
		t.Fatalf("legacy columns = notify %q conversation %q", notify, conversationID)
	}

	var raw string
	if err := handle.SQLDB().QueryRowContext(ctx,
		`SELECT schedule FROM automations WHERE id = ?`, "t-legacy",
	).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var sched struct {
		Type   string `json:"type"`
		Origin string `json:"origin"`
	}
	if err := json.Unmarshal([]byte(raw), &sched); err != nil {
		t.Fatal(err)
	}
	if sched.Type != "weekly" || sched.Origin == "" {
		t.Fatalf("weekly origin not backfilled: %+v", sched)
	}
}
