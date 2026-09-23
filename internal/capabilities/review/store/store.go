// Package store owns the post-turn review queue in ~/.opencraft/user.db:
// suggestions a background review produced that wait for the user's
// verdict. Rows are never applied from here — accepting one runs the
// same write path as the remember tool, and the row is kept as a record
// of what was accepted or discarded.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// Suggestion statuses.
const (
	StatusPending   = "pending"
	StatusAccepted  = "accepted"
	StatusDiscarded = "discarded"
)

// Suggestion kinds. Only "memory" exists today; the column keeps room
// for a skill suggestion without another migration.
const KindMemory = "memory"

// ErrNotFound is returned when a suggestion id has no row.
var ErrNotFound = errors.New("review: suggestion not found")

// timeLayout is the RFC3339 storage format for all timestamps.
const timeLayout = time.RFC3339

// Suggestion is one queued write-back candidate.
type Suggestion struct {
	ID                 string          `json:"id"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at,omitzero"`
	Status             string          `json:"status"`
	Kind               string          `json:"kind"`
	Payload            json.RawMessage `json:"payload"`
	Reason             string          `json:"reason,omitempty"`
	SourceWorkspace    string          `json:"source_workspace,omitempty"`
	SourceConversation string          `json:"source_conversation,omitempty"`
	SourceRun          string          `json:"source_run,omitempty"`
}

// Store persists review suggestions in the shared user database.
type Store struct {
	db *sql.DB
	// now is swappable in tests.
	now func() time.Time
}

// Attach binds the queue to an existing foundation/db handle that
// internal/foundation/compat.User already migrated.
func Attach(handle *db.DB) (*Store, error) {
	if handle == nil {
		return nil, fmt.Errorf("review: nil database")
	}
	return &Store{db: handle.SQLDB(), now: func() time.Time {
		return time.Now().UTC()
	}}, nil
}

// Queue is the surface the review hook (write side) and the settings
// bindings (read/decide side) consume. Empty reports "no user database
// in this runtime": the review hook stores nothing and the page shows
// an empty queue.
type Queue interface {
	Create(ctx context.Context, suggestion Suggestion) (Suggestion, error)
	List(ctx context.Context, status string, limit int) ([]Suggestion, error)
	Get(ctx context.Context, id string) (Suggestion, error)
	SetStatus(ctx context.Context, id, status string) (Suggestion, error)
	Count(ctx context.Context, status string) (int, error)
	Empty() bool
}

// Compile-time assertion the store satisfies the consumed surface.
var _ Queue = (*Store)(nil)

// Empty reports that this store has a user database behind it.
func (s *Store) Empty() bool { return false }

// emptyQueue is the Queue implementation for runtimes without a user
// database.
type emptyQueue struct{}

// Empty returns the queue of a runtime that has no user database.
func Empty() Queue { return emptyQueue{} }

func (emptyQueue) Empty() bool { return true }

func (emptyQueue) Create(context.Context, Suggestion) (Suggestion, error) {
	return Suggestion{}, errdefs.NotAvailablef("review: no user database in this runtime")
}

func (emptyQueue) List(context.Context, string, int) ([]Suggestion, error) {
	return nil, nil
}

func (emptyQueue) Get(context.Context, string) (Suggestion, error) {
	return Suggestion{}, errdefs.NotAvailablef("review: no user database in this runtime")
}

func (emptyQueue) SetStatus(context.Context, string, string) (Suggestion, error) {
	return Suggestion{}, errdefs.NotAvailablef("review: no user database in this runtime")
}

func (emptyQueue) Count(context.Context, string) (int, error) { return 0, nil }

// ValidStatus reports whether status is one of the queue statuses.
func ValidStatus(status string) bool {
	switch status {
	case StatusPending, StatusAccepted, StatusDiscarded:
		return true
	default:
		return false
	}
}

