package compat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// A finished delegation is routed home as an archived turn the app
// wrote. Where that turn existed only as its rendered prose, it now
// carries its author and its fields: archive_turns.kind names the
// writer and archive_turns.payload_json holds the writer's own
// structured record, so a reader (a title, the transcript) reads a
// column instead of parsing a sentence the app may reword tomorrow.
//
// This step adds both columns and files the notes previous builds
// archived as plain user text. The parsing below is the one-off cost of
// not having had a kind; the live writer (capabilities/subagents, via
// orchestration/host) writes the same object directly.
const (
	delegationNoteVersion = 18
	delegationNoteName    = "018_delegation_note_kind"
)

// delegationNoteKind is the turn kind a delegation note is filed under.
// It must equal subagents.KindDelegationNote; the two are pinned by
// TestDelegationNoteBackfillMatchesLiveShape, which compares this
// file's output against the live writer's.
const delegationNoteKind = "delegation_note"

// delegationNotePayload is the structured record the current build
// stores for a note — the same JSON object as subagents.NotePayload.
//
// The shape is restated here rather than imported because the layer
// order is fixed: the capability that owns the type imports
// foundation/config, which imports this package, so this package cannot
// import it back. The keys are a stored contract in their own right
// (rows written by this step and by the live writer must decode
// identically), which is what the shape test checks byte for byte.
type delegationNotePayload struct {
	Target      string `json:"target"`
	Status      string `json:"status"`
	CardID      string `json:"card_id,omitempty"`
	RunID       string `json:"run_id,omitempty"`
	ParentRunID string `json:"parent_run_id,omitempty"`
	Body        string `json:"body,omitempty"`
}

var (
	// delegationNoteHeaderPattern reads the note's first line as the
	// old writer rendered it: the target Go-quoted, the status bare.
	delegationNoteHeaderPattern = regexp.MustCompile(
		`^\[delegated worker (".*") finished: ([a-z_]+)\]$`)
	// delegationNoteReferencePattern matches the optional reference
	// line naming card and runs.
	delegationNoteReferencePattern = regexp.MustCompile(`^\((.*)\)$`)
)

// terminalNoteStatuses is the vocabulary the note was only ever written
// under. A header naming anything else is not a note this step can file.
var terminalNoteStatuses = map[string]bool{
	"succeeded": true,
	"failed":    true,
	"canceled":  true,
}

// upgradeDelegationNotes adds the author columns to archive_turns and
// files the notes archived before they existed.
func upgradeDelegationNotes(ctx context.Context, handle *db.DB) error {
	if handle == nil {
		return fmt.Errorf("compat: nil workspace database")
	}
	conn := handle.SQLDB()
	var applied int
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`,
		delegationNoteVersion).Scan(&applied); err != nil {
		return fmt.Errorf("compat: check delegation note kind: %w", err)
	}
	if applied > 0 {
		return nil
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("compat: begin delegation note kind: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil &&
			!errors.Is(err, sql.ErrTxDone) {
			telemetry.WarnErr(ctx,
				"compat: rollback delegation note kind failed", err)
		}
	}()
	for _, col := range []struct {
		name string
		decl string
	}{
		{name: "kind", decl: "TEXT NOT NULL DEFAULT ''"},
		{name: "payload_json", decl: "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := ensureColumn(
			ctx, tx, "archive_turns", col.name, col.decl,
		); err != nil {
			return err
		}
	}
	if err := backfillDelegationNotes(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, name, applied_at)
		 VALUES (?, ?, datetime('now'))`,
		delegationNoteVersion, delegationNoteName); err != nil {
		return fmt.Errorf("compat: record delegation note kind: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("compat: commit delegation note kind: %w", err)
	}
	return nil
}

// backfillDelegationNotes marks every archived turn whose first message
// is a note. The note's run key ("subagent:<card>") narrows the scan to
// the rows its writer produced, and the rendered text then has to match
// the note's exact shape: a hand-written message that merely mentions a
// worker, or a note a future build words differently, keeps its empty
// kind and renders as the row it always was.
func backfillDelegationNotes(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT t.id, m.content_json
		FROM archive_turns t
		JOIN archive_messages m ON m.turn_id = t.id
		WHERE t.kind = ''
			AND t.run_id LIKE 'subagent:%'
			AND m.role = 'user'
			AND m.seq = (
				SELECT MIN(m2.seq) FROM archive_messages m2
				WHERE m2.turn_id = t.id
			)`)
	if err != nil {
		return fmt.Errorf("compat: list delegation notes to file: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "compat: close delegation note rows failed",
			rows.Close())
	}()
	type filed struct {
		id      int64
		payload []byte
	}
	var updates []filed
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return fmt.Errorf("compat: scan delegation note: %w", err)
		}
		var content message.Content
		if err := json.Unmarshal([]byte(raw), &content); err != nil {
			// A message this step cannot decode keeps its empty kind:
			// the text is still there, and the transcript renders it
			// as the row it was.
			continue
		}
		payload, ok := parseDelegationNoteText(content.Text())
		if !ok {
			continue
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf(
				"compat: encode delegation note payload: %w", err)
		}
		updates = append(updates, filed{id: id, payload: encoded})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("compat: list delegation notes to file: %w", err)
	}
	for _, u := range updates {
		if _, err := tx.ExecContext(ctx, `
			UPDATE archive_turns
			SET kind = ?, payload_json = ?
			WHERE id = ?`,
			delegationNoteKind, string(u.payload), u.id); err != nil {
			return fmt.Errorf("compat: file delegation note turn %d: %w",
				u.id, err)
		}
	}
	return nil
}

// parseDelegationNoteText reads one rendered note back into its fields.
// It reports false for anything that is not exactly the note's shape,
// so the classifier cannot invent a note out of prose that resembles
// one.
func parseDelegationNoteText(text string) (delegationNotePayload, bool) {
	header, rest, _ := strings.Cut(text, "\n")
	m := delegationNoteHeaderPattern.FindStringSubmatch(header)
	if m == nil || !terminalNoteStatuses[m[2]] {
		return delegationNotePayload{}, false
	}
	target, err := strconv.Unquote(m[1])
	if err != nil {
		// The writer quoted the target with %q; an unreadable quote
		// still names the worker once the quotes come off.
		target = strings.Trim(m[1], `"`)
	}
	payload := delegationNotePayload{Target: target, Status: m[2]}
	if refLine, after, _ := strings.Cut(rest, "\n"); refLine != "" {
		if ref := delegationNoteReferencePattern.FindStringSubmatch(refLine); ref != nil {
			parseDelegationNoteReference(ref[1], &payload)
			rest = after
		}
	}
	// The writer separated the body with one blank line; everything
	// after it is the quoted answer (or failure), byte for byte.
	payload.Body = strings.TrimPrefix(rest, "\n")
	return payload, true
}

// parseDelegationNoteReference reads the reference line's parts into
// the payload. The writer joined them with ", " and named each part.
func parseDelegationNoteReference(text string, payload *delegationNotePayload) {
	for _, part := range strings.Split(text, ", ") {
		switch {
		case strings.HasPrefix(part, "card "):
			payload.CardID = strings.TrimPrefix(part, "card ")
		case strings.HasPrefix(part, "asked by run "):
			payload.ParentRunID = strings.TrimPrefix(part, "asked by run ")
		case strings.HasPrefix(part, "run "):
			payload.RunID = strings.TrimPrefix(part, "run ")
		}
	}
}
