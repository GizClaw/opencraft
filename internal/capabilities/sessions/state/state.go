// Package state implements the SQLite storage owned by the session
// store: conversations, archive turns/messages, memory items, summary
// nodes, agent checkpoints, settings and schema migrations. It is opened by sessions.Store at
// <root>/session.db and is not a deploy resource of its own.
package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/foundation/db"
	"github.com/GizClaw/opencraft/internal/foundation/ids"
)

// Store is a SQLite-backed opencraft state store. Open creates or
// opens the database handle without applying migrations; the central
// internal/foundation/compat package owns every schema step and must run
// Workspace before the store is used.
type Store struct {
	db     *db.DB
	ownsDB bool
}

// Open opens (creating if necessary) the SQLite database at path.
// Callers must apply internal/foundation/compat.WorkspaceSchema before
// using the returned store.
func Open(path string) (*Store, error) {
	handle, err := db.OpenWithOptions(path, db.OpenOptions{ForeignKeys: false})
	if err != nil {
		return nil, fmt.Errorf("state: open %s: %w", path, err)
	}
	return &Store{db: handle, ownsDB: true}, nil
}

// Attach wraps an already-migrated foundation/db handle. The caller
// keeps ownership of the handle.
func Attach(handle *db.DB) *Store {
	return &Store{db: handle, ownsDB: false}
}

// Close closes the underlying database handle when owned.
func (s *Store) Close() error {
	if s.ownsDB && s.db != nil {
		return s.db.Close()
	}
	return nil
}

// Handle returns the foundation/db handle backing this store.
func (s *Store) Handle() *db.DB { return s.db }

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("state: not found")

// ---------------------------------------------------------------------------
// Checkpoints: state is the project's single SQLite store, so it also
// serves as the agent checkpoint store (one connection, one schema
// owner) instead of a second sqlite backend sharing the same file.
// ---------------------------------------------------------------------------

// CheckpointStats summarizes the agent_checkpoints table. A checkpoint
// is rewritten once per completed wave, so Rows/Runs/Bytes are also the
// write-amplification figure for recovery: a turn normally drops its row
// when it ends, and anything left is either a live run or a turn the next
// assembly has to reconstruct.
//
// Two kinds of row share the table, and the prefix tells them apart:
// rows under ids.RunPrefix are the engine's per-run checkpoints — the
// crash-recovery log the host replays at assembly to reconstruct a turn
// the process never archived — while core's own session-state rows carry
// a different prefix. Only the first kind is what recovery examines.
type CheckpointStats struct {
	// Rows counts every checkpoint in the table.
	Rows int `json:"rows"`
	// Runs counts the rows under [ids.RunPrefix].
	Runs int `json:"runs"`
	// Bytes is the encoded size of every row.
	Bytes int64 `json:"bytes"`
}

// CheckpointStats reports the size of the checkpoint table.
func (s *Store) CheckpointStats(ctx context.Context) (CheckpointStats, error) {
	rows, err := s.db.SQLDB().QueryContext(ctx,
		`SELECT exec_id, length(CAST(data AS BLOB)) FROM agent_checkpoints`)
	if err != nil {
		return CheckpointStats{}, fmt.Errorf("state: checkpoint stats: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "state: close checkpoint rows failed", rows.Close())
	}()
	var stats CheckpointStats
	for rows.Next() {
		var id string
		var size sql.NullInt64
		if err := rows.Scan(&id, &size); err != nil {
			return CheckpointStats{}, fmt.Errorf("state: checkpoint stats: %w", err)
		}
		stats.Rows++
		stats.Bytes += size.Int64
		if ids.IsRun(id) {
			stats.Runs++
		}
	}
	if err := rows.Err(); err != nil {
		return CheckpointStats{}, fmt.Errorf("state: checkpoint stats: %w", err)
	}
	return stats, nil
}

// Save implements agent.CheckpointStore: an atomic upsert keyed by exec
// id; the later of two overlapping saves wins.
func (s *Store) Save(ctx context.Context, cp agent.Checkpoint) error {
	if err := cp.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(cp.Clone())
	if err != nil {
		return fmt.Errorf("state: encode checkpoint: %w", err)
	}
	_, err = s.db.SQLDB().ExecContext(ctx, `
		INSERT INTO agent_checkpoints (exec_id, data, revision, saved_at)
		VALUES (?, ?, 1, ?)
		ON CONFLICT(exec_id) DO UPDATE SET
			data = excluded.data,
			revision = agent_checkpoints.revision + 1,
			saved_at = excluded.saved_at
	`, cp.ExecID, string(data), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("state: save checkpoint %s: %w", cp.ExecID, err)
	}
	return nil
}

// Load implements agent.CheckpointStore. A missing record returns
// (nil, nil); the returned checkpoint is caller-owned.
//
// A record is a base plus its append-only chain (see
// checkpoint_journal.go): the chain is folded in before the caller sees
// the record, so a reader never learns whether it cost one row or many.
func (s *Store) Load(ctx context.Context, execID string) (*agent.Checkpoint, error) {
	if err := validateExecID(execID); err != nil {
		return nil, err
	}
	return s.loadCheckpoint(ctx, execID)
}

// List implements agent.CheckpointLister.
func (s *Store) List(ctx context.Context) ([]string, error) {
	rows, err := s.db.SQLDB().QueryContext(ctx,
		`SELECT exec_id FROM agent_checkpoints ORDER BY exec_id`)
	if err != nil {
		return nil, fmt.Errorf("state: list checkpoints: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "state: close checkpoint rows failed", rows.Close())
	}()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("state: list checkpoints: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list checkpoints: %w", err)
	}
	return ids, nil
}

// Delete implements agent.CheckpointDeleter: the base record and its chain
// go together, so a chain never outlives the base it extends.
func (s *Store) Delete(ctx context.Context, execID string) error {
	if err := validateExecID(execID); err != nil {
		return err
	}
	return s.deleteCheckpoint(ctx, execID)
}

var (
	_ agent.CheckpointStore   = (*Store)(nil)
	_ agent.CheckpointLister  = (*Store)(nil)
	_ agent.CheckpointDeleter = (*Store)(nil)
)
