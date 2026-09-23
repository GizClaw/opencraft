// Package userstore owns the user-level long-term memory table in
// ~/.opencraft/user.db: facts that outlive a conversation (preferences,
// environment, project conventions). The desktop and headless hosts
// migrate user.db through internal/foundation/compat and then Attach
// binds this store to the shared handle.
//
// The store is the single write point for facts: the remember tool, the
// settings page and an accepted review suggestion all call Add/Replace
// so dedupe, limits and provenance are applied exactly once.
package userstore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// Limits enforced by the store. They are a safety rail rather than a
// user preference: one fact is a sentence or two, and the injected
// window is bounded separately (see the worldstate user-memory
// section), so a runaway writer must not be able to grow either.
const (
	// MaxItems is the hard cap on live (non-retired) facts.
	MaxItems = 200
	// MaxTextBytes caps one fact's text in bytes.
	MaxTextBytes = 4096
)

// Scopes a fact can carry.
const (
	ScopeGlobal    = "global"
	ScopeWorkspace = "workspace"
)

// ErrNotFound is returned when a fact id has no row.
var ErrNotFound = errors.New("userstore: memory not found")

// timeLayout is the storage format for all timestamps. The fractional
// second is always written at a fixed nine-digit width so the text
// sorts lexicographically in time order: the injected memory section is
// ordered by updated_at DESC and SQLite compares the stored strings
// byte-wise, but RFC3339Nano trims trailing zeros and a trimmed
// fraction sorts after a longer one in the same second (".5Z" > ".51Z"),
// which inverts two facts the remember tool wrote as one batch.
const timeLayout = "2006-01-02T15:04:05.000000000Z07:00"

// timeReadLayout parses every timestamp any version stored: the fixed
// nine-digit fraction above and the trimmed RFC3339Nano rows (down to
// the second resolution) older builds wrote. time.Parse treats the
// layout's fractional field as optional, so one layout reads them all.
const timeReadLayout = time.RFC3339Nano

