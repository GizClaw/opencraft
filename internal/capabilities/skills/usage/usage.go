// Package usage owns the skill lifecycle tables in ~/.opencraft/user.db:
// one append-only row per skill activation, the user's per-skill
// decisions (pin), and the archive records that make a retired skill
// restorable. The desktop and headless hosts migrate user.db through
// internal/foundation/compat and then Attach binds this store.
//
// Recording is best-effort by contract: a usage write must never fail a
// turn, so callers log and continue.
package usage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// timeLayout is the RFC3339 storage format for all timestamps.
const timeLayout = time.RFC3339

// Event is one skill activation: the skill was injected into a turn (a
// $mention or a ranked hit) or read through the skill_read tool.
type Event struct {
	Name           string    `json:"name"`
	Scope          string    `json:"scope,omitempty"`
	UsedAt         time.Time `json:"used_at"`
	RunID          string    `json:"run_id,omitempty"`
	ConversationID string    `json:"conversation_id,omitempty"`
}

// Stat aggregates one skill's usage. Scope is the most recently seen
// scope (empty when every event predates scope recording).
type Stat struct {
	Name     string    `json:"name"`
	Scope    string    `json:"scope,omitempty"`
	Uses     int       `json:"uses"`
	LastUsed time.Time `json:"last_used"`
}

// State is the user's stored decision about one skill. Pinned skills
// are never curator candidates; retired skills stay on disk and stay
// restorable but are not injected or mentioned any more.
type State struct {
	Name      string    `json:"name"`
	Scope     string    `json:"scope,omitempty"`
	Pinned    bool      `json:"pinned"`
	Retired   bool      `json:"retired"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Archive records one archived skill directory: the tar.gz snapshot and
// the SKILL.md path it came from. Archived skills are never deleted by
// the curator, so a restore is always possible.
type Archive struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Scope       string    `json:"scope,omitempty"`
	SkillPath   string    `json:"skill_path"`
	ArchivePath string    `json:"archive_path"`
	CreatedAt   time.Time `json:"created_at"`
	RestoredAt  time.Time `json:"restored_at,omitzero"`
}

// Store persists skill usage, decisions and archives in the shared user
// database.
type Store struct {
	db *sql.DB
	// now is swappable in tests.
	now func() time.Time
}

// Attach binds the usage store to an existing foundation/db handle that
// internal/foundation/compat.User already migrated.
func Attach(handle *db.DB) (*Store, error) {
	if handle == nil {
		return nil, fmt.Errorf("skills/usage: nil database")
	}
	return &Store{db: handle.SQLDB(), now: func() time.Time {
		return time.Now().UTC()
	}}, nil
}

// Recorder is the write surface the skills service, the worldstate hook
// and the skill_read tool consume. Empty reports "no user database in
// this runtime": recording becomes a no-op and the skills page shows no
// statistics.
type Recorder interface {
	Record(ctx context.Context, event Event) error
	Empty() bool
}

// Lifecycle is the full surface the curator, the skills registry and
// the skills page consume: the recording half above plus the stored
// decisions, the derived statistics and the archive records.
type Lifecycle interface {
	Recorder
	Stats(ctx context.Context, since time.Time) ([]Stat, error)
	States(ctx context.Context) (map[string]State, error)
	SetPinned(ctx context.Context, name, scope string, pinned bool) (State, error)
	SetRetired(ctx context.Context, name, scope string, retired bool) (State, error)
	Retired(ctx context.Context) (map[string]bool, error)
	Prune(ctx context.Context, before time.Time) (int64, error)
	RecordArchive(ctx context.Context, archive Archive) error
	ListArchives(ctx context.Context) ([]Archive, error)
	GetArchive(ctx context.Context, id string) (Archive, error)
	MarkRestored(ctx context.Context, id string) error
}

// Compile-time assertions the store satisfies both consumed surfaces.
var (
	_ Recorder  = (*Store)(nil)
	_ Lifecycle = (*Store)(nil)
)

// Empty reports that this store has a user database behind it.
func (s *Store) Empty() bool { return false }

// emptyStore is the implementation used by runtimes without a user
// database: every write is a no-op and every read is empty.
type emptyStore struct{}

// EmptyRecorder returns the recorder of a runtime that has no user
// database. Every write is a no-op.
func EmptyRecorder() Recorder { return emptyStore{} }

// EmptyLifecycle returns the lifecycle surface of a runtime that has no
// user database: no usage is recorded, no skill is ever retired and the
// archive list stays empty.
func EmptyLifecycle() Lifecycle { return emptyStore{} }

func (emptyStore) Record(context.Context, Event) error { return nil }

func (emptyStore) Empty() bool { return true }

func (emptyStore) Stats(context.Context, time.Time) ([]Stat, error) { return nil, nil }

func (emptyStore) States(context.Context) (map[string]State, error) {
	return nil, nil
}

func (emptyStore) SetPinned(
	_ context.Context, name, scope string, pinned bool,
) (State, error) {
	return State{Name: name, Scope: scope, Pinned: pinned}, nil
}

func (emptyStore) SetRetired(
	_ context.Context, name, scope string, retired bool,
) (State, error) {
	return State{Name: name, Scope: scope, Retired: retired}, nil
}

func (emptyStore) Retired(context.Context) (map[string]bool, error) { return nil, nil }

func (emptyStore) Prune(context.Context, time.Time) (int64, error) { return 0, nil }

func (emptyStore) RecordArchive(context.Context, Archive) error { return nil }

func (emptyStore) ListArchives(context.Context) ([]Archive, error) { return nil, nil }

func (emptyStore) GetArchive(context.Context, string) (Archive, error) {
	return Archive{}, errdefs.NotFoundf("skills/usage: archive not found")
}

func (emptyStore) MarkRestored(context.Context, string) error { return nil }

// Record appends one activation. A row without a skill name is
// dropped.
func (s *Store) Record(ctx context.Context, event Event) error {
	name := strings.TrimSpace(event.Name)
	if name == "" {
		return nil
	}
	at := event.UsedAt
	if at.IsZero() {
		at = s.now()
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO skill_usage(name, scope, used_at, run_id, conversation_id)
		VALUES (?, ?, ?, ?, ?)`,
		name, strings.TrimSpace(event.Scope),
		at.UTC().Format(timeLayout),
		event.RunID, event.ConversationID,
	); err != nil {
		return fmt.Errorf("skills/usage: record %s: %w", name, err)
	}
	return nil
}

