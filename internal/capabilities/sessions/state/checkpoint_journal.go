package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/telemetry"
)

// checkpointChainLimit bounds how many deltas one run may accumulate
// before the store asks for a fresh base. It matches core's own
// `build.checkpoint_compaction` default: the writer asks for a base at
// that point anyway, and the cap here is what keeps a writer that never
// compacts from growing the chain without bound.
const checkpointChainLimit = 32

// WriteBase implements agent.CheckpointJournal: it replaces a run's record
// and discards any deltas previously appended to it, in one transaction.
//
// A base write happens at the first boundary, after a resume, and whenever
// the chain reaches its cap — the three moments at which the incremental
// chain stops describing the run on its own.
func (s *Store) WriteBase(ctx context.Context, cp agent.Checkpoint) error {
	if err := cp.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(cp.Clone())
	if err != nil {
		return fmt.Errorf("state: encode checkpoint: %w", err)
	}
	tx, err := s.db.SQLDB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("state: write checkpoint base %s: %w", cp.ExecID, err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			telemetry.WarnErr(ctx, "state: rollback checkpoint transaction failed", err)
		}
	}()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_checkpoints (exec_id, data, revision, saved_at)
		VALUES (?, ?, 1, ?)
		ON CONFLICT(exec_id) DO UPDATE SET
			data = excluded.data,
			revision = agent_checkpoints.revision + 1,
			saved_at = excluded.saved_at
	`, cp.ExecID, string(data), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("state: write checkpoint base %s: %w", cp.ExecID, err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM agent_checkpoint_deltas WHERE exec_id = ?`, cp.ExecID,
	); err != nil {
		return fmt.Errorf("state: clear checkpoint chain %s: %w", cp.ExecID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("state: commit checkpoint base %s: %w", cp.ExecID, err)
	}
	return nil
}

// Append implements agent.CheckpointJournal: it extends a run's record with
// one boundary. The row is only written while the run has a base and its
// chain is below the cap; anything else answers agent.ErrNeedsBase, which
// is the engine's cue to write a base instead.
//
// The delta borrows the producing board's memory, so it is encoded here
// before returning (see agent.CheckpointJournal).
func (s *Store) Append(ctx context.Context, delta agent.CheckpointDelta) error {
	if err := delta.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(delta)
	if err != nil {
		return fmt.Errorf("state: encode checkpoint delta %s: %w", delta.ExecID, err)
	}
	res, err := s.db.SQLDB().ExecContext(ctx, `
		INSERT INTO agent_checkpoint_deltas (exec_id, seq, data, saved_at)
		SELECT b.exec_id,
		       COALESCE((SELECT MAX(d.seq) FROM agent_checkpoint_deltas d
		                 WHERE d.exec_id = b.exec_id), 0) + 1,
		       ?, ?
		FROM agent_checkpoints b
		WHERE b.exec_id = ?
		  AND (SELECT COUNT(*) FROM agent_checkpoint_deltas c
		       WHERE c.exec_id = b.exec_id) < ?
	`, string(data), time.Now().UTC().Format(time.RFC3339Nano),
		delta.ExecID, checkpointChainLimit)
	if err != nil {
		return fmt.Errorf("state: append checkpoint delta %s: %w", delta.ExecID, err)
	}
	inserted, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("state: append checkpoint delta %s: %w", delta.ExecID, err)
	}
	if inserted == 0 {
		return fmt.Errorf(
			"state: append checkpoint delta %s: no base record or the chain is at its cap: %w",
			delta.ExecID, agent.ErrNeedsBase)
	}
	return nil
}