// Fact is one stored memory row.
type Fact struct {
	ID                 string    `json:"id"`
	Kind               string    `json:"kind"`
	Scope              string    `json:"scope"`
	Workspace          string    `json:"workspace,omitempty"`
	Text               string    `json:"text"`
	DedupeKey          string    `json:"-"`
	SourceConversation string    `json:"source_conversation,omitempty"`
	SourceRun          string    `json:"source_run,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	Stale              bool      `json:"stale"`
}

// Query filters one List call.
type Query struct {
	// Workspace selects the workspace whose facts are wanted. It also
	// unlocks scope='workspace' rows; global rows are always included.
	Workspace string
	// IncludeStale returns retired facts too.
	IncludeStale bool
	// Limit caps the returned rows (0 = no cap).
	Limit int
}

// Store persists user-level facts in the shared user database.
type Store struct {
	db *sql.DB
	// now is swappable in tests.
	now func() time.Time
}

// Attach binds the memory store to an existing foundation/db handle
// that internal/foundation/compat.User already migrated.
func Attach(handle *db.DB) (*Store, error) {
	if handle == nil {
		return nil, fmt.Errorf("userstore: nil database")
	}
	return &Store{db: handle.SQLDB(), now: func() time.Time {
		return time.Now().UTC()
	}}, nil
}

// Memory is the write/read surface the remember tool, the worldstate
// injection and the settings bindings consume. Empty reports
// "no user database in this runtime": callers degrade instead of
// failing (no tool contribution, no injected section).
type Memory interface {
	Add(ctx context.Context, fact Fact) (Fact, error)
	Replace(ctx context.Context, id, text string) (Fact, error)
	Remove(ctx context.Context, id string) error
	SetStale(ctx context.Context, id string, stale bool) (Fact, error)
	List(ctx context.Context, query Query) ([]Fact, error)
	Get(ctx context.Context, id string) (Fact, error)
	Empty() bool
}

// Compile-time assertion the store satisfies the consumed surface.
var _ Memory = (*Store)(nil)

// Empty reports that this store has a user database behind it.
func (s *Store) Empty() bool { return false }

// emptyMemory is the Memory implementation for runtimes without a user
// database (a CLI run that could not open one, tests). Reads return
// nothing, writes report the missing database.
type emptyMemory struct{}

// Empty returns the memory implementation of a runtime that has no
// user database. It contributes no tools and injects no section.
func Empty() Memory { return emptyMemory{} }

func (emptyMemory) Empty() bool { return true }

func (emptyMemory) Add(context.Context, Fact) (Fact, error) {
	return Fact{}, errdefs.NotAvailablef("memory: no user database in this runtime")
}

func (emptyMemory) Replace(context.Context, string, string) (Fact, error) {
	return Fact{}, errdefs.NotAvailablef("memory: no user database in this runtime")
}

func (emptyMemory) Remove(context.Context, string) error {
	return errdefs.NotAvailablef("memory: no user database in this runtime")
}

func (emptyMemory) SetStale(context.Context, string, bool) (Fact, error) {
	return Fact{}, errdefs.NotAvailablef("memory: no user database in this runtime")
}

func (emptyMemory) List(context.Context, Query) ([]Fact, error) { return nil, nil }

func (emptyMemory) Get(context.Context, string) (Fact, error) {
	return Fact{}, errdefs.NotAvailablef("memory: no user database in this runtime")
}

// Add stores one fact. A fact whose (scope, workspace, dedupe key)
// already exists updates that row's text and kind instead of adding a
// twin, so restating the same fact is idempotent. Text is normalized
// for the dedupe key (case, surrounding whitespace, internal runs of
// whitespace) but stored as typed.
func (s *Store) Add(ctx context.Context, fact Fact) (Fact, error) {
	normalized, err := normalizeFact(fact)
	if err != nil {
		return Fact{}, err
	}
	now := s.now()
	if normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = now
	}
	normalized.UpdatedAt = now
	if normalized.ID == "" {
		id, err := newID()
		if err != nil {
			return Fact{}, err
		}
		normalized.ID = id
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Fact{}, fmt.Errorf("userstore: begin add: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			telemetry.WarnErr(ctx, "userstore: rollback add failed", err)
		}
	}()

	existing, found, err := findByDedupe(
		ctx, tx, normalized.Scope, normalized.Workspace, normalized.DedupeKey)
	if err != nil {
		return Fact{}, err
	}
	if found {
		// The row keeps its id and creation time: the fact is the
		// same fact, restated. An empty kind or source in the new
		// call never erases what the original write recorded.
		existing.Text = normalized.Text
		existing.Kind = firstNonEmpty(normalized.Kind, existing.Kind)
		existing.SourceConversation = firstNonEmpty(
			normalized.SourceConversation, existing.SourceConversation)
		existing.SourceRun = firstNonEmpty(
			normalized.SourceRun, existing.SourceRun)
		existing.Stale = false
		existing.UpdatedAt = now
		if err := updateFact(ctx, tx, existing); err != nil {
			return Fact{}, err
		}
		if err := tx.Commit(); err != nil {
			return Fact{}, fmt.Errorf("userstore: commit add: %w", err)
		}
		return existing, nil
	}

	live, err := countLive(ctx, tx)
	if err != nil {
		return Fact{}, err
	}
	if live >= MaxItems {
		return Fact{}, errdefs.Validationf(
			"memory: %d facts is the limit; remove one before adding another",
			MaxItems)
	}

	if err := insertFact(ctx, tx, normalized); err != nil {
		return Fact{}, err
	}
	if err := tx.Commit(); err != nil {
		return Fact{}, fmt.Errorf("userstore: commit add: %w", err)
	}
	return normalized, nil
}

// Replace rewrites one fact's text, keeping its kind, scope and
// provenance. The dedupe key moves with the text, so an edit that
// collides with another fact is rejected rather than silently merging.
func (s *Store) Replace(ctx context.Context, id, text string) (Fact, error) {
	if strings.TrimSpace(id) == "" {
		return Fact{}, errdefs.Validationf("memory: fact id is required")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return Fact{}, errdefs.Validationf("memory: fact text is required")
	}
	if len(text) > MaxTextBytes {
		return Fact{}, errdefs.Validationf(
			"memory: text is %d bytes, the limit is %d", len(text), MaxTextBytes)
	}
	now := s.now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Fact{}, fmt.Errorf("userstore: begin replace: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			telemetry.WarnErr(ctx, "userstore: rollback replace failed", err)
		}
	}()
	fact, found, err := findByID(ctx, tx, id)
	if err != nil {
		return Fact{}, err
	}
	if !found {
		return Fact{}, ErrNotFound
	}
	fact.Text = text
	// The identity follows the text: a row whose key still named the
	// old text would collide with the wrong fact on the next Add.
	fact.DedupeKey = dedupeKey(text)
	fact.UpdatedAt = now
	if err := updateFact(ctx, tx, fact); err != nil {
		if isUniqueViolation(err) {
			return Fact{}, errdefs.Conflictf(
				"memory: another fact already says that; remove one of them")
		}
		return Fact{}, err
	}
	if err := tx.Commit(); err != nil {
		return Fact{}, fmt.Errorf("userstore: commit replace: %w", err)
	}
	return fact, nil
}

// Remove deletes one fact outright.
func (s *Store) Remove(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return errdefs.Validationf("memory: fact id is required")
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM user_memory WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("userstore: remove %s: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("userstore: remove %s: %w", id, err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// SetStale retires or revives one fact. Retired facts stay readable
// (the page shows them behind a toggle) but are never injected.
func (s *Store) SetStale(ctx context.Context, id string, stale bool) (Fact, error) {
	if strings.TrimSpace(id) == "" {
		return Fact{}, errdefs.Validationf("memory: fact id is required")
	}
	flag := 0
	if stale {
		flag = 1
	}
	fact, err := s.Get(ctx, id)
	if err != nil {
		return Fact{}, err
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE user_memory SET stale = ?, updated_at = ? WHERE id = ?`,
		flag, s.now().Format(timeLayout), id); err != nil {
		return Fact{}, fmt.Errorf("userstore: set stale %s: %w", id, err)
	}
	fact.Stale = stale
	return fact, nil
}

