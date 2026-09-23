package compat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// A conversation's history to the model used to have a second physical
// copy: memory_items held the same messages archive_messages held, written
// in the archive's transaction and kept in step by convention. The model
// window is now a projection of the transcript
// (capabilities/memory/projection.go), so that copy is dead weight — and
// worse, a second answer to "what did the model see", which is exactly
// what the convergence removed. This step drops it.
//
// The step is Go rather than a SQL file for two reasons: it reports before
// it drops, and a database that never created the table is simply recorded
// as migrated. The report is a whole-database aggregate, not a per-row
// diff (a row-level diff would need the projection's render rules, which
// live above this layer): the copy's rows are counted against the
// transcript rows that could have produced them, and the rows the copy
// structurally could not hold — app-authored turns, imported system
// prompts, rows with no text — are counted by their own category. What
// must never appear is the reverse: a memory row with no transcript row
// behind it, which would mean the copy knew something the transcript
// never recorded. Those conversations are named in the log.
const (
	dropMemoryItemsVersion = 20
	dropMemoryItemsName    = "020_drop_memory_items"
)

// memoryDriftSample bounds how many conversations the report walks. The
// step must not cost more than the upgrade it is part of, and a
// disagreement is either everywhere (the aggregate counts show it) or in
// a handful of conversations (the sample names them).
const memoryDriftSample = 200

