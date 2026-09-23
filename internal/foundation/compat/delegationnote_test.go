package compat

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// delegationNotePayloadWire is the stored form of a note's fields: the
// JSON object the live writer (capabilities/subagents.NotePayload) and
// this layer's backfill both produce. The same literal is pinned from
// the other side in subagents' TestNotePayloadWireShape, so the two
// encoders — one written by a build that had the kind column, one
// reconstructing it from prose — cannot drift apart without a failing
// test on one of the two paths.
const delegationNotePayloadWire = `{"target":"researcher","status":"succeeded",` +
	`"card_id":"card-1","run_id":"run-child","parent_run_id":"run-parent",` +
	`"body":"the report"}`

// delegationNoteProse is that same note as builds before this step
// rendered it: the header, the reference line, a blank line, then the
// quoted answer.
const delegationNoteProse = `[delegated worker "researcher" finished: succeeded]
(card card-1, run run-child, asked by run run-parent)

the report`

// archiveMessageJSON wraps text in the shape archive_messages stores.
func archiveMessageJSON(text string) string {
	raw, err := json.Marshal(map[string]any{
		"parts": []map[string]any{{"type": "text", "text": text}},
	})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// seedPreDelegationNoteWorkspace opens a workspace database migrated up
// to the step before 018 and seeds the rows the tests below read.
func seedPreDelegationNoteWorkspace(t *testing.T) *db.DB {
	t.Helper()
	ctx := context.Background()
	handle, err := db.Open(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })

	all := workspaceMigrations()
	pre := make([]db.Migration, 0, len(all))
	for _, m := range all {
		if m.Version < delegationNoteVersion {
			pre = append(pre, m)
		}
	}
	if err := handle.Migrate(ctx, pre); err != nil {
		t.Fatalf("apply pre-018 migrations: %v", err)
	}
	for _, stmt := range []string{
		`INSERT INTO conversations (id, created_at, updated_at) VALUES
			('s-1', '2026-09-01T10:00:00Z', '2026-09-01T10:00:00Z')`,
		// Turn 1 is a note the old build routed home.
		`INSERT INTO archive_turns (conversation_id, seq, run_id, at) VALUES
			('s-1', 1, 'subagent:card-1', '2026-09-01T10:00:00Z')`,
		// Turn 2 is the user speaking: same words, no subagent run id.
		`INSERT INTO archive_turns (conversation_id, seq, run_id, at) VALUES
			('s-1', 2, 'run-2', '2026-09-01T10:01:00Z')`,
		// Turn 3 is a subagent run whose text is not a note.
		`INSERT INTO archive_turns (conversation_id, seq, run_id, at) VALUES
			('s-1', 3, 'subagent:card-3', '2026-09-01T10:02:00Z')`,
		// Turn 4 is a note-shaped message that is not the turn's first.
		`INSERT INTO archive_turns (conversation_id, seq, run_id, at) VALUES
			('s-1', 4, 'subagent:card-4', '2026-09-01T10:03:00Z')`,
	} {
		if _, err := handle.SQLDB().ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed pre-018 rows: %v", err)
		}
	}
	seq := 0
	message := func(turnID int64, role, text string) {
		seq++
		if _, err := handle.SQLDB().ExecContext(ctx, `
			INSERT INTO archive_messages
				(conversation_id, turn_id, seq, role, content_json, created_at)
			VALUES ('s-1', ?, ?, ?, ?, '2026-09-01T10:00:00Z')`,
			turnID, seq, role, archiveMessageJSON(text)); err != nil {
			t.Fatalf("seed archive message: %v", err)
		}
	}
	message(1, "user", delegationNoteProse)
	message(2, "user", delegationNoteProse)
	message(3, "user", `[delegated worker "researcher" finished: nonsense]`)
	message(4, "user", "what the user asked")
	message(4, "user", delegationNoteProse)
	return handle
}

// TestDelegationNoteBackfillFilesOldNotes is the migration's happy path:
// the note turn gains its author and its fields, decoded from the prose,
// and every row that only looks like a note keeps its empty kind.
func TestDelegationNoteBackfillFilesOldNotes(t *testing.T) {
	ctx := context.Background()
	handle := seedPreDelegationNoteWorkspace(t)
	if err := WorkspaceSchema(ctx, handle); err != nil {
		t.Fatalf("apply schema including 018: %v", err)
	}
	// A second run must be a no-op, not a re-scan that rewrites rows.
	if err := WorkspaceSchema(ctx, handle); err != nil {
		t.Fatalf("schema must be idempotent: %v", err)
	}
	var applied int
	if err := handle.SQLDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`,
		delegationNoteVersion).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("step %d recorded %d times, want once",
			delegationNoteVersion, applied)
	}

	want := map[int64]struct {
		kind    string
		payload string
	}{
		1: {kind: delegationNoteKind, payload: delegationNotePayloadWire},
		2: {},
		3: {},
		4: {},
	}
	for seq, expected := range want {
		var kind, payload string
		if err := handle.SQLDB().QueryRowContext(ctx, `
			SELECT kind, payload_json FROM archive_turns
			WHERE conversation_id = 's-1' AND seq = ?`, seq,
		).Scan(&kind, &payload); err != nil {
			t.Fatalf("read turn %d: %v", seq, err)
		}
		if kind != expected.kind || payload != expected.payload {
			t.Fatalf("turn %d kind/payload = %q/%q, want %q/%q",
				seq, kind, payload, expected.kind, expected.payload)
		}
	}
}

// TestDelegationNoteBackfillParsesRenderedNotes pins the reader against
// every shape the old writer could produce: the reference line is
// optional, the body may be missing, and a failure quotes the error
// instead of an answer.
func TestDelegationNoteBackfillParsesRenderedNotes(t *testing.T) {
	header := func(target, status string) string {
		return fmt.Sprintf("[delegated worker %q finished: %s]", target, status)
	}
	cases := []struct {
		name string
		text string
		want string
	}{
		{
			name: "full",
			text: delegationNoteProse,
			want: delegationNotePayloadWire,
		},
		{
			name: "no reference line",
			text: header("researcher", "succeeded") + "\n\njust the answer",
			want: `{"target":"researcher","status":"succeeded",` +
				`"body":"just the answer"}`,
		},
		{
			name: "no body",
			text: header("researcher", "canceled") + "\n(card card-9)",
			want: `{"target":"researcher","status":"canceled",` +
				`"card_id":"card-9"}`,
		},
		{
			name: "failed",
			text: header("builder", "failed") + "\n\nboom",
			want: `{"target":"builder","status":"failed","body":"boom"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload, ok := parseDelegationNoteText(tc.text)
			if !ok {
				t.Fatalf("note not recognized: %q", tc.text)
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != tc.want {
				t.Fatalf("payload = %s, want %s", raw, tc.want)
			}
		})
	}

	// Everything that is not exactly the note's shape stays unfiled: a
	// status the writer never used, prose that merely quotes the
	// header, and a message that is not a note at all.
	for _, text := range []string{
		header("researcher", "accepted"),
		"sure, the worker finished: succeeded",
		"",
		header("researcher", "succeeded") + " trailing text on the header line",
	} {
		if payload, ok := parseDelegationNoteText(text); ok {
			t.Fatalf("recognized %q as a note: %+v", text, payload)
		}
	}
}
