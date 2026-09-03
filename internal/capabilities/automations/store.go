package automations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

const (
	perTaskRunLimit = 100
	globalRunLimit  = 1000
)

// ErrNotFound is returned when a task id has no row.
var ErrNotFound = errors.New("automations: task not found")

// timeLayout is the RFC3339 storage format for all timestamps.
const timeLayout = time.RFC3339

// Store persists automation tasks and runs in the shared user
// database (~/.opencraft/user.db).
type Store struct {
	db      *sql.DB
	closeFn func() error
}

// New attaches the automation store to an existing foundation/db
// handle and registers the automation migrations.
func New(handle *db.DB) (*Store, error) {
	if handle == nil {
		return nil, fmt.Errorf("automations: nil database")
	}
	if err := handle.Migrate(context.Background(), Migrations()); err != nil {
		return nil, fmt.Errorf("automations: migrate schema: %w", err)
	}
	return Attach(handle)
}

// Attach binds the automation store to an existing foundation/db
// handle without migrating. Callers that already ran the centralized
// user migrations should use Attach; New remains for standalone use.
func Attach(handle *db.DB) (*Store, error) {
	if handle == nil {
		return nil, fmt.Errorf("automations: nil database")
	}
	if err := ensureAutomationsColumn(handle.SQLDB(), "notify", "TEXT NOT NULL DEFAULT 'always'"); err != nil {
		return nil, fmt.Errorf("automations: migrate schema: %w", err)
	}
	if err := ensureAutomationsColumn(handle.SQLDB(), "conversation_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return nil, fmt.Errorf("automations: migrate schema: %w", err)
	}
	return &Store{db: handle.SQLDB()}, nil
}

// ensureAutomationsColumn adds a column to an existing automations
// table when it predates the current schema (CREATE TABLE IF NOT
// EXISTS leaves old tables untouched).
func ensureAutomationsColumn(db *sql.DB, column, decl string) error {
	rows, err := db.Query(`PRAGMA table_info(automations)`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
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
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(
		`ALTER TABLE automations ADD COLUMN ` + column + ` ` + decl)
	return err
}

// Open opens (creating if necessary) the automation store at the user
// database path. It owns the connection; desktop callers should
// prefer New over a shared db.DB.
func Open(path string) (*Store, error) {
	udb, err := db.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := New(udb)
	if err != nil {
		_ = udb.Close()
		return nil, err
	}
	st.closeFn = udb.Close
	return st, nil
}

// Close closes the connection when this store owns it (Open).
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	if s.closeFn != nil {
		return s.closeFn()
	}
	return nil
}

// ListTasks returns every task, ordered by name.
func (s *Store) ListTasks(ctx context.Context) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, prompt, schedule, workspace, mode, model, think,
		       conversation_id, notify,
		       enabled, created_at, updated_at, last_run_at, last_status,
		       next_run_at
		FROM automations ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("automations: list tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("automations: list tasks: %w", err)
	}
	return out, nil
}