// Stats aggregates one row per skill, most used first and most recently
// used as the tie-break. since bounds the window (zero = all events).
func (s *Store) Stats(ctx context.Context, since time.Time) ([]Stat, error) {
	var (
		conds string
		args  []any
	)
	if !since.IsZero() {
		conds = " WHERE used_at >= ?"
		args = append(args, since.UTC().Format(timeLayout))
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT name, COUNT(*), MAX(used_at) AS last_used
		FROM skill_usage`+conds+`
		GROUP BY name
		ORDER BY COUNT(*) DESC, last_used DESC, name`, args...)
	if err != nil {
		return nil, fmt.Errorf("skills/usage: stats: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "skills/usage: close stat rows failed", rows.Close())
	}()
	var out []Stat
	for rows.Next() {
		var (
			stat     Stat
			lastUsed string
		)
		if err := rows.Scan(&stat.Name, &stat.Uses, &lastUsed); err != nil {
			return nil, fmt.Errorf("skills/usage: scan stat: %w", err)
		}
		if at, err := time.Parse(timeLayout, lastUsed); err == nil {
			stat.LastUsed = at
		}
		out = append(out, stat)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("skills/usage: stats: %w", err)
	}
	return out, nil
}

// States returns every stored decision, keyed by skill name.
func (s *Store) States(ctx context.Context) (map[string]State, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, scope, pinned, retired, updated_at FROM skill_state`)
	if err != nil {
		return nil, fmt.Errorf("skills/usage: states: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "skills/usage: close state rows failed", rows.Close())
	}()
	out := map[string]State{}
	for rows.Next() {
		var (
			state   State
			pinned  int
			retired int
			updated string
		)
		if err := rows.Scan(
			&state.Name, &state.Scope, &pinned, &retired, &updated,
		); err != nil {
			return nil, fmt.Errorf("skills/usage: scan state: %w", err)
		}
		state.Pinned = pinned != 0
		state.Retired = retired != 0
		if at, err := time.Parse(timeLayout, updated); err == nil {
			state.UpdatedAt = at
		}
		out[state.Name] = state
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("skills/usage: states: %w", err)
	}
	return out, nil
}

// Retired returns the names of every retired skill. The skills registry
// reads it once per turn: a retired skill keeps its files (and its
// archive record) but is no longer offered to the model.
func (s *Store) Retired(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name FROM skill_state WHERE retired = 1`)
	if err != nil {
		return nil, fmt.Errorf("skills/usage: retired: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "skills/usage: close retired rows failed", rows.Close())
	}()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("skills/usage: scan retired: %w", err)
		}
		out[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("skills/usage: retired: %w", err)
	}
	return out, nil
}

// SetPinned stores one skill's pin decision. Pinning an unpinned skill
// keeps its previous scope when the caller passes an empty one.
func (s *Store) SetPinned(
	ctx context.Context, name, scope string, pinned bool,
) (State, error) {
	return s.setDecision(ctx, name, scope, func(state *State, now time.Time) {
		state.Pinned = pinned
		state.UpdatedAt = now
	})
}

// SetRetired retires or restores one skill. A retired skill stays on
// disk (and keeps its archive record) but drops out of the injected
// skills list and of $mention activation.
func (s *Store) SetRetired(
	ctx context.Context, name, scope string, retired bool,
) (State, error) {
	return s.setDecision(ctx, name, scope, func(state *State, now time.Time) {
		state.Retired = retired
		state.UpdatedAt = now
	})
}

// setDecision applies one user decision to skill_state, creating the
// row when it is the first one for this skill. The two decisions share
// this path so a pin and a retire can never fight over the row.
func (s *Store) setDecision(
	ctx context.Context,
	name, scope string,
	mutate func(state *State, now time.Time),
) (State, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return State{}, errdefs.Validationf("skills/usage: skill name is required")
	}
	now := s.now()
	stored, err := s.state(ctx, name)
	if err != nil {
		return State{}, err
	}
	// The mutation starts from the stored row, not from zero: pinning
	// and retiring are independent decisions that share one row, so a
	// call that only sets one of them must carry the other through.
	state := stored
	state.Name = name
	if trimmed := strings.TrimSpace(scope); trimmed != "" {
		state.Scope = trimmed
	}
	mutate(&state, now)
	pinned := boolInt(state.Pinned)
	retired := boolInt(state.Retired)
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO skill_state(name, scope, pinned, retired, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			scope = CASE WHEN excluded.scope = '' THEN scope ELSE excluded.scope END,
			pinned = excluded.pinned,
			retired = excluded.retired,
			updated_at = excluded.updated_at`,
		state.Name, state.Scope, pinned, retired, now.Format(timeLayout),
	); err != nil {
		return State{}, fmt.Errorf("skills/usage: decide %s: %w", name, err)
	}
	return state, nil
}