// List returns the facts visible in one workspace: global rows plus
// the workspace's own, most recently updated first.
func (s *Store) List(ctx context.Context, query Query) ([]Fact, error) {
	var (
		conds []string
		args  []any
	)
	conds = append(conds, `(scope = ? OR (scope = ? AND workspace = ?))`)
	args = append(args, ScopeGlobal, ScopeWorkspace, query.Workspace)
	if !query.IncludeStale {
		conds = append(conds, "stale = 0")
	}
	sqlText := `SELECT id, kind, scope, workspace, text, source_conversation,
		       source_run, created_at, updated_at, stale
		FROM user_memory WHERE ` + strings.Join(conds, " AND ") +
		` ORDER BY updated_at DESC, rowid DESC`
	if query.Limit > 0 {
		sqlText += " LIMIT ?"
		args = append(args, query.Limit)
	}
	rows, err := s.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("userstore: list: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "userstore: close list rows failed", rows.Close())
	}()
	var out []Fact
	for rows.Next() {
		fact, err := scanFact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("userstore: list: %w", err)
	}
	return out, nil
}

// Get reads one fact by id.
func (s *Store) Get(ctx context.Context, id string) (Fact, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, kind, scope, workspace, text,
		       source_conversation, source_run, created_at, updated_at, stale
		FROM user_memory WHERE id = ?`, id)
	fact, err := scanFact(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Fact{}, ErrNotFound
	}
	if err != nil {
		return Fact{}, err
	}
	return fact, nil
}

// scanFact reads one row (or QueryRow result) into a Fact.
func scanFact(rows interface{ Scan(...any) error }) (Fact, error) {
	var (
		fact             Fact
		created, updated string
		stale            int
	)
	if err := rows.Scan(
		&fact.ID, &fact.Kind, &fact.Scope, &fact.Workspace, &fact.Text,
		&fact.SourceConversation, &fact.SourceRun,
		&created, &updated, &stale,
	); err != nil {
		return Fact{}, fmt.Errorf("userstore: scan fact: %w", err)
	}
	if at, err := time.Parse(timeReadLayout, created); err == nil {
		fact.CreatedAt = at
	}
	if at, err := time.Parse(timeReadLayout, updated); err == nil {
		fact.UpdatedAt = at
	}
	fact.Stale = stale != 0
	return fact, nil
}

func insertFact(ctx context.Context, tx *sql.Tx, fact Fact) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_memory(
			id, kind, scope, workspace, text, dedupe_key,
			source_conversation, source_run, created_at, updated_at, stale
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		fact.ID, fact.Kind, fact.Scope, fact.Workspace, fact.Text,
		dedupeKey(fact.Text),
		fact.SourceConversation, fact.SourceRun,
		fact.CreatedAt.UTC().Format(timeLayout),
		fact.UpdatedAt.UTC().Format(timeLayout),
		boolInt(fact.Stale),
	); err != nil {
		if isUniqueViolation(err) {
			return errdefs.Conflictf(
				"memory: another fact already says that; remove one of them")
		}
		return fmt.Errorf("userstore: insert fact: %w", err)
	}
	return nil
}

func updateFact(ctx context.Context, tx *sql.Tx, fact Fact) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE user_memory
		SET kind = ?, text = ?, dedupe_key = ?, source_conversation = ?,
		    source_run = ?, updated_at = ?, stale = ?
		WHERE id = ?`,
		fact.Kind, fact.Text, dedupeKey(fact.Text),
		fact.SourceConversation, fact.SourceRun,
		fact.UpdatedAt.UTC().Format(timeLayout),
		boolInt(fact.Stale), fact.ID,
	); err != nil {
		return fmt.Errorf("userstore: update fact: %w", err)
	}
	return nil
}

