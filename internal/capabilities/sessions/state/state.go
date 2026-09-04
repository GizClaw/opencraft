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
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// Store is a SQLite-backed opencraft state store. Open creates or
// opens the database handle without applying migrations; the central
// orchestration/migrations package owns every schema step and must run
// Workspace before the store is used.
type Store struct {
	db     *db.DB
	ownsDB bool
}

// Open opens (creating if necessary) the SQLite database at path.
// Callers must apply orchestration/migrations.WorkspaceSchema before
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
func (s *Store) Load(ctx context.Context, execID string) (*agent.Checkpoint, error) {
	if strings.TrimSpace(execID) == "" {
		return nil, errdefs.Validation(
			errors.New("state: checkpoint exec_id is required"))
	}
	var data string
	err := s.db.SQLDB().QueryRowContext(ctx,
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
	return &cp, nil
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

// Delete implements agent.CheckpointDeleter.
func (s *Store) Delete(ctx context.Context, execID string) error {
	if strings.TrimSpace(execID) == "" {
		return errdefs.Validation(
			errors.New("state: checkpoint exec_id is required"))
	}
	if _, err := s.db.SQLDB().ExecContext(ctx,
		`DELETE FROM agent_checkpoints WHERE exec_id = ?`, execID); err != nil {
		return fmt.Errorf("state: delete checkpoint %s: %w", execID, err)
	}
	return nil
}

var (
	_ agent.CheckpointStore   = (*Store)(nil)
	_ agent.CheckpointLister  = (*Store)(nil)
	_ agent.CheckpointDeleter = (*Store)(nil)
)

// SetThinkLevel upserts the per-session reasoning effort
// (low | medium | high) into the session_settings table.
func (s *Store) SetThinkLevel(ctx context.Context, contextID, level string) error {
	if strings.TrimSpace(contextID) == "" {
		return errdefs.Validation(
			errors.New("state: session context_id is required"))
	}
	if strings.TrimSpace(level) == "" {
		return errdefs.Validation(
			errors.New("state: think level is required"))
	}
	_, err := s.db.SQLDB().ExecContext(ctx, `
		INSERT INTO session_settings (context_id, think_level, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(context_id) DO UPDATE SET
			think_level = excluded.think_level,
			updated_at = excluded.updated_at
	`, contextID, level, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("state: set think level %s: %w", contextID, err)
	}
	return nil
}

// ThinkLevel returns the persisted reasoning effort for a session.
// A missing row returns "", letting the caller apply its default.
func (s *Store) ThinkLevel(ctx context.Context, contextID string) (string, error) {
	var level string
	err := s.db.SQLDB().QueryRowContext(ctx,
		`SELECT think_level FROM session_settings WHERE context_id = ?`,
		contextID,
	).Scan(&level)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("state: load think level %s: %w", contextID, err)
	}
	return level, nil
}

// SetModel upserts the per-session model hint ("provider/name", or an
// empty string for the default routing policy) into the
// session_settings table.
func (s *Store) SetModel(ctx context.Context, contextID, model string) error {
	if strings.TrimSpace(contextID) == "" {
		return errdefs.Validation(
			errors.New("state: session context_id is required"))
	}
	_, err := s.db.SQLDB().ExecContext(ctx, `
		INSERT INTO session_settings (context_id, think_level, model, updated_at)
		VALUES (?, '', ?, ?)
		ON CONFLICT(context_id) DO UPDATE SET
			model = excluded.model,
			updated_at = excluded.updated_at
	`, contextID, model, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("state: set model %s: %w", contextID, err)
	}
	return nil
}

// Model returns the persisted model hint for a session. A missing row
// (or a session that never set one) returns "", meaning the default
// routing policy applies.
func (s *Store) Model(ctx context.Context, contextID string) (string, error) {
	var model string
	err := s.db.SQLDB().QueryRowContext(ctx,
		`SELECT model FROM session_settings WHERE context_id = ?`,
		contextID,
	).Scan(&model)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("state: load model %s: %w", contextID, err)
	}
	return model, nil
}

// RemoveSettings deletes one session's settings row (think level and
// model hint). Deleting an unknown session is a no-op.
func (s *Store) RemoveSettings(ctx context.Context, contextID string) error {
	if _, err := s.db.SQLDB().ExecContext(ctx,
		`DELETE FROM session_settings WHERE context_id = ?`, contextID); err != nil {
		return fmt.Errorf("state: remove settings %s: %w", contextID, err)
	}
	return nil
}

// SetMode upserts the per-session sandbox permission mode into the
// session_settings table.
func (s *Store) SetMode(ctx context.Context, contextID, mode string) error {
	if strings.TrimSpace(contextID) == "" {
		return errdefs.Validation(
			errors.New("state: session context_id is required"))
	}
	if strings.TrimSpace(mode) == "" {
		return errdefs.Validation(
			errors.New("state: mode is required"))
	}
	_, err := s.db.SQLDB().ExecContext(ctx, `
		INSERT INTO session_settings (context_id, think_level, model, mode, updated_at)
		VALUES (?, '', '', ?, ?)
		ON CONFLICT(context_id) DO UPDATE SET
			mode = excluded.mode,
			updated_at = excluded.updated_at
	`, contextID, mode, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("state: set mode %s: %w", contextID, err)
	}
	return nil
}

// Mode returns the persisted sandbox permission mode for a session.
// A missing row returns "", letting the caller apply its default.
func (s *Store) Mode(ctx context.Context, contextID string) (string, error) {
	var mode string
	err := s.db.SQLDB().QueryRowContext(ctx,
		`SELECT mode FROM session_settings WHERE context_id = ?`,
		contextID,
	).Scan(&mode)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("state: load mode %s: %w", contextID, err)
	}
	return mode, nil
}