func (s *Store) state(ctx context.Context, name string) (State, error) {
	var (
		state   State
		pinned  int
		retired int
		updated string
	)
	row := s.db.QueryRowContext(ctx,
		`SELECT name, scope, pinned, retired, updated_at
		 FROM skill_state WHERE name = ?`, name)
	if err := row.Scan(
		&state.Name, &state.Scope, &pinned, &retired, &updated,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return State{Name: name}, nil
		}
		return State{}, fmt.Errorf("skills/usage: read state %s: %w", name, err)
	}
	state.Pinned = pinned != 0
	state.Retired = retired != 0
	if at, err := time.Parse(timeLayout, updated); err == nil {
		state.UpdatedAt = at
	}
	return state, nil
}

// Prune drops usage events older than before and returns how many rows
// went away. The curator calls it so the table cannot grow without
// bound; aggregates the page needs are all within the window.
func (s *Store) Prune(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM skill_usage WHERE used_at < ?`,
		before.UTC().Format(timeLayout))
	if err != nil {
		return 0, fmt.Errorf("skills/usage: prune: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("skills/usage: prune: %w", err)
	}
	return affected, nil
}

// RecordArchive stores one archive record.
func (s *Store) RecordArchive(ctx context.Context, archive Archive) error {
	if strings.TrimSpace(archive.ID) == "" ||
		strings.TrimSpace(archive.Name) == "" ||
		strings.TrimSpace(archive.ArchivePath) == "" {
		return errdefs.Validationf(
			"skills/usage: archive needs an id, a name and a path")
	}
	at := archive.CreatedAt
	if at.IsZero() {
		at = s.now()
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO skill_archive(
			id, name, scope, skill_path, archive_path, created_at, restored_at
		) VALUES (?, ?, ?, ?, ?, ?, '')`,
		archive.ID, archive.Name, archive.Scope, archive.SkillPath,
		archive.ArchivePath, at.UTC().Format(timeLayout),
	); err != nil {
		return fmt.Errorf("skills/usage: record archive %s: %w", archive.Name, err)
	}
	return nil
}