func dropMemoryItems(ctx context.Context, handle *db.DB) error {
	conn := handle.SQLDB()
	var applied int
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`,
		dropMemoryItemsVersion).Scan(&applied); err != nil {
		return fmt.Errorf("compat: check memory_items drop: %w", err)
	}
	if applied > 0 {
		return nil
	}
	var exists int
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master
		 WHERE type = 'table' AND name = 'memory_items'`).Scan(&exists); err != nil {
		return fmt.Errorf("compat: inspect memory_items: %w", err)
	}
	if exists == 0 {
		_, err := conn.ExecContext(ctx,
			`INSERT INTO schema_migrations(version, name, applied_at)
			 VALUES (?, ?, datetime('now'))`,
			dropMemoryItemsVersion, dropMemoryItemsName)
		if err != nil {
			return fmt.Errorf("compat: record memory_items drop: %w", err)
		}
		return nil
	}

	if err := reportMemoryItemsDrift(ctx, conn); err != nil {
		return err
	}
	// The drop and its version record go together: a database must never
	// be left with the table gone and the step unrecorded (which would
	// re-run the report against a table that is no longer there).
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("compat: begin memory_items drop: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			telemetry.WarnErr(ctx, "compat: rollback memory_items drop failed", err)
		}
	}()
	if _, err := tx.ExecContext(ctx, `DROP TABLE memory_items`); err != nil {
		return fmt.Errorf("compat: drop memory_items: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, name, applied_at)
		 VALUES (?, ?, datetime('now'))`,
		dropMemoryItemsVersion, dropMemoryItemsName); err != nil {
		return fmt.Errorf("compat: record memory_items drop: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("compat: commit memory_items drop: %w", err)
	}
	return nil
}

// reportMemoryItemsDrift logs what the retired copy held, in categories,
// before it is dropped. Nothing here blocks the upgrade: the copy is
// derived data by construction, and a disagreement the report names is a
// question about the past, not a reason to keep a second history.
func reportMemoryItemsDrift(ctx context.Context, conn *sql.DB) error {
	var memoryRows, memoryThreads int
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*), COUNT(DISTINCT thread_id) FROM memory_items`,
	).Scan(&memoryRows, &memoryThreads); err != nil {
		return fmt.Errorf("compat: count memory_items: %w", err)
	}
	var archived, notes, system, textless int
	// Counted structurally, in SQL: app-authored turns and imported
	// system prompts are exact (kind and role are columns), while "no
	// text" only says the payload has no text part — whether a renderer
	// would drop the row is a rule that lives above this layer
	// (capabilities/memory/projection.go). The report is a diagnostic,
	// not a renderer.
	if err := conn.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM archive_messages m
			 JOIN archive_turns t ON t.id = m.turn_id
			 WHERE t.kind = '' AND m.role != 'system'),
			(SELECT COUNT(*) FROM archive_messages m
			 JOIN archive_turns t ON t.id = m.turn_id
			 WHERE t.kind != ''),
			(SELECT COUNT(*) FROM archive_messages WHERE role = 'system'),
			(SELECT COUNT(*) FROM archive_messages m
			 JOIN archive_turns t ON t.id = m.turn_id
			 WHERE t.kind = '' AND m.role != 'system'
			   AND m.content_json NOT LIKE '%"text"%')`,
	).Scan(&archived, &notes, &system, &textless); err != nil {
		return fmt.Errorf("compat: count transcript rows: %w", err)
	}

	telemetry.Info(ctx, "compat: memory_items is derived; the transcript is the history",
		otellog.Int("memory_rows", memoryRows),
		otellog.Int("memory_conversations", memoryThreads),
		otellog.Int("transcript_rows", archived),
		otellog.Int("transcript_note_rows", notes),
		otellog.Int("transcript_system_rows", system),
		otellog.Int("transcript_textless_rows", textless))

	if memoryRows == 0 {
		return nil
	}

	memCounts, err := memoryRowsPerThread(ctx, conn)
	if err != nil {
		return err
	}
	threads := make([]string, 0, len(memCounts))
	for thread := range memCounts {
		threads = append(threads, thread)
	}
	if len(threads) > memoryDriftSample {
		threads = threads[:memoryDriftSample]
	}
	transcriptCounts, err := transcriptRowsPerConversation(ctx, conn, threads)
	if err != nil {
		return err
	}
	var unexplained []string
	for _, thread := range threads {
		if memCounts[thread] > transcriptCounts[thread] {
			unexplained = append(unexplained, fmt.Sprintf("%s(+%d)",
				thread, memCounts[thread]-transcriptCounts[thread]))
			if len(unexplained) >= 5 {
				break
			}
		}
	}
	if len(unexplained) > 0 {
		telemetry.Warn(ctx,
			"compat: memory_items held rows the transcript does not",
			otellog.String("conversations", strings.Join(unexplained, ", ")))
	}
	return nil
}

func memoryRowsPerThread(
	ctx context.Context, conn *sql.DB,
) (map[string]int, error) {
	rows, err := conn.QueryContext(ctx,
		`SELECT thread_id, COUNT(*) FROM memory_items GROUP BY thread_id`)
	if err != nil {
		return nil, fmt.Errorf("compat: memory rows per conversation: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "compat: close memory count rows failed", rows.Close())
	}()
	counts := make(map[string]int)
	for rows.Next() {
		var thread string
		var count int
		if err := rows.Scan(&thread, &count); err != nil {
			return nil, fmt.Errorf("compat: scan memory count: %w", err)
		}
		counts[thread] = count
	}
	return counts, rows.Err()
}

func transcriptRowsPerConversation(
	ctx context.Context, conn *sql.DB, threads []string,
) (map[string]int, error) {
	counts := make(map[string]int, len(threads))
	if len(threads) == 0 {
		return counts, nil
	}
	placeholders := make([]byte, 0, len(threads)*2)
	args := make([]any, 0, len(threads))
	for i, thread := range threads {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args = append(args, thread)
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT m.conversation_id, COUNT(*)
		FROM archive_messages m
		JOIN archive_turns t ON t.id = m.turn_id
		WHERE t.kind = '' AND m.role != 'system'
		  AND m.conversation_id IN (`+string(placeholders)+`)
		GROUP BY m.conversation_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("compat: transcript rows per conversation: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "compat: close transcript count rows failed", rows.Close())
	}()
	for rows.Next() {
		var thread string
		var count int
		if err := rows.Scan(&thread, &count); err != nil {
			return nil, fmt.Errorf("compat: scan transcript count: %w", err)
		}
		counts[thread] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, thread := range threads {
		if _, ok := counts[thread]; !ok {
			counts[thread] = 0
		}
	}
	return counts, nil
}
