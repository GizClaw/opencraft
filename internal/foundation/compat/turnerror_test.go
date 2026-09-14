package compat

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// TestTurnErrorBackfillClassifiesStoredProse pins the one-time
// classification of rows written before the columns existed: the two
// enums come from the rendered error text, and rows the rules cannot
// classify keep their empty class.
func TestTurnErrorBackfillClassifiesStoredProse(t *testing.T) {
	ctx := context.Background()
	handle, err := db.OpenWithOptions(
		filepath.Join(t.TempDir(), "session.db"),
		db.OpenOptions{ForeignKeys: false})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = handle.Close() }()
	if err := WorkspaceSchema(ctx, handle); err != nil {
		t.Fatal(err)
	}
	// The schema now carries the columns; rewind the step so the
	// backfill runs against them.
	if _, err := handle.SQLDB().ExecContext(ctx,
		`DELETE FROM schema_migrations WHERE version = ?`, turnErrorVersion,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.SQLDB().ExecContext(ctx, `
		INSERT INTO conversations(id, title, created_at, updated_at,
			turn_count, message_count, usage_json)
		VALUES ('s-1', 't', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z',
			0, 0, '{}')`); err != nil {
		t.Fatal(err)
	}
	for i, row := range []struct {
		runID     string
		status    string
		errText   string
		wantCause string
		wantKind  string
	}{
		{
			runID: "run-cancel", status: "interrupted",
			errText:   "engine: interrupted (user_cancel): user stopped the turn",
			wantCause: "user_cancel",
		},
		{
			runID: "run-shutdown", status: "interrupted",
			errText:   "graph \"assistant\" node \"llm\": engine: interrupted (host_shutdown)",
			wantCause: "host_shutdown",
		},
		{
			runID: "run-provider", status: "failed",
			// The detail suffix is real archive data the old renderer
			// could not classify.
			errText:  "graph \"assistant\" node \"llm\": invalid_provider_response during generate: stream.finish.validation",
			wantKind: "invalid_provider_response",
		},
		{
			runID: "run-provider-bare", status: "failed",
			errText:  "provider_failure during generate",
			wantKind: "provider_failure",
		},
		{
			runID: "run-unknown-kind", status: "failed",
			errText: "graph \"assistant\" node \"compact\": missing variable \"x\" on board",
			// A node wiring failure carries no class: the UI falls back
			// to its generic copy, exactly as before the columns.
			wantCause: "",
			wantKind:  "",
		},
		{
			runID: "run-ok", status: "completed",
		},
	} {
		if _, err := handle.SQLDB().ExecContext(ctx, `
			INSERT INTO archive_turns(
				conversation_id, seq, run_id, at, requested_at,
				started_at, finished_at, status, error,
				request_id, response_id, artifacts_json)
			VALUES ('s-1', ?, ?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z',
				'2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', ?, ?,
				'', '', '[]')`,
			i+1, row.runID, row.status, row.errText); err != nil {
			t.Fatal(err)
		}
	}

	if err := upgradeTurnErrorClassification(ctx, handle); err != nil {
		t.Fatalf("upgrade: %v", err)
	}

	rows, err := handle.SQLDB().QueryContext(ctx, `
		SELECT run_id, interrupt_cause, error_kind FROM archive_turns
		ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	got := map[string][2]string{}
	for rows.Next() {
		var runID, cause, kind string
		if err := rows.Scan(&runID, &cause, &kind); err != nil {
			t.Fatal(err)
		}
		got[runID] = [2]string{cause, kind}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := map[string][2]string{
		"run-cancel":        {"user_cancel", ""},
		"run-shutdown":      {"host_shutdown", ""},
		"run-provider":      {"", "invalid_provider_response"},
		"run-provider-bare": {"", "provider_failure"},
		"run-unknown-kind":  {"", ""},
		"run-ok":            {"", ""},
	}
	for runID, w := range want {
		if got[runID] != w {
			t.Errorf("%s: class = %v, want %v", runID, got[runID], w)
		}
	}

	// The step is recorded, so a second run is a no-op and cannot
	// re-classify rows a later version wrote.
	var applied int
	if err := handle.SQLDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`,
		turnErrorVersion).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("version rows = %d, want 1", applied)
	}
}