// loadCheckpoint reads one run's base record and folds its chain onto it in
// a single read transaction: a base write committing between two separate
// reads would hand back a delta belonging to the new base applied onto the
// old one.
func (s *Store) loadCheckpoint(
	ctx context.Context, execID string,
) (*agent.Checkpoint, error) {
	tx, err := s.db.SQLDB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("state: load checkpoint %s: %w", execID, err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			telemetry.WarnErr(ctx, "state: rollback checkpoint transaction failed", err)
		}
	}()

	var data string
	err = tx.QueryRowContext(ctx,
		`SELECT data FROM agent_checkpoints WHERE exec_id = ?`, execID,
	).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("state: load checkpoint %s: %w", execID, err)
	}
	var cp agent.Checkpoint
	if err := json.Unmarshal([]byte(data), &cp); err != nil {
		return nil, fmt.Errorf("state: decode checkpoint %s: %w", execID, err)
	}
	if err := cp.Validate(); err != nil {
		return nil, fmt.Errorf("state: corrupt checkpoint %s: %w", execID, err)
	}
	if err := replayCheckpointDeltas(ctx, tx, &cp); err != nil {
		return nil, err
	}
	return &cp, nil
}

// replayCheckpointDeltas folds a run's incremental records onto a decoded
// base, in sequence order. A gap means the chain is corrupt: resuming from
// part of it would silently drop state, so it is reported as unavailable —
// the host's cue to start a fresh run instead.
func replayCheckpointDeltas(
	ctx context.Context, tx *sql.Tx, cp *agent.Checkpoint,
) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT seq, data FROM agent_checkpoint_deltas
		WHERE exec_id = ? ORDER BY seq`, cp.ExecID)
	if err != nil {
		return fmt.Errorf("state: load checkpoint %s: replay: %w", cp.ExecID, err)
	}
	defer func() {
		telemetry.WarnErr(ctx,
			"state: close checkpoint delta rows failed", rows.Close())
	}()
	want := int64(1)
	for rows.Next() {
		var seq int64
		var raw string
		if err := rows.Scan(&seq, &raw); err != nil {
			return fmt.Errorf("state: scan checkpoint delta %s: %w", cp.ExecID, err)
		}
		if seq != want {
			return errdefs.NotAvailablef(
				"state: checkpoint chain for %s has a gap at seq %d (want %d): %w",
				cp.ExecID, seq, want, agent.ErrNeedsBase)
		}
		var delta agent.CheckpointDelta
		if err := json.Unmarshal([]byte(raw), &delta); err != nil {
			return fmt.Errorf("state: decode checkpoint delta %s: %w", cp.ExecID, err)
		}
		if delta.ExecID != cp.ExecID {
			return errdefs.NotAvailablef(
				"state: checkpoint chain for %s holds delta of %s",
				cp.ExecID, delta.ExecID)
		}
		delta.ApplyTo(cp)
		want++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("state: replay checkpoint deltas %s: %w", cp.ExecID, err)
	}
	// The folded record must still be a valid checkpoint: a chain that
	// applies but leaves an invalid record is as unusable as a gap.
	if err := cp.Validate(); err != nil {
		return errdefs.NotAvailablef(
			"state: checkpoint %s is unusable after replay: %v", cp.ExecID, err)
	}
	return nil
}

// deleteCheckpoint removes a run's base record and its chain in one
// transaction: a leftover delta describes a base that no longer exists.
func (s *Store) deleteCheckpoint(ctx context.Context, execID string) error {
	tx, err := s.db.SQLDB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("state: delete checkpoint %s: %w", execID, err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			telemetry.WarnErr(ctx, "state: rollback checkpoint transaction failed", err)
		}
	}()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM agent_checkpoints WHERE exec_id = ?`, execID); err != nil {
		return fmt.Errorf("state: delete checkpoint %s: %w", execID, err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM agent_checkpoint_deltas WHERE exec_id = ?`, execID,
	); err != nil {
		return fmt.Errorf("state: delete checkpoint chain %s: %w", execID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("state: commit checkpoint delete %s: %w", execID, err)
	}
	return nil
}

var _ agent.CheckpointJournal = (*Store)(nil)

// execIDFrom checkpoint ids are opaque run ids; the helpers below keep the
// validation in one place for the two entry points that take one.
func validateExecID(execID string) error {
	if strings.TrimSpace(execID) == "" {
		return errdefs.Validation(
			errors.New("state: checkpoint exec_id is required"))
	}
	return nil
}