// GetTask returns one task by id.
func (s *Store) GetTask(ctx context.Context, id string) (Task, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, prompt, schedule, workspace, mode, model, think,
		       conversation_id, notify,
		       enabled, created_at, updated_at, last_run_at, last_status,
		       next_run_at
		FROM automations WHERE id = ?`, id)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, fmt.Errorf("automations: get task: %w", err)
	}
	return t, nil
}

// SaveTask inserts or updates one task. New tasks get an id, fresh
// timestamps, and a future nextRunAt; updates keep the schedule's
// next trigger unless it is zero or already past.
func (s *Store) SaveTask(ctx context.Context, task Task) (Task, error) {
	task.Name = strings.TrimSpace(task.Name)
	task.Prompt = strings.TrimSpace(task.Prompt)
	task.Workspace = strings.TrimSpace(task.Workspace)
	if task.Mode == "" {
		task.Mode = ModeWorkspace
	}
	if task.Notify == "" {
		task.Notify = NotifyAlways
	}
	if err := task.Validate(); err != nil {
		return Task{}, err
	}
	now := time.Now()
	// Updates that omit the weekly phase anchor keep the stored one
	// instead of re-anchoring the phase.
	if task.ID != "" {
		if existing, err := s.GetTask(ctx, task.ID); err == nil &&
			task.Schedule.Origin == "" {
			task.Schedule.Origin = existing.Schedule.Origin
		}
	}
	task.Schedule.ensureOrigin(now)
	if task.ID == "" {
		task.ID = NewID("t-")
		task.CreatedAt = now
	} else if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	task.UpdatedAt = now
	if task.NextRunAt.IsZero() || !task.NextRunAt.After(now) {
		next, err := task.Schedule.Next(now)
		if err != nil {
			return Task{}, fmt.Errorf("automations: next run: %w", err)
		}
		task.NextRunAt = next
	}
	scheduleJSON, err := json.Marshal(task.Schedule)
	if err != nil {
		return Task{}, fmt.Errorf("automations: encode schedule: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO automations (
			id, name, prompt, schedule, workspace, mode, model, think,
			conversation_id, notify, enabled, created_at, updated_at,
			last_run_at, last_status,
			next_run_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			prompt = excluded.prompt,
			schedule = excluded.schedule,
			workspace = excluded.workspace,
			mode = excluded.mode,
			model = excluded.model,
			think = excluded.think,
			conversation_id = excluded.conversation_id,
			notify = excluded.notify,
			enabled = excluded.enabled,
			updated_at = excluded.updated_at,
			next_run_at = excluded.next_run_at
	`,
		task.ID, task.Name, task.Prompt, string(scheduleJSON),
		task.Workspace, task.Mode, task.Model, task.Think,
		task.ConversationID, task.Notify,
		boolInt(task.Enabled), fmtTime(task.CreatedAt), fmtTime(task.UpdatedAt),
		fmtTime(task.LastRunAt), task.LastStatus, fmtTime(task.NextRunAt),
	)
	if err != nil {
		return Task{}, fmt.Errorf("automations: save task: %w", err)
	}
	return task, nil
}

