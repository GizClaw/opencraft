package compat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// The structured class of a failed turn (interrupt cause + inference
// error kind) became its own archive columns so a transcript can render
// without parsing prose. This step adds the columns and classifies the
// rows written before they existed, using the rules the transcript
// renderer applied at render time until then.
//
// The two patterns read flowcraft's formatters: `engine: interrupted`
// from agent.Interrupted, and the `graph "…" node "…": ` wrapper around
// an inference error. They live here rather than in the UI because this
// is a one-time migration of stored rows, not a rule anything reads at
// render time any more.
//
// They are deliberately more tolerant than the renderer they replace,
// which only accepted the bare `engine: interrupted` prefix and stopped
// at the operation name. Real archives hold rows the old renderer could
// not classify — `graph "…" node "…": engine: interrupted
// (host_shutdown)` and `invalid_provider_response during generate:
// stream.finish.validation` — and reading the fact that is already
// there is strictly better than leaving it empty. Rows that still match
// neither pattern keep an empty class, which renders the generic copy
// exactly as before.
const (
	turnErrorVersion = 14
	turnErrorName    = "014_turn_error_classification"
)

var (
	// interruptCausePattern captures the cause of a rendered interrupt.
	interruptCausePattern = regexp.MustCompile(
		`^(?:graph "[^"]+" node "[^"]+": )?engine: interrupted(?: \(([a-z_]+)\))?`)
	// errorKindPattern captures the leading kind of a rendered node
	// failure, with or without the graph/node prefix.
	errorKindPattern = regexp.MustCompile(
		`^(?:graph "[^"]+" node "[^"]+": )?([a-z_]+)(?: during [a-z_]+)?(?: at [^:]+)?(?:: .*)?$`)
)

// knownErrorKinds is the inference kind vocabulary the backfill accepts.
// A stored failure whose leading token is not in it — `engine`, say,
// from interrupt prose, or a sentence like "exceeded max iterations" —
// keeps an empty class and renders the generic copy. The live path
// writes whatever kind the engine reports; this list exists only so a
// one-time read of historical prose cannot invent a kind.
var knownErrorKinds = map[string]bool{
	"invalid_request":             true,
	"unsupported_operation":       true,
	"unsupported_feature":         true,
	"invalid_extension":           true,
	"unknown_provider":            true,
	"unknown_model":               true,
	"unknown_profile":             true,
	"policy_denied":               true,
	"operation_interrupted":       true,
	"compiler_contract_violation": true,
	"provider_failure":            true,
	"invalid_provider_response":   true,
	"provider_truncated":          true,
	"undefined_tool":              true,
}

// upgradeTurnErrorClassification adds the classifier columns to
// archive_turns and backfills the rows that predate them.
func upgradeTurnErrorClassification(ctx context.Context, handle *db.DB) error {
	if handle == nil {
		return fmt.Errorf("compat: nil workspace database")
	}
	conn := handle.SQLDB()
	var applied int
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`,
		turnErrorVersion).Scan(&applied); err != nil {
		return fmt.Errorf("compat: check turn error classification: %w", err)
	}
	if applied > 0 {
		return nil
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("compat: begin turn error classification: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil &&
			!errors.Is(err, sql.ErrTxDone) {
			telemetry.WarnErr(ctx,
				"compat: rollback turn error classification failed", err)
		}
	}()
	for _, col := range []struct {
		name string
		decl string
	}{
		{name: "interrupt_cause", decl: "TEXT NOT NULL DEFAULT ''"},
		{name: "error_kind", decl: "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := ensureColumn(
			ctx, tx, "archive_turns", col.name, col.decl,
		); err != nil {
			return err
		}
	}
	if err := backfillTurnErrorClass(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, name, applied_at)
		 VALUES (?, ?, datetime('now'))`,
		turnErrorVersion, turnErrorName); err != nil {
		return fmt.Errorf("compat: record turn error classification: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("compat: commit turn error classification: %w", err)
	}
	return nil
}

// backfillTurnErrorClass classifies every archived failure that has no
// class yet. A turn whose prose matches neither pattern is left empty:
// the UI shows its generic copy for it, which is what it showed before
// the columns existed.
func backfillTurnErrorClass(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, error FROM archive_turns
		WHERE error <> '' AND interrupt_cause = '' AND error_kind = ''`)
	if err != nil {
		return fmt.Errorf("compat: list turns to classify: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "compat: close classify rows failed", rows.Close())
	}()
	type classified struct {
		id          int64
		cause, kind string
	}
	var updates []classified
	for rows.Next() {
		var id int64
		var errText string
		if err := rows.Scan(&id, &errText); err != nil {
			return fmt.Errorf("compat: scan turn to classify: %w", err)
		}
		cause, kind := classifyErrorText(errText)
		if cause == "" && kind == "" {
			continue
		}
		updates = append(updates, classified{id: id, cause: cause, kind: kind})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("compat: list turns to classify: %w", err)
	}
	for _, u := range updates {
		if _, err := tx.ExecContext(ctx, `
			UPDATE archive_turns
			SET interrupt_cause = ?, error_kind = ?
			WHERE id = ?`, u.cause, u.kind, u.id); err != nil {
			return fmt.Errorf("compat: classify turn %d: %w", u.id, err)
		}
	}
	return nil
}

// classifyErrorText reads one stored error string into the two enums.
func classifyErrorText(errText string) (cause, kind string) {
	if m := interruptCausePattern.FindStringSubmatch(errText); m != nil {
		cause = m[1]
	}
	if m := errorKindPattern.FindStringSubmatch(errText); m != nil {
		if knownErrorKinds[m[1]] {
			kind = m[1]
		}
	}
	return cause, kind
}