// Create stores one suggestion. The caller supplies a stable Id when it
// has one (the review run derives it from conversation + run + index),
// so replaying a review cannot queue the same candidate twice.
func (s *Store) Create(
	ctx context.Context, suggestion Suggestion,
) (Suggestion, error) {
	if strings.TrimSpace(suggestion.Kind) == "" {
		return Suggestion{}, errdefs.Validationf("review: suggestion kind is required")
	}
	if len(suggestion.Payload) == 0 || !json.Valid(suggestion.Payload) {
		return Suggestion{}, errdefs.Validationf(
			"review: suggestion payload must be valid JSON")
	}
	if suggestion.Status == "" {
		suggestion.Status = StatusPending
	}
	if !ValidStatus(suggestion.Status) {
		return Suggestion{}, errdefs.Validationf(
			"review: unknown status %q", suggestion.Status)
	}
	now := s.now()
	if suggestion.CreatedAt.IsZero() {
		suggestion.CreatedAt = now
	}
	suggestion.UpdatedAt = now
	if suggestion.ID == "" {
		id, err := newID()
		if err != nil {
			return Suggestion{}, err
		}
		suggestion.ID = id
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO review_suggestions(
			id, created_at, updated_at, status, kind, payload_json, reason,
			source_workspace, source_conversation, source_run
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		suggestion.ID, suggestion.CreatedAt.UTC().Format(timeLayout),
		suggestion.UpdatedAt.UTC().Format(timeLayout),
		suggestion.Status, suggestion.Kind, string(suggestion.Payload),
		suggestion.Reason, suggestion.SourceWorkspace,
		suggestion.SourceConversation, suggestion.SourceRun,
	); err != nil {
		return Suggestion{}, fmt.Errorf("review: create suggestion: %w", err)
	}
	return suggestion, nil
}

// List returns suggestions in one status, newest first. An empty status
// lists every row.
func (s *Store) List(
	ctx context.Context, status string, limit int,
) ([]Suggestion, error) {
	var (
		conds []string
		args  []any
	)
	if status != "" {
		conds = append(conds, "status = ?")
		args = append(args, status)
	}
	sqlText := `SELECT id, created_at, updated_at, status, kind, payload_json,
		       reason, source_workspace, source_conversation, source_run
		FROM review_suggestions`
	if len(conds) > 0 {
		sqlText += " WHERE " + strings.Join(conds, " AND ")
	}
	sqlText += " ORDER BY created_at DESC, id"
	if limit > 0 {
		sqlText += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("review: list suggestions: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "review: close suggestion rows failed", rows.Close())
	}()
	var out []Suggestion
	for rows.Next() {
		suggestion, err := scanSuggestion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, suggestion)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("review: list suggestions: %w", err)
	}
	return out, nil
}

// Get reads one suggestion by id.
func (s *Store) Get(ctx context.Context, id string) (Suggestion, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, created_at, updated_at, status,
		       kind, payload_json, reason, source_workspace,
		       source_conversation, source_run
		FROM review_suggestions WHERE id = ?`, id)
	suggestion, err := scanSuggestion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Suggestion{}, ErrNotFound
	}
	if err != nil {
		return Suggestion{}, err
	}
	return suggestion, nil
}

// SetStatus records the user's verdict on one suggestion. Only pending
// rows can move: a decided row stays decided, so a repeated click (or a
// retry) cannot flip an accepted suggestion into a discarded one.
func (s *Store) SetStatus(ctx context.Context, id, status string) (Suggestion, error) {
	if !ValidStatus(status) || status == StatusPending {
		return Suggestion{}, errdefs.Validationf(
			"review: %q is not a decision status", status)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE review_suggestions SET status = ?, updated_at = ?
		WHERE id = ? AND status = ?`,
		status, s.now().Format(timeLayout), id, StatusPending)
	if err != nil {
		return Suggestion{}, fmt.Errorf("review: set status %s: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return Suggestion{}, fmt.Errorf("review: set status %s: %w", id, err)
	}
	suggestion, err := s.Get(ctx, id)
	if err != nil {
		return Suggestion{}, err
	}
	if affected == 0 && suggestion.Status == StatusPending {
		return Suggestion{}, ErrNotFound
	}
	return suggestion, nil
}

// Count returns how many suggestions carry one status (empty status:
// every row).
func (s *Store) Count(ctx context.Context, status string) (int, error) {
	var (
		sqlText = "SELECT COUNT(*) FROM review_suggestions"
		args    []any
	)
	if status != "" {
		sqlText += " WHERE status = ?"
		args = append(args, status)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, sqlText, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("review: count suggestions: %w", err)
	}
	return n, nil
}

func scanSuggestion(rows interface{ Scan(...any) error }) (Suggestion, error) {
	var (
		suggestion       Suggestion
		created, updated string
		payload          string
	)
	if err := rows.Scan(
		&suggestion.ID, &created, &updated, &suggestion.Status,
		&suggestion.Kind, &payload, &suggestion.Reason,
		&suggestion.SourceWorkspace, &suggestion.SourceConversation,
		&suggestion.SourceRun,
	); err != nil {
		return Suggestion{}, fmt.Errorf("review: scan suggestion: %w", err)
	}
	if at, err := time.Parse(timeLayout, created); err == nil {
		suggestion.CreatedAt = at
	}
	if at, err := time.Parse(timeLayout, updated); err == nil {
		suggestion.UpdatedAt = at
	}
	suggestion.Payload = json.RawMessage(payload)
	return suggestion, nil
}

// newID mints one suggestion id when the caller has no stable key.
func newID() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("review: generate id: %w", err)
	}
	return "rs-" + hex.EncodeToString(raw[:]), nil
}
