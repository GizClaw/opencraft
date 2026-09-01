// Package usage owns the user-level token usage tables in
// ~/.opencraft/user.db: per-model usage across every workspace and
// session, aggregated on demand. The desktop shell shares one
// userdb.DB between usage and automations; New attaches to that
// handle, while Open remains a convenience for standalone use/tests.
package usage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/GizClaw/opencraft/internal/userdb"
)

// Store is the user-level usage database.
type Store struct {
	db      *sql.DB
	closeFn func() error
}

// New attaches the usage store to an existing user database handle
// and ensures the usage schema exists.
func New(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("usage: nil database")
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS model_usage (
		workspace_id TEXT NOT NULL,
		session_id   TEXT NOT NULL,
		model        TEXT NOT NULL,
		input_tokens INTEGER NOT NULL DEFAULT 0,
		output_tokens INTEGER NOT NULL DEFAULT 0,
		cache_read_tokens INTEGER NOT NULL DEFAULT 0,
		reasoning_tokens INTEGER NOT NULL DEFAULT 0,
		latency_ms INTEGER NOT NULL DEFAULT 0,
		updated_at TEXT NOT NULL,
		PRIMARY KEY (workspace_id, session_id, model)
	)`); err != nil {
		return nil, fmt.Errorf("usage: create schema: %w", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS model_usage_hourly (
		model        TEXT NOT NULL,
		hour         TEXT NOT NULL,
		input_tokens INTEGER NOT NULL DEFAULT 0,
		output_tokens INTEGER NOT NULL DEFAULT 0,
		cache_read_tokens INTEGER NOT NULL DEFAULT 0,
		reasoning_tokens INTEGER NOT NULL DEFAULT 0,
		latency_ms INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (model, hour)
	)`); err != nil {
		return nil, fmt.Errorf("usage: create hourly schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Open opens (creating if necessary) the usage database at path. It
// owns the connection and Close closes it. Desktop callers should
// prefer New over a shared userdb.DB.
func Open(path string) (*Store, error) {
	udb, err := userdb.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := New(udb.SQLDB())
	if err != nil {
		_ = udb.Close()
		return nil, err
	}
	st.closeFn = udb.Close
	return st, nil
}

// Close closes the database handle.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	if s.closeFn != nil {
		return s.closeFn()
	}
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// Usage is one recorded usage delta for a model in a session.
type Usage struct {
	InputTokens     int64
	OutputTokens    int64
	CacheReadTokens int64
	ReasoningTokens int64
	LatencyMs       int64
}

// Record accumulates one usage delta for (workspace, session, model).
func (s *Store) Record(
	ctx context.Context,
	workspaceID, sessionID, model string,
	u Usage,
) error {
	if model == "" || sessionID == "" {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("usage: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO model_usage (
			workspace_id, session_id, model,
			input_tokens, output_tokens, cache_read_tokens,
			reasoning_tokens, latency_ms, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace_id, session_id, model) DO UPDATE SET
			input_tokens = input_tokens + excluded.input_tokens,
			output_tokens = output_tokens + excluded.output_tokens,
			cache_read_tokens = cache_read_tokens + excluded.cache_read_tokens,
			reasoning_tokens = reasoning_tokens + excluded.reasoning_tokens,
			latency_ms = latency_ms + excluded.latency_ms,
			updated_at = excluded.updated_at
	`,
		workspaceID, sessionID, model,
		u.InputTokens, u.OutputTokens, u.CacheReadTokens,
		u.ReasoningTokens, u.LatencyMs,
		time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("usage: record %s: %w", model, err)
	}
	hour := time.Now().UTC().Truncate(time.Hour).Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO model_usage_hourly (
			model, hour,
			input_tokens, output_tokens, cache_read_tokens,
			reasoning_tokens, latency_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(model, hour) DO UPDATE SET
			input_tokens = input_tokens + excluded.input_tokens,
			output_tokens = output_tokens + excluded.output_tokens,
			cache_read_tokens = cache_read_tokens + excluded.cache_read_tokens,
			reasoning_tokens = reasoning_tokens + excluded.reasoning_tokens,
			latency_ms = latency_ms + excluded.latency_ms
	`,
		model, hour,
		u.InputTokens, u.OutputTokens, u.CacheReadTokens,
		u.ReasoningTokens, u.LatencyMs,
	); err != nil {
		return fmt.Errorf("usage: record hourly %s: %w", model, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("usage: commit: %w", err)
	}
	return nil
}

// SummaryRow aggregates one model's usage across all workspaces and
// sessions.
type SummaryRow struct {
	Model           string
	InputTokens     int64
	OutputTokens    int64
	CacheReadTokens int64
	ReasoningTokens int64
	LatencyMs       int64
	Workspaces      int
	Sessions        int
	UpdatedAt       string
}

// Summary returns per-model usage, most used first.
func (s *Store) Summary(ctx context.Context) ([]SummaryRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			model,
			SUM(input_tokens), SUM(output_tokens), SUM(cache_read_tokens),
			SUM(reasoning_tokens), SUM(latency_ms),
			COUNT(DISTINCT workspace_id), COUNT(DISTINCT session_id),
			MAX(updated_at)
		FROM model_usage
		GROUP BY model
		ORDER BY (SUM(input_tokens) + SUM(output_tokens)) DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("usage: summary: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []SummaryRow
	for rows.Next() {
		var r SummaryRow
		if err := rows.Scan(
			&r.Model,
			&r.InputTokens, &r.OutputTokens, &r.CacheReadTokens,
			&r.ReasoningTokens, &r.LatencyMs,
			&r.Workspaces, &r.Sessions,
			&r.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("usage: scan summary: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Granularity selects the time bucket for a usage series.
type Granularity string

const (
	GranularityHour Granularity = "hour"
	GranularityDay  Granularity = "day"
)

// Point is one time-bucketed usage sample for a model.
type Point struct {
	Time            string
	InputTokens     int64
	OutputTokens    int64
	CacheReadTokens int64
	ReasoningTokens int64
}

// Series returns one model's usage bucketed by hour or day, oldest
// first. Hour buckets keep the stored UTC hour string; day buckets are
// local calendar days computed with utcOffsetMinutes, so boundaries
// match the viewer's timezone. start and end bound the recorded UTC
// hours ([start, end)); empty strings leave that side unbounded.
func (s *Store) Series(
	ctx context.Context,
	model string,
	granularity Granularity,
	utcOffsetMinutes int,
	start, end string,
) ([]Point, error) {
	bucket := "hour"
	if granularity == GranularityDay {
		if utcOffsetMinutes != 0 {
			bucket = fmt.Sprintf(
				"substr(datetime(hour, '%+d minutes'), 1, 10)",
				utcOffsetMinutes,
			)
		} else {
			bucket = "substr(hour, 1, 10)"
		}
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT
			%s,
			SUM(input_tokens), SUM(output_tokens), SUM(cache_read_tokens),
			SUM(reasoning_tokens)
		FROM model_usage_hourly
		WHERE model = ?
			AND (? = '' OR hour >= ?)
			AND (? = '' OR hour < ?)
		GROUP BY %s
		ORDER BY %s
	`, bucket, bucket, bucket), model, start, start, end, end)
	if err != nil {
		return nil, fmt.Errorf("usage: series: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Point
	for rows.Next() {
		var p Point
		if err := rows.Scan(
			&p.Time,
			&p.InputTokens, &p.OutputTokens, &p.CacheReadTokens,
			&p.ReasoningTokens,
		); err != nil {
			return nil, fmt.Errorf("usage: scan series: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
