package compat

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// TestPromptProjectionBackfillFillsLegacyRows covers the upgrade path of
// migration 026: a row written before the column existed carries no prompt
// form, so the reader projects it the long way (decode, clone, render). The
// backfill derives it once and stores it, which is what makes an existing
// workspace as cheap to replay as a fresh one.
func TestPromptProjectionBackfillFillsLegacyRows(t *testing.T) {
	ctx := context.Background()
	handle, err := db.Open(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := handle.Migrate(ctx, workspaceMigrations()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	// The step ran on the empty database; forget that so the rows below are
	// seen as legacy ones.
	if _, err := handle.SQLDB().ExecContext(ctx,
		`DELETE FROM schema_migrations WHERE version = ?`,
		promptProjectionVersion); err != nil {
		t.Fatal(err)
	}
	if err := handle.SetForeignKeys(false); err != nil {
		t.Fatal(err)
	}

	call := message.Content{Parts: []message.Part{
		message.ToolCallPart{Call: message.ToolCall{
			ID: "c1", Name: "exec_command",
			Arguments: json.RawMessage(`{"cmd":"go test ./..."}`),
		}},
	}}
	callJSON, err := json.Marshal(call)
	if err != nil {
		t.Fatal(err)
	}
	textJSON, err := json.Marshal(message.NewTextContent("hello"))
	if err != nil {
		t.Fatal(err)
	}
	for i, tc := range []struct {
		role    string
		content []byte
	}{
		{"user", textJSON},
		{"assistant", callJSON},
	} {
		if _, err := handle.SQLDB().ExecContext(ctx, `
			INSERT INTO archive_messages(
				conversation_id, turn_id, seq, role, content_json, created_at)
			VALUES ('s-1', 't-1', ?, ?, ?, '2026-10-27T00:00:00Z')`,
			i, tc.role, string(tc.content)); err != nil {
			t.Fatalf("insert legacy row: %v", err)
		}
	}

	if err := backfillPromptProjection(ctx, handle); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	rows, err := handle.SQLDB().QueryContext(ctx, `
		SELECT seq, prompt_json, prompt_state FROM archive_messages ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var states, prompts []string
	for rows.Next() {
		var seq int64
		var prompt, st string
		if err := rows.Scan(&seq, &prompt, &st); err != nil {
			t.Fatal(err)
		}
		states = append(states, st)
		prompts = append(prompts, prompt)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(states) != 2 {
		t.Fatalf("rows = %d, want 2", len(states))
	}
	for i, st := range states {
		if st != "ok" {
			t.Errorf("row %d prompt_state = %q, want ok", i, st)
		}
		if prompts[i] == "" {
			t.Errorf("row %d has no prompt projection", i)
		}
	}
	var projected message.Message
	if err := json.Unmarshal([]byte(prompts[0]), &projected); err != nil {
		t.Fatalf("decode stored projection: %v", err)
	}
	if got := projected.Content.Text(); got != "hello" {
		t.Errorf("stored projection text = %q, want hello", got)
	}
	// The tool call's rendering is the point of storing the projection: it is
	// the part the reader would otherwise recompute for every row.
	if !strings.Contains(prompts[1], "tool_call: exec_command") {
		t.Errorf("stored projection lost the tool activity: %q", prompts[1])
	}

	// Idempotent: a second run is recorded and does nothing.
	if err := backfillPromptProjection(ctx, handle); err != nil {
		t.Fatalf("second backfill: %v", err)
	}
}
