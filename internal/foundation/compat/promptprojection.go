package compat

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"go.opentelemetry.io/otel/attribute"

	"github.com/GizClaw/opencraft/internal/foundation/db"
	"github.com/GizClaw/opencraft/internal/foundation/utils/summarytext"
)

// Rows written before migration 026 carry no prompt projection, so the reader
// projects them the way it always did: decode the content, clone it, render the
// tool activity. That is correct, but it is exactly the cost the column exists
// to avoid, and a workspace that is never rewritten keeps paying it.
//
// This step derives the projection once for every such row. It is a Go step
// because the rendering is Go's (the projection cannot be expressed in SQL),
// and it runs in batches so an interrupted upgrade keeps what it finished: a
// row that already carries a state is skipped, so a re-run continues.
const (
	promptProjectionVersion = 27
	promptProjectionName    = "027_prompt_projection_backfill"
)

// promptBackfillBatch bounds one transaction. A large archive is tens of
// thousands of rows; committing per batch keeps the write lock short and makes
// progress durable, which matters because the step runs inside the upgrade the
// user is waiting on.
const promptBackfillBatch = 512

func backfillPromptProjection(ctx context.Context, handle *db.DB) error {
	conn := handle.SQLDB()
	var done int
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`,
		promptProjectionVersion).Scan(&done); err != nil {
		return fmt.Errorf("compat: check prompt projection backfill: %w", err)
	}
	if done > 0 {
		return nil
	}
	total := 0
	for {
		rows, err := conn.QueryContext(ctx, `
			SELECT id, role, content_json FROM archive_messages
			WHERE prompt_state = '' LIMIT ?`, promptBackfillBatch)
		if err != nil {
			return fmt.Errorf("compat: read rows without a projection: %w", err)
		}
		type pending struct {
			id      int64
			role    string
			content string
			prompt  string
			state   string
		}
		batch := make([]pending, 0, promptBackfillBatch)
		for rows.Next() {
			var row pending
			if err := rows.Scan(&row.id, &row.role, &row.content); err != nil {
				telemetry.WarnErr(ctx, "compat: close archive rows failed", rows.Close())
				return fmt.Errorf("compat: scan archive row: %w", err)
			}
			batch = append(batch, row)
		}
		iterErr := rows.Err()
		if err := rows.Close(); err != nil {
			telemetry.WarnErr(ctx, "compat: close archive rows failed", err)
		}
		if iterErr != nil {
			return fmt.Errorf("compat: iterate archive rows: %w", iterErr)
		}
		if len(batch) == 0 {
			break
		}
		for i := range batch {
			var content message.Content
			if err := json.Unmarshal([]byte(batch[i].content), &content); err != nil {
				// A payload that does not decode has no prompt form now and
				// will not grow one: record the skip so the reader stops
				// trying and the batch makes progress.
				batch[i].state = summarytext.PromptSkip
				continue
			}
			projected, state := summarytext.ProjectRow(
				message.Role(batch[i].role), content)
			batch[i].state = state
			if state == summarytext.PromptSkip {
				continue
			}
			raw, err := json.Marshal(projected)
			if err != nil {
				return fmt.Errorf("compat: marshal prompt projection: %w", err)
			}
			batch[i].prompt = string(raw)
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("compat: begin prompt projection batch: %w", err)
		}
		for i := range batch {
			if _, err := tx.ExecContext(ctx, `
				UPDATE archive_messages SET prompt_json = ?, prompt_state = ?
				WHERE id = ?`,
				batch[i].prompt, batch[i].state, batch[i].id); err != nil {
				telemetry.WarnErr(ctx, "compat: rollback prompt projection batch failed", tx.Rollback())
				return fmt.Errorf("compat: write prompt projection: %w", err)
			}
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("compat: commit prompt projection batch: %w", err)
		}
		total += len(batch)
	}
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, name, applied_at)
		 VALUES (?, ?, datetime('now'))`,
		promptProjectionVersion, promptProjectionName); err != nil {
		return fmt.Errorf("compat: record prompt projection backfill: %w", err)
	}
	if total > 0 {
		telemetry.Info(ctx, "compat: prompt projection backfilled",
			attribute.Int("rows", total))
	}
	return nil
}
