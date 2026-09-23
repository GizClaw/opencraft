// Package compat is the single compatibility layer of OpenCraft: the
// only place that knows which shapes an older build wrote. It owns
// every versioned SQLite migration (workspace session.db and user
// user.db), the idempotent legacy-data adoption steps (JSON
// transcripts written before sessions lived in SQLite, plugin
// ownership rows written before the ownership sidecar), and the rules
// that keep a hand-edited user configuration layer loadable.
//
// Adding compatibility for a new release means editing this package
// and nothing else. Two shapes exist:
//
//   - Versioned steps run once per database and are recorded in
//     schema_migrations; see migrations.go, schema_workspace.go and
//     schema_user.go.
//   - Shape rules describe data that lives outside a database (the
//     user layer, plugin ownership rows, subagent declarations) and
//     are detected by predicate, because nothing rewrites those files
//     in place; see configdoc.go, codec.go, retiredrefs.go and
//     agentdeclaration.go.
//
// The package deliberately owns no I/O policy: it takes payloads and
// primitives and returns them transformed. Opening databases, holding
// the config-state lock and writing files stay with the packages that
// own those resources (foundation/db, foundation/config,
// capabilities/sessions), which reach back into this package through
// the ports declared in workspaceimport.go.
//
// Two classes of compatibility stay outside the layer, because neither
// is a shape an older build wrote:
//
//   - Invariants a module enforces on every read and write. Usage keys
//     are normalized to the model name on their way in and out
//     (capabilities/sessions.NormalizeModelName), and an instance
//     without a stable id is addressed positionally only when the
//     layer cannot carry a disabled one. The stored shape those rules
//     grew out of is a migration (user 004); the rule itself belongs
//     to the module that owns the column.
//   - The legacy turn marker ("\n\n> ⛔ …") that pre-archive-column
//     builds appended to the last assistant text. No build ever
//     persisted it: the marker went into the renderer's message view,
//     the transcript writer archived engine messages, and the archive
//     that replaced it records status and error on the turn row. Git
//     history has no Go writer for it and real archives hold zero
//     occurrences, so the renderer's strip was deleted rather than
//     turned into step 014 — a step that would rewrite nothing.
package compat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// Workspace migrates one workspace session.db, imports legacy JSON
// transcripts found under root, and backfills the message full-text
// index for databases that predate it. root is the workspace sessions
// directory (the parent of each s-* session folder).
func Workspace(
	ctx context.Context, handle *db.DB, root string,
	importer WorkspaceImporter,
) error {
	if err := WorkspaceSchema(ctx, handle); err != nil {
		return err
	}
	if err := WorkspaceData(ctx, root, importer); err != nil {
		return err
	}
	return backfillSearchIndex(ctx, handle, importer)
}

// WorkspaceSchema applies only the versioned workspace schema
// migrations. Workspace normally runs WorkspaceData afterwards.
func WorkspaceSchema(ctx context.Context, handle *db.DB) error {
	if handle == nil {
		return fmt.Errorf("compat: nil workspace database")
	}
	if err := handle.Migrate(ctx, workspaceMigrations()); err != nil {
		return fmt.Errorf("compat: workspace schema: %w", err)
	}
	if err := cleanupSummaryNodesFK(ctx, handle); err != nil {
		return err
	}
	if err := upgradeTurnErrorClassification(ctx, handle); err != nil {
		return err
	}
	// Workspace handles open with foreign_keys=OFF because pre-012
	// schemas reference the tables 009 drops. Once every migration
	// ran, the schema is clean and enforcement can come back on for
	// all later reads and writes.
	if err := handle.SetForeignKeys(true); err != nil {
		return fmt.Errorf("compat: enable workspace foreign keys: %w", err)
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
		return fmt.Errorf("compat: check summary cleanup: %w", err)
	}
	if applied > 0 {
		return nil
	}
	var exists int
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master
		 WHERE type = 'table' AND name = 'summary_nodes'`,
	).Scan(&exists); err != nil {
		return fmt.Errorf("compat: inspect summary nodes table: %w", err)
	}
	if exists == 0 {
		_, err := conn.ExecContext(ctx,
			`INSERT INTO schema_migrations(version, name, applied_at)
			 VALUES (?, ?, datetime('now'))`,
			cleanupSummaryNodesVersion, cleanupSummaryNodesName)
		if err != nil {
			return fmt.Errorf("compat: record summary cleanup: %w", err)
		}
		return nil
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("compat: begin summary cleanup: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil &&
			!errors.Is(err, sql.ErrTxDone) {
			telemetry.WarnErr(ctx,
				"compat: rollback summary cleanup failed", err)
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
			return fmt.Errorf("compat: summary cleanup %q: %w", stmt, err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, name, applied_at)
		 VALUES (?, ?, datetime('now'))`,
		cleanupSummaryNodesVersion, cleanupSummaryNodesName); err != nil {
		return fmt.Errorf("compat: record summary cleanup: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("compat: commit summary cleanup: %w", err)
	}
	return nil
}

// User migrates the user-level user.db shared by usage and
// automations: the versioned SQL set, then the Go step that repairs a
// pre-runner schema (see migrateUserLegacy, recorded as version 6).
func User(ctx context.Context, handle *db.DB) error {
	if handle == nil {
		return fmt.Errorf("compat: nil user database")
	}
	if err := handle.Migrate(ctx, userMigrations()); err != nil {
		return fmt.Errorf("compat: user schema: %w", err)
	}
	if err := migrateUserLegacy(ctx, handle); err != nil {
		return fmt.Errorf("compat: user legacy: %w", err)
	}
	return nil
}
