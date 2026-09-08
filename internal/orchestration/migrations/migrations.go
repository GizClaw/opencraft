// Package migrations is the single owner of every OpenCraft SQLite
// schema and legacy-data migration. It defines the complete versioned
// migration sets for workspace session.db and user user.db, executes
// them through foundation/db, and runs the idempotent legacy
// compatibility steps that older app versions need.
package migrations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// Workspace migrates one workspace session.db and then imports legacy
// JSON transcripts found under root. root is the workspace sessions
// directory (the parent of each s-* session folder).
func Workspace(ctx context.Context, handle *db.DB, root string) error {
	if err := WorkspaceSchema(ctx, handle); err != nil {
		return err
	}
	return WorkspaceData(ctx, root, handle)
}

// WorkspaceSchema applies only the versioned workspace schema
// migrations. Workspace normally runs WorkspaceData afterwards.
func WorkspaceSchema(ctx context.Context, handle *db.DB) error {
	if handle == nil {
		return fmt.Errorf("migrations: nil workspace database")
	}
	if err := handle.Migrate(ctx, workspaceMigrations()); err != nil {
		return fmt.Errorf("migrations: workspace schema: %w", err)
	}
	if err := cleanupSummaryNodesFK(ctx, handle); err != nil {
		return err
	}
	// Workspace handles open with foreign_keys=OFF because pre-012
	// schemas reference the tables 009 drops. Once every migration
	// ran, the schema is clean and enforcement can come back on for
	// all later reads and writes.
	if err := handle.SetForeignKeys(true); err != nil {
		return fmt.Errorf("migrations: enable workspace foreign keys: %w", err)
	}
	return nil
}

// cleanupSummaryNodesVersion is recorded like an SQL migration so the
// cleanup runs exactly once per database. It lives in Go because SQL
// files cannot express the "skip when the legacy table never existed"
// branch: v0.1.0 databases recorded migration 004 without ever
// creating summary_nodes, and 009 dropped its parent table threads.
const (
	cleanupSummaryNodesVersion = 12
	cleanupSummaryNodesName    = "012_summary_nodes_no_fk"
)

// cleanupSummaryNodesFK rebuilds summary_nodes without the
// REFERENCES threads(id) constraint left over from before migration
// 009 dropped the threads table. With the parent gone, every foreign
// key check on the old declaration fails, which is why the workspace
// database runs with foreign_keys=OFF until this cleanup. Databases
// that never created summary_nodes are recorded as migrated and left
// alone.
func cleanupSummaryNodesFK(ctx context.Context, handle *db.DB) error {
	conn := handle.SQLDB()
	var applied int
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`,
		cleanupSummaryNodesVersion).Scan(&applied); err != nil {
		return fmt.Errorf("migrations: check summary cleanup: %w", err)
	}
	if applied > 0 {
		return nil
	}
	var exists int
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master
		 WHERE type = 'table' AND name = 'summary_nodes'`,
	).Scan(&exists); err != nil {
		return fmt.Errorf("migrations: inspect summary nodes table: %w", err)
	}
	if exists == 0 {
		_, err := conn.ExecContext(ctx,
			`INSERT INTO schema_migrations(version, name, applied_at)
			 VALUES (?, ?, datetime('now'))`,
			cleanupSummaryNodesVersion, cleanupSummaryNodesName)
		if err != nil {
			return fmt.Errorf("migrations: record summary cleanup: %w", err)
		}
		return nil
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrations: begin summary cleanup: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil &&
			!errors.Is(err, sql.ErrTxDone) {
			telemetry.WarnErr(ctx,
				"migrations: rollback summary cleanup failed", err)
		}
	}()
	statements := []string{
		`CREATE TABLE summary_nodes_new (
			id TEXT PRIMARY KEY,
			thread_id TEXT NOT NULL,
			level INTEGER NOT NULL DEFAULT 0,
			parent_ids TEXT NOT NULL DEFAULT '[]',
			source_ids TEXT NOT NULL DEFAULT '[]',
			summary TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			metadata TEXT NOT NULL DEFAULT '{}'
		)`,
		`INSERT INTO summary_nodes_new (
			id, thread_id, level, parent_ids, source_ids, summary,
			created_at, updated_at, metadata
		)
		SELECT id, thread_id, level, parent_ids, source_ids, summary,
			created_at, updated_at, metadata
		FROM summary_nodes`,
		`DROP TABLE summary_nodes`,
		`ALTER TABLE summary_nodes_new RENAME TO summary_nodes`,
		`CREATE INDEX idx_summary_thread_level
			ON summary_nodes(thread_id, level)`,
	}
	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrations: summary cleanup %q: %w", stmt, err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, name, applied_at)
		 VALUES (?, ?, datetime('now'))`,
		cleanupSummaryNodesVersion, cleanupSummaryNodesName); err != nil {
		return fmt.Errorf("migrations: record summary cleanup: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrations: commit summary cleanup: %w", err)
	}
	return nil
}

// User migrates the user-level user.db shared by usage and
// automations, then applies user-level legacy compatibility steps.
func User(ctx context.Context, handle *db.DB) error {
	if handle == nil {
		return fmt.Errorf("migrations: nil user database")
	}
	if err := handle.Migrate(ctx, userMigrations()); err != nil {
		return fmt.Errorf("migrations: user schema: %w", err)
	}
	if err := migrateUserLegacy(ctx, handle); err != nil {
		return fmt.Errorf("migrations: user legacy: %w", err)
	}
	return nil
}
