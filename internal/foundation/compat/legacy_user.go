package compat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// userLegacyVersion is the user.db version recorded for the Go
// compatibility step below, in the same sequence as the SQL files in
// sql/user.
const (
	userLegacyVersion = 6
	userLegacyName    = "006_user_legacy_upgrade"
)

// migrateUserLegacy brings user.db rows created before the centralized
// migration runner up to the current schema, once per database. It is
// recorded like an SQL migration (see userLegacyVersion): the steps
// inspect the schema rather than express themselves as statements, and
// without a recorded version every start would repeat the inspection
// and the weekly-origin scan. It lives in Go for the same reason the
// workspace summary cleanup does — SQL cannot express "add the column
// only when a pre-runner build left it out".
func migrateUserLegacy(ctx context.Context, handle *db.DB) error {
	conn := handle.SQLDB()
	var applied int
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`,
		userLegacyVersion).Scan(&applied); err != nil {
		return fmt.Errorf("compat: check user legacy upgrade: %w", err)
	}
	if applied > 0 {
		return nil
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("compat: begin user legacy upgrade: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil &&
			!errors.Is(err, sql.ErrTxDone) {
			telemetry.WarnErr(ctx,
				"compat: rollback user legacy upgrade failed", err)
		}
	}()
	for _, col := range []struct {
		name string
		decl string
	}{
		{name: "notify", decl: "TEXT NOT NULL DEFAULT 'always'"},
		{name: "conversation_id", decl: "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := ensureColumn(
			ctx, tx, "automations", col.name, col.decl,
		); err != nil {
			return err
		}
	}
	if err := backfillWeeklyOrigins(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, name, applied_at)
		 VALUES (?, ?, datetime('now'))`,
		userLegacyVersion, userLegacyName); err != nil {
		return fmt.Errorf("compat: record user legacy upgrade: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("compat: commit user legacy upgrade: %w", err)
	}
	return nil
}

// execQueryer is the part of database/sql shared by *sql.DB and
// *sql.Tx, so one legacy step can run inside a transaction.
type execQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// ensureColumn adds one column to a table created by an app version
// whose CREATE TABLE predates the current schema.
func ensureColumn(
	ctx context.Context, q execQueryer, table, column, decl string,
) error {
	rows, err := q.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", table, err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "compat: close column inspection rows failed",
			rows.Close())
	}()
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return fmt.Errorf("scan %s columns: %w", table, err)
		}
		if name == column {
			return rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx,
		`ALTER TABLE `+table+` ADD COLUMN `+column+` `+decl,
	); err != nil {
		return fmt.Errorf("add %s.%s: %w", table, column, err)
	}
	return nil
}

// backfillWeeklyOrigins gives pre-anchor weekly automations a phase
// origin. Weekly tasks saved before Origin existed run forever on the
// "origin required" error path otherwise.
func backfillWeeklyOrigins(ctx context.Context, q execQueryer) error {
	rows, err := q.QueryContext(ctx, `SELECT id, schedule FROM automations`)
	if err != nil {
		return fmt.Errorf("list automations for origin backfill: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "compat: close automations rows failed",
			rows.Close())
	}()

	type scheduleJSON struct {
		Type          string   `json:"type"`
		IntervalHours int      `json:"interval_hours,omitempty"`
		IntervalWeeks int      `json:"interval_weeks,omitempty"`
		Days          []string `json:"days,omitempty"`
		Time          string   `json:"time,omitempty"`
		Origin        string   `json:"origin,omitempty"`
	}
	type update struct {
		id   string
		raw  string
		next string
	}
	var updates []update
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return fmt.Errorf("scan automations for origin backfill: %w", err)
		}
		var sched scheduleJSON
		if err := json.Unmarshal([]byte(raw), &sched); err != nil {
			continue // leave corrupt schedules for the task editor.
		}
		if sched.Type != "weekly" || sched.Origin != "" {
			continue
		}
		sched.Origin = time.Now().Format("2006-01-02")
		encoded, err := json.Marshal(sched)
		if err != nil {
			return fmt.Errorf("encode weekly origin for %s: %w", id, err)
		}
		updates = append(updates, update{id: id, raw: raw, next: string(encoded)})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list automations for origin backfill: %w", err)
	}
	for _, u := range updates {
		if _, err := q.ExecContext(ctx,
			`UPDATE automations SET schedule = ? WHERE id = ? AND schedule = ?`,
			u.next, u.id, u.raw,
		); err != nil {
			return fmt.Errorf("backfill weekly origin for %s: %w", u.id, err)
		}
	}
	return nil
}