func findByID(ctx context.Context, tx *sql.Tx, id string) (Fact, bool, error) {
	row := tx.QueryRowContext(ctx, `SELECT id, kind, scope, workspace, text,
		       source_conversation, source_run, created_at, updated_at, stale
		FROM user_memory WHERE id = ?`, id)
	fact, err := scanFact(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Fact{}, false, nil
	}
	if err != nil {
		return Fact{}, false, err
	}
	return fact, true, nil
}

func findByDedupe(
	ctx context.Context, tx *sql.Tx, scope, workspace, key string,
) (Fact, bool, error) {
	row := tx.QueryRowContext(ctx, `SELECT id, kind, scope, workspace, text,
		       source_conversation, source_run, created_at, updated_at, stale
		FROM user_memory WHERE scope = ? AND workspace = ? AND dedupe_key = ?`,
		scope, workspace, key)
	fact, err := scanFact(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Fact{}, false, nil
	}
	if err != nil {
		return Fact{}, false, err
	}
	return fact, true, nil
}

func countLive(ctx context.Context, tx *sql.Tx) (int, error) {
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_memory WHERE stale = 0`).Scan(&n); err != nil {
		return 0, fmt.Errorf("userstore: count facts: %w", err)
	}
	return n, nil
}

// normalizeFact validates one incoming fact and fills the derived
// fields (scope, kind, dedupe key).
// Validate normalizes one incoming fact under exactly the rules Add
// applies. Callers that must not write (the review queue accepting a
// suggestion, a dry run in the settings page) use it so a candidate is
// rejected identically whether it is written now or later.
func Validate(fact Fact) (Fact, error) { return normalizeFact(fact) }

// DedupeKeyOf exposes the identity a fact's text normalizes to, so a
// caller can tell whether a candidate is already stored without
// writing it first.
func DedupeKeyOf(text string) string { return dedupeKey(text) }

func normalizeFact(fact Fact) (Fact, error) {
	fact.Text = strings.TrimSpace(fact.Text)
	if fact.Text == "" {
		return Fact{}, errdefs.Validationf("memory: fact text is required")
	}
	if len(fact.Text) > MaxTextBytes {
		return Fact{}, errdefs.Validationf(
			"memory: text is %d bytes, the limit is %d",
			len(fact.Text), MaxTextBytes)
	}
	fact.Scope = strings.TrimSpace(fact.Scope)
	switch fact.Scope {
	case ScopeGlobal:
		fact.Workspace = ""
	case ScopeWorkspace:
		if strings.TrimSpace(fact.Workspace) == "" {
			return Fact{}, errdefs.Validationf(
				"memory: a workspace fact needs the workspace path")
		}
	default:
		return Fact{}, errdefs.Validationf(
			"memory: unknown scope %q (want global or workspace)", fact.Scope)
	}
	fact.Kind = strings.TrimSpace(fact.Kind)
	if fact.Kind == "" {
		fact.Kind = "fact"
	}
	fact.DedupeKey = dedupeKey(fact.Text)
	return fact, nil
}

// dedupeKey normalizes a fact's text into its identity: lowercased,
// whitespace collapsed, surrounding punctuation dropped. Restating the
// same fact with different spacing or a trailing period updates the
// row instead of adding a twin.
func dedupeKey(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	space := false
	for _, r := range text {
		if unicode.IsSpace(r) {
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return strings.Trim(b.String(), ".,;:!?。，；：！？")
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// newID mints one fact id. Ids are opaque; identity lives in the
// dedupe key.
func newID() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("userstore: generate id: %w", err)
	}
	return "um-" + hex.EncodeToString(raw[:]), nil
}
