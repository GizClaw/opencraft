package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/GizClaw/flowcraft/core/telemetry"
)

// Migration is one versioned schema step owned by the centralized
// orchestration/migrations package. The foundation db package executes
// migrations but never defines any.
type Migration struct {
	Version    int
	Name       string
	Statements []string
}

// Migrate applies migrations in version order inside individual
// transactions. Applied versions are tracked in schema_migrations and
// never re-run.
func (d *DB) Migrate(ctx context.Context, migrations []Migration) error {
	if d == nil || d.db == nil {
		return fmt.Errorf("db: migrate on closed database")
	}
	if _, err := d.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			applied_at TEXT NOT NULL
		)`); err != nil {
		return fmt.Errorf("db: create migrations table: %w", err)
	}
	// Databases created by the old state package tracked migrations
	// without a name column. Make the table compatible before reading
	// or inserting rows.
	if _, err := d.db.ExecContext(ctx,
		"ALTER TABLE schema_migrations ADD COLUMN name TEXT NOT NULL DEFAULT ''",
	); err != nil {
		// Duplicate column is expected on current tables.
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return fmt.Errorf("db: upgrade migrations table: %w", err)
		}
	}

	sorted := append([]Migration(nil), migrations...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Version < sorted[j].Version
	})
	for _, m := range sorted {
		if m.Version <= 0 {
			return fmt.Errorf("db: migration %q has invalid version %d", m.Name, m.Version)
		}
		var exists int
		if err := d.db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM schema_migrations WHERE version = ?",
			m.Version).Scan(&exists); err != nil {
			return fmt.Errorf("db: check migration %d: %w", m.Version, err)
		}
		if exists > 0 {
			continue
		}
		if err := d.applyMigration(ctx, m); err != nil {
			return fmt.Errorf("db: migration %d %s: %w", m.Version, m.Name, err)
		}
	}
	return nil
}

func (d *DB) applyMigration(ctx context.Context, m Migration) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			telemetry.WarnErr(ctx, "db: rollback migration failed", err)
		}
	}()
	for _, stmt := range m.Statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("statement %q: %w", firstLine(stmt), err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO schema_migrations(version, name, applied_at)
		VALUES (?, ?, datetime('now'))`, m.Version, m.Name); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
