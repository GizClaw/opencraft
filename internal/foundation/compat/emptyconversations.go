package compat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// A usage write used to recreate the conversation row it was writing to
// when the read missed (see sessions.writeUsage for the fix), so a chat
// the user had already deleted could come back as a row the sidebar
// lists: zero turns, no title, and a usage_json that is not empty — the
// `(empty)` entry whose only remaining data was a token count. This step
// removes the rows that shape still describes, so an upgrade does not
// ask the user to delete their deleted conversations a second time.
//
// The shape is exact, not a heuristic: turn_count = 0 rules out a
// conversation with an archive (and therefore one that a running turn
// could still be writing, since the row is seeded with a title at run
// start); an empty title rules out a start that seeded one; non-empty
// usage rules out the drafts and interrupted imports that share the
// zero-turn shape but carry no counters; and the absence of a `title`
// state document rules out a conversation the user renamed through the
// row. What remains can only have been produced by the old write-back.
//
// The step is Go rather than a SQL file for the two usual reasons: it
// reports before it deletes — counts and conversation ids, so the trace
// of the old bug survives in telemetry — and a database that already ran
// it is recorded once, in schema_migrations.
const (
	purgeEmptyConversationsVersion = 22
	purgeEmptyConversationsName    = "022_purge_empty_conversations"
)

// purgeSample bounds the ids named in the report. A disagreement is
// either in every conversation (the count shows it) or in a handful.
const purgeSample = 20

func purgeEmptyConversations(ctx context.Context, handle *db.DB) error {
	conn := handle.SQLDB()
	var applied int
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`,
		purgeEmptyConversationsVersion).Scan(&applied); err != nil {
		return fmt.Errorf("compat: check empty conversation purge: %w", err)
	}
	if applied > 0 {
		return nil
	}
	ids, err := emptyConversationIDs(ctx, conn)
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		telemetry.Info(ctx,
			"compat: removing conversations a late usage write resurrected",
			otellog.Int("conversations", len(ids)),
			otellog.String("sample", strings.Join(sampleIDs(ids), ", ")))
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("compat: begin empty conversation purge: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			telemetry.WarnErr(ctx,
				"compat: rollback empty conversation purge failed", err)
		}
	}()
	for _, id := range ids {
		// The rows go the way a delete takes them, and the id is
		// retired in the same transaction: the user deleted this
		// conversation once, and the upgrade must not quietly make it
		// possible to bring back.
		for _, target := range []struct{ table, column string }{
			{"archive_messages", "conversation_id"},
			{"archive_turns", "conversation_id"},
			{"conversation_state", "conversation_id"},
			{"summary_nodes", "thread_id"},
			{"message_fts", "conversation_id"},
			{"conversations", "id"},
		} {
			if err := execIfPresent(
				ctx, tx, target.table,
				"DELETE FROM "+target.table+" WHERE "+target.column+" = ?",
				id,
			); err != nil {
				return fmt.Errorf("compat: purge conversation %s: %w", id, err)
			}
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO deleted_conversations(id, deleted_at)
			VALUES (?, ?)
			ON CONFLICT(id) DO NOTHING`,
			id, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("compat: retire purged conversation %s: %w", id, err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, name, applied_at)
		 VALUES (?, ?, datetime('now'))`,
		purgeEmptyConversationsVersion, purgeEmptyConversationsName); err != nil {
		return fmt.Errorf("compat: record empty conversation purge: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("compat: commit empty conversation purge: %w", err)
	}
	return nil
}

// emptyConversationIDs returns the ids of the rows the old usage
// write-back produced (see the step's comment for why this predicate is
// exact).
func emptyConversationIDs(
	ctx context.Context, conn *sql.DB,
) ([]string, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT c.id FROM conversations c
		WHERE c.turn_count = 0
		  AND c.message_count = 0
		  AND trim(c.title) = ''
		  AND c.usage_json IS NOT NULL
		  AND c.usage_json <> ''
		  AND c.usage_json <> '{}'
		  AND NOT EXISTS (
			SELECT 1 FROM conversation_state s
			WHERE s.conversation_id = c.id AND s.name = 'title'
		  )
		ORDER BY c.id`)
	if err != nil {
		return nil, fmt.Errorf("compat: find empty conversations: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "compat: close empty conversation rows failed", rows.Close())
	}()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("compat: scan empty conversation: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func sampleIDs(ids []string) []string {
	if len(ids) <= purgeSample {
		return ids
	}
	return ids[:purgeSample]
}

// execIfPresent runs query only when table exists. The workspace schema
// is registered by this package before the step runs, but a database
// adopted from an older build may predate message_fts (migration 016),
// and the purge of a conversation must not depend on tables that
// conversation could not have written.
func execIfPresent(
	ctx context.Context, tx *sql.Tx, table, query string, args ...any,
) error {
	var found int
	err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM sqlite_master
		WHERE type = 'table' AND name = ?`, table).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, query, args...)
	return err
}
