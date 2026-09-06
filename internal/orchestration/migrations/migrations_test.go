package migrations

import (
	"context"
	"path/filepath"
	"testing"

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
	if err := Workspace(ctx, ws, root); err != nil {
		t.Fatal(err)
	}
	if err := Workspace(ctx, ws, root); err != nil {
		t.Fatalf("workspace migrations must be idempotent: %v", err)
	}
	for _, table := range []string{
		"conversations", "archive_turns", "archive_messages",
		"conversation_state", "agent_checkpoints", "session_settings",
		"memory_items", "summary_nodes",
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
	if err := User(ctx, user); err != nil {
		t.Fatal(err)
	}
	if err := User(ctx, user); err != nil {
		t.Fatalf("user migrations must be idempotent: %v", err)
	}
	for _, table := range []string{"model_usage", "model_usage_hourly",
		"automations", "automation_runs"} {
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

func TestUserUsageMigrationRebucketsByModelName(t *testing.T) {
	ctx := context.Background()
	handle, err := db.Open(filepath.Join(t.TempDir(), "user.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = handle.Close() }()

	all := userMigrations()
	// Reproduce a pre-rebucket database: only the two usage schema
	// steps applied, with rows keyed "provider/model".
	if err := handle.Migrate(ctx, all[:2]); err != nil {
		t.Fatalf("apply original usage migrations: %v", err)
	}
	for _, stmt := range []string{
		`INSERT INTO model_usage (
			workspace_id, session_id, model,
			input_tokens, output_tokens, cache_read_tokens,
			reasoning_tokens, latency_ms, updated_at
		) VALUES
			('ws-a', 's-1', 'openai/gpt-test', 100, 20, 5, 2, 0, '2026-09-01T10:00:00Z'),
			('ws-a', 's-1', 'azure/gpt-test', 50, 10, 0, 5, 0, '2026-09-02T11:00:00Z')`,
		`INSERT INTO model_usage_hourly (
			model, hour,
			input_tokens, output_tokens, cache_read_tokens,
			reasoning_tokens, latency_ms
		) VALUES
			('openai/gpt-test', '2026-09-01T10:00:00Z', 100, 20, 5, 2, 0),
			('azure/gpt-test', '2026-09-01T10:00:00Z', 50, 10, 0, 5, 0)`,
	} {
		if _, err := handle.SQLDB().ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed legacy usage: %v", err)
		}
	}

	if err := handle.Migrate(ctx, all); err != nil {
		t.Fatalf("apply rebucket migration: %v", err)
	}
	if err := handle.Migrate(ctx, all); err != nil {
		t.Fatalf("user migrations must be idempotent: %v", err)
	}

	var model string
	var total, input, output, cacheRead, cacheWrite, reasoning, calls int64
	err = handle.SQLDB().QueryRowContext(ctx, `
		SELECT model, total_tokens, input_tokens, output_tokens,
		       cache_read_tokens, cache_write_tokens, reasoning_tokens, calls
		FROM model_usage WHERE workspace_id = 'ws-a' AND session_id = 's-1'`,
	).Scan(&model, &total, &input, &output, &cacheRead, &cacheWrite,
		&reasoning, &calls)
	if err != nil {
		t.Fatalf("read rebucketed model_usage: %v", err)
	}
	if model != "gpt-test" || total != 180 || input != 150 || output != 30 ||
		cacheRead != 5 || cacheWrite != 0 || reasoning != 7 || calls != 0 {
		t.Fatalf("model_usage after rebucket = %q %d total %d/%d read %d write %d reason %d calls %d",
			model, total, input, output, cacheRead, cacheWrite, reasoning, calls)
	}

	var hourlyInput, hourlyOutput, hourlyWrite int64
	err = handle.SQLDB().QueryRowContext(ctx, `
		SELECT input_tokens, output_tokens, cache_write_tokens
		FROM model_usage_hourly
		WHERE model = 'gpt-test' AND hour = '2026-09-01T10:00:00Z'`,
	).Scan(&hourlyInput, &hourlyOutput, &hourlyWrite)
	if err != nil {
		t.Fatalf("read rebucketed model_usage_hourly: %v", err)
	}
	if hourlyInput != 150 || hourlyOutput != 30 || hourlyWrite != 0 {
		t.Fatalf("model_usage_hourly after rebucket = %d/%d write %d",
			hourlyInput, hourlyOutput, hourlyWrite)
	}

	var legacy int
	if err := handle.SQLDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM model_usage WHERE model LIKE '%/%'`,
	).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if legacy != 0 {
		t.Fatalf("provider-prefixed rows remain after rebucket: %d", legacy)
	}
}
