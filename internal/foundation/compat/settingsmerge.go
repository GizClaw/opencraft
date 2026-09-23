package compat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// mergeSessionSettingsVersion is recorded like an SQL migration so the
// step runs once per database. It lives in Go rather than in
// sql/workspace because the merge is conditional twice over: a database
// that never created the legacy table is simply recorded as migrated,
// and each legacy row's three columns have to become one JSON document
// (SQL would need json_object(), an optional build of the SQLite
// amalgamation, while the store writes its documents from Go anyway).
const (
	mergeSessionSettingsVersion = 19
	mergeSessionSettingsName    = "019_session_settings_merge"
)

// legacySessionSettings is the shape this step reads, and
// sessionSettingsDocumentName is the document it writes into. Both are
// spelled out here — mirroring
// sessions/state.SessionSettingsName and its document struct — because
// this layer must keep reading and writing shapes of builds that are
// gone; a shared constant would move with the live store.
const sessionSettingsDocumentName = "settings"

// legacySessionSettingsWire is the document as the migration writes it.
type legacySessionSettingsWire struct {
	ThinkLevel string `json:"think_level,omitempty"`
	Model      string `json:"model,omitempty"`
	Mode       string `json:"mode,omitempty"`
}

// mergeSessionSettings folds a session's own settings — reasoning
// effort, model hint and sandbox mode — from the session_settings table
// into the conversation_state document the store reads today, then drops
// the table. A key is written only when the legacy row actually carried
// one, so a row that held nothing but empty values leaves no document
// behind (every reader treats absence and an empty value alike).
//
// A document that already exists is left alone: only a build older than
// this step wrote session_settings, so nothing can have written the
// document before the table is merged.
func mergeSessionSettings(ctx context.Context, handle *db.DB) error {
	conn := handle.SQLDB()
	var applied int
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`,
		mergeSessionSettingsVersion).Scan(&applied); err != nil {
		return fmt.Errorf("compat: check session settings merge: %w", err)
	}
	if applied > 0 {
		return nil
	}
	var exists int
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master
		 WHERE type = 'table' AND name = 'session_settings'`).Scan(&exists); err != nil {
		return fmt.Errorf("compat: inspect session settings table: %w", err)
	}
	if exists == 0 {
		return recordSessionSettingsMerge(ctx, conn)
	}

	legacy, err := readLegacySessionSettings(ctx, conn)
	if err != nil {
		return err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("compat: begin session settings merge: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			telemetry.WarnErr(ctx,
				"compat: rollback session settings merge failed", err)
		}
	}()
	merged := 0
	for _, row := range legacy {
		payload, err := json.Marshal(row.doc)
		if err != nil {
			return fmt.Errorf("compat: encode session settings %s: %w",
				row.contextID, err)
		}
		var stored int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM conversation_state
			 WHERE conversation_id = ? AND name = ?`,
			row.contextID, sessionSettingsDocumentName).Scan(&stored); err != nil {
			return fmt.Errorf("compat: inspect session settings document: %w", err)
		}
		if stored > 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO conversation_state(
				conversation_id, name, value_json, updated_at
			) VALUES (?, ?, ?, ?)`,
			row.contextID, sessionSettingsDocumentName, string(payload),
			row.updatedAt); err != nil {
			return fmt.Errorf("compat: write session settings %s: %w",
				row.contextID, err)
		}
		merged++
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE session_settings`); err != nil {
		return fmt.Errorf("compat: drop session settings: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, name, applied_at)
		 VALUES (?, ?, datetime('now'))`,
		mergeSessionSettingsVersion, mergeSessionSettingsName); err != nil {
		return fmt.Errorf("compat: record session settings merge: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("compat: commit session settings merge: %w", err)
	}
	if merged > 0 {
		telemetry.Info(ctx,
			"compat: merged session settings into conversation state",
			otellog.Int("sessions", merged))
	}
	return nil
}

// legacySettingsRow is one session_settings row on its way into the
// document shape.
type legacySettingsRow struct {
	contextID string
	doc       legacySessionSettingsWire
	updatedAt string
}

func readLegacySessionSettings(
	ctx context.Context, conn *sql.DB,
) ([]legacySettingsRow, error) {
	rows, err := conn.QueryContext(ctx,
		`SELECT context_id, think_level, model, mode, updated_at
		 FROM session_settings`)
	if err != nil {
		return nil, fmt.Errorf("compat: read session settings: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "compat: close session settings rows failed",
			rows.Close())
	}()
	var out []legacySettingsRow
	for rows.Next() {
		var contextID, thinkLevel, model, mode, updatedAt string
		if err := rows.Scan(
			&contextID, &thinkLevel, &model, &mode, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("compat: scan session settings: %w", err)
		}
		doc := legacySessionSettingsWire{
			ThinkLevel: thinkLevel,
			Model:      model,
			Mode:       mode,
		}
		if strings.TrimSpace(doc.ThinkLevel) == "" &&
			strings.TrimSpace(doc.Model) == "" &&
			strings.TrimSpace(doc.Mode) == "" {
			// The row held nothing: an absent document says the same.
			continue
		}
		out = append(out, legacySettingsRow{
			contextID: contextID,
			doc:       doc,
			updatedAt: updatedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("compat: session settings rows: %w", err)
	}
	return out, nil
}

// recordSessionSettingsMerge marks the step as applied in a database
// that never created the legacy table.
func recordSessionSettingsMerge(ctx context.Context, conn *sql.DB) error {
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, name, applied_at)
		 VALUES (?, ?, datetime('now'))`,
		mergeSessionSettingsVersion, mergeSessionSettingsName); err != nil {
		return fmt.Errorf("compat: record session settings merge: %w", err)
	}
	return nil
}
