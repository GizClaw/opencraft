package compat_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions/state"
	"github.com/GizClaw/opencraft/internal/foundation/compat"
	"github.com/GizClaw/opencraft/internal/foundation/db"
)

func TestWorkspaceAndUserMigrationSets(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "sessions")
	ws, err := db.OpenWithOptions(
		filepath.Join(t.TempDir(), "session.db"),
		db.OpenOptions{ForeignKeys: false})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ws.Close() }()
	if err := compat.Workspace(ctx, ws, root, state.Importer(ws)); err != nil {
		t.Fatal(err)
	}
	if err := compat.Workspace(ctx, ws, root, state.Importer(ws)); err != nil {
		t.Fatalf("workspace migrations must be idempotent: %v", err)
	}
	for _, table := range []string{
		"conversations", "archive_turns", "archive_messages",
		"conversation_state", "agent_checkpoints", "session_settings",
		"memory_items", "summary_nodes", "message_fts",
	} {
		var n int
		if err := ws.SQLDB().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master
			 WHERE type = 'table' AND name = ?`, table,
		).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("workspace table %s missing after migrations", table)
		}
	}

	user, err := db.Open(filepath.Join(t.TempDir(), "user.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = user.Close() }()
	if err := compat.User(ctx, user); err != nil {
		t.Fatal(err)
	}
	if err := compat.User(ctx, user); err != nil {
		t.Fatalf("user migrations must be idempotent: %v", err)
	}
	for _, table := range []string{"model_usage", "model_usage_hourly",
		"automations", "automation_runs", "user_memory", "skill_usage",
		"skill_state", "skill_archive", "review_suggestions"} {
		var n int
		if err := user.SQLDB().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master
			 WHERE type = 'table' AND name = ?`, table,
		).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("user table %s missing after migrations", table)
		}
	}
}
