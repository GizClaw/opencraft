package migrations

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// migrateUserLegacy brings user.db rows created before the centralized
// migration runner up to the current schema. Every step is idempotent
// so repeated starts are safe.
func migrateUserLegacy(ctx context.Context, handle *db.DB) error {
	sqlDB := handle.SQLDB()
	for _, col := range []struct {
		name string
		decl string
	}{
		{name: "notify", decl: "TEXT NOT NULL DEFAULT 'always'"},
		{name: "conversation_id", decl: "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := ensureColumn(
			ctx, sqlDB, "automations", col.name, col.decl,
		); err != nil {
			return err
		}
	}
	return backfillWeeklyOrigins(ctx, sqlDB)
}

// ensureColumn adds one column to a table created by an app version
// whose CREATE TABLE predates the current schema.
func ensureColumn(
	ctx context.Context, db *sql.DB, table, column, decl string,
) error {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", table, err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "migrations: close column inspection rows failed",
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
	if _, err := db.ExecContext(ctx,
		`ALTER TABLE `+table+` ADD COLUMN `+column+` `+decl,
	); err != nil {
		return fmt.Errorf("add %s.%s: %w", table, column, err)
	}
	return nil
}

// backfillWeeklyOrigins gives pre-anchor weekly automations a phase
// origin. Weekly tasks saved before Origin existed run forever on the
// "origin required" error path otherwise.
func backfillWeeklyOrigins(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT id, schedule FROM automations`)
	if err != nil {
		return fmt.Errorf("list automations for origin backfill: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "migrations: close automations rows failed",
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
		if _, err := db.ExecContext(ctx,
			`UPDATE automations SET schedule = ? WHERE id = ? AND schedule = ?`,
			u.next, u.id, u.raw,
		); err != nil {
			return fmt.Errorf("backfill weekly origin for %s: %w", u.id, err)
		}
	}
	return nil
}
