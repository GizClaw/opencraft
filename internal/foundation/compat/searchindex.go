package compat

import (
	"context"
	"fmt"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// The message full-text index (migration 016) is written by the store
// on the live path: every archived message inserts its index row inside
// the same transaction as the archive row, so a workspace written by a
// current build is never missing rows. Databases written before 016
// carry an empty index instead, and filling those is a Go step because
// the indexed text is the message's prompt projection — rendered by Go,
// not derivable from the stored JSON by SQL.
//
// The backfill itself belongs to the store: it owns the rows, the
// rendering and the transaction boundaries, and runs through the
// WorkspaceImporter port like the rest of the workspace migration. This
// layer owns that it happens exactly once per database, recorded in
// schema_migrations like any other step.
const (
	searchIndexVersion = 17
	searchIndexName    = "017_message_fts_backfill"
)

// BackfillSearchIndex indexes the messages of a database that predates
// the full-text index. It is not part of Workspace: the walk is
// proportional to the archive (minutes on a large database), so the
// caller schedules it after the store is already usable and treats a
// failure as recoverable — the store's backfill is idempotent (it skips
// rows the index already holds), so an interrupted run simply finishes
// on the next open.
func BackfillSearchIndex(
	ctx context.Context, handle *db.DB, importer WorkspaceImporter,
) error {
	if handle == nil {
		return fmt.Errorf("compat: nil workspace database")
	}
	if importer == nil {
		return fmt.Errorf("compat: workspace importer is required")
	}
	var applied int
	if err := handle.SQLDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`,
		searchIndexVersion).Scan(&applied); err != nil {
		return fmt.Errorf("compat: check search index backfill: %w", err)
	}
	if applied > 0 {
		return nil
	}
	if err := importer.BackfillSearchIndex(ctx); err != nil {
		return fmt.Errorf("compat: backfill message index: %w", err)
	}
	if _, err := handle.SQLDB().ExecContext(ctx,
		`INSERT INTO schema_migrations(version, name, applied_at)
		 VALUES (?, ?, datetime('now'))`,
		searchIndexVersion, searchIndexName); err != nil {
		return fmt.Errorf("compat: record search index backfill: %w", err)
	}
	telemetry.Info(ctx, "compat: message search index backfilled")
	return nil
}