// ListArchives returns every archive record, newest first.
func (s *Store) ListArchives(ctx context.Context) ([]Archive, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, scope, skill_path, archive_path, created_at, restored_at
		FROM skill_archive ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("skills/usage: list archives: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "skills/usage: close archive rows failed", rows.Close())
	}()
	var out []Archive
	for rows.Next() {
		var (
			archive             Archive
			created, restoredAt string
		)
		if err := rows.Scan(
			&archive.ID, &archive.Name, &archive.Scope, &archive.SkillPath,
			&archive.ArchivePath, &created, &restoredAt,
		); err != nil {
			return nil, fmt.Errorf("skills/usage: scan archive: %w", err)
		}
		if at, err := time.Parse(timeLayout, created); err == nil {
			archive.CreatedAt = at
		}
		if at, err := time.Parse(timeLayout, restoredAt); err == nil {
			archive.RestoredAt = at
		}
		out = append(out, archive)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("skills/usage: list archives: %w", err)
	}
	return out, nil
}

// MarkRestored stamps one archive record as restored.
func (s *Store) MarkRestored(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE skill_archive SET restored_at = ? WHERE id = ?`,
		s.now().Format(timeLayout), id)
	if err != nil {
		return fmt.Errorf("skills/usage: mark restored %s: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("skills/usage: mark restored %s: %w", id, err)
	}
	if affected == 0 {
		return errdefs.NotFoundf("skills/usage: archive %s not found", id)
	}
	return nil
}

// GetArchive reads one archive record by id.
func (s *Store) GetArchive(ctx context.Context, id string) (Archive, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, scope, skill_path, archive_path, created_at, restored_at
		FROM skill_archive WHERE id = ?`, id)
	var (
		archive             Archive
		created, restoredAt string
	)
	if err := row.Scan(
		&archive.ID, &archive.Name, &archive.Scope, &archive.SkillPath,
		&archive.ArchivePath, &created, &restoredAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Archive{}, errdefs.NotFoundf(
				"skills/usage: archive %s not found", id)
		}
		return Archive{}, fmt.Errorf("skills/usage: read archive %s: %w", id, err)
	}
	if at, err := time.Parse(timeLayout, created); err == nil {
		archive.CreatedAt = at
	}
	if at, err := time.Parse(timeLayout, restoredAt); err == nil {
		archive.RestoredAt = at
	}
	return archive, nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