// DeleteTask removes one task and its run history in one transaction.
func (s *Store) DeleteTask(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("automations: begin delete: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM automation_runs WHERE task_id = ?`, id); err != nil {
		return fmt.Errorf("automations: delete runs: %w", err)
	}
	res, err := tx.ExecContext(ctx,
		`DELETE FROM automations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("automations: delete task: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("automations: commit delete: %w", err)
	}
	return nil
}

// ListRuns returns one task's run history, newest first.
func (s *Store) ListRuns(ctx context.Context, taskID string) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, at, status, error, conversation_id, run_id,
		       duration_ms, summary
		FROM automation_runs WHERE task_id = ? ORDER BY at DESC, id DESC`,
		taskID)
	if err != nil {
		return nil, fmt.Errorf("automations: list runs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Run
	for rows.Next() {
		var r Run
		var at string
		if err := rows.Scan(
			&r.ID, &r.TaskID, &at, &r.Status, &r.Error,
			&r.ConversationID, &r.RunID, &r.DurationMs, &r.Summary,
		); err != nil {
			return nil, fmt.Errorf("automations: scan run: %w", err)
		}
		r.At = parseTime(at)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("automations: list runs: %w", err)
	}
	return out, nil
}

// AppendRun inserts one run record and prunes history to the per-task
// and global caps.
func (s *Store) AppendRun(ctx context.Context, run Run) (Run, error) {
	if run.ID == "" {
		run.ID = NewID("run_")
	}
	if run.At.IsZero() {
		run.At = time.Now()
	}
	if run.Status == "" {
		run.Status = RunRunning
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, fmt.Errorf("automations: begin run: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO automation_runs (
			id, task_id, at, status, error, conversation_id, run_id,
			duration_ms, summary
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.TaskID, fmtTime(run.At), string(run.Status),
		run.Error, run.ConversationID, run.RunID, run.DurationMs,
		run.Summary,
	); err != nil {
		return Run{}, fmt.Errorf("automations: insert run: %w", err)
	}
	if err := pruneRuns(ctx, tx, run.TaskID); err != nil {
		return Run{}, err
	}
	if err := tx.Commit(); err != nil {
		return Run{}, fmt.Errorf("automations: commit run: %w", err)
	}
	return run, nil
}

// UpdateRun writes the terminal state of one run.
func (s *Store) UpdateRun(ctx context.Context, run Run) error {
	if _, err := s.db.ExecContext(ctx, `
		UPDATE automation_runs SET
			status = ?, error = ?, conversation_id = ?, run_id = ?,
			duration_ms = ?, summary = ?
		WHERE id = ?`,
		string(run.Status), run.Error, run.ConversationID, run.RunID,
		run.DurationMs, run.Summary, run.ID,
	); err != nil {
		return fmt.Errorf("automations: update run: %w", err)
	}
	return nil
}

// SetTaskLast records the task-level last run summary.
func (s *Store) SetTaskLast(
	ctx context.Context, taskID string, at time.Time, status string,
) error {
	if _, err := s.db.ExecContext(ctx, `
		UPDATE automations SET last_run_at = ?, last_status = ?
		WHERE id = ?`,
		fmtTime(at), status, taskID,
	); err != nil {
		return fmt.Errorf("automations: set task last: %w", err)
	}
	return nil
}

// AdvanceNextRun persists the next trigger point (already computed
// from a now-anchored base, so a missed window never wedges the
// schedule).
func (s *Store) AdvanceNextRun(
	ctx context.Context, taskID string, next time.Time,
) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE automations SET next_run_at = ? WHERE id = ?`,
		fmtTime(next), taskID,
	); err != nil {
		return fmt.Errorf("automations: advance next run: %w", err)
	}
	return nil
}

// Reconcile marks runs left in "running" (app exit/crash) as failed
// and mirrors that onto the task-level last status. It returns the
// number of runs corrected.
func (s *Store) Reconcile(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE automation_runs SET status = 'failed', error = '应用重启中断'
		WHERE status = 'running'`)
	if err != nil {
		return 0, fmt.Errorf("automations: reconcile runs: %w", err)
	}
	n, _ := res.RowsAffected()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE automations SET last_status = 'failed'
		WHERE last_status = 'running'`); err != nil {
		return 0, fmt.Errorf("automations: reconcile tasks: %w", err)
	}
	return n, nil
}

func pruneRuns(ctx context.Context, tx *sql.Tx, taskID string) error {
	for _, q := range []string{
		`DELETE FROM automation_runs WHERE id IN (
			SELECT id FROM automation_runs WHERE task_id = ?
			ORDER BY at DESC, id DESC LIMIT -1 OFFSET ?)`,
		`DELETE FROM automation_runs WHERE id IN (
			SELECT id FROM automation_runs
			ORDER BY at DESC, id DESC LIMIT -1 OFFSET ?)`,
	} {
		var (
			args []any
			err  error
		)
		if strings.Contains(q, "task_id = ?") {
			args = []any{taskID, perTaskRunLimit}
		} else {
			args = []any{globalRunLimit}
		}
		if _, err = tx.ExecContext(ctx, q, args...); err != nil {
			return fmt.Errorf("automations: prune runs: %w", err)
		}
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTask(row rowScanner) (Task, error) {
	var (
		t            Task
		scheduleJSON string
		createdAt    string
		updatedAt    string
		lastRunAt    string
		nextRunAt    string
		enabled      int
	)
	if err := row.Scan(
		&t.ID, &t.Name, &t.Prompt, &scheduleJSON, &t.Workspace,
		&t.Mode, &t.Model, &t.Think, &t.ConversationID, &t.Notify, &enabled,
		&createdAt, &updatedAt, &lastRunAt, &t.LastStatus, &nextRunAt,
	); err != nil {
		return Task{}, err
	}
	if err := json.Unmarshal([]byte(scheduleJSON), &t.Schedule); err != nil {
		return Task{}, fmt.Errorf("automations: decode schedule: %w", err)
	}
	t.Enabled = enabled != 0
	t.CreatedAt = parseTime(createdAt)
	t.UpdatedAt = parseTime(updatedAt)
	t.LastRunAt = parseTime(lastRunAt)
	t.NextRunAt = parseTime(nextRunAt)
	return t, nil
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(timeLayout)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
