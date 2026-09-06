// Package usage owns the user-level token usage tables in
// ~/.opencraft/user.db: per-model usage across every workspace and
// session, aggregated on demand. Model rows are keyed by model name
// only (not provider/model): statistics intentionally bucket by name
// across providers. The desktop shell migrates user.db through
// orchestration/migrations and then Attach binds this store to the
// shared handle.
package usage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// Store is the user-level usage database.
type Store struct {
	db *sql.DB
}

// Attach binds the usage store to an existing foundation/db handle
// that orchestration/migrations.User already migrated.
func Attach(handle *db.DB) (*Store, error) {
	if handle == nil {
		return nil, fmt.Errorf("usage: nil database")
	}
	return &Store{db: handle.SQLDB()}, nil
}

// Usage is one recorded usage delta for a model in a session.
type Usage struct {
	TotalTokens      int64
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	ReasoningTokens  int64
	LatencyMs        int64
	Calls            int64
}

// Record accumulates one usage delta for (workspace, session, model).
// model is normalized to its name-only form, so a legacy
// "provider/name" key from an imported bundle cannot be written back.
// TotalTokens is the provider-reported billed total; callers that only
// record a breakdown fall back to input + output, which matches the
// legacy rows rebuilt by migration 004.
// at is the moment the engine reported the usage; its UTC hour feeds
// the model_usage_hourly bucket so long turns are attributed to the
// report-arrival time instead of the turn-finish write time.
func (s *Store) Record(
	ctx context.Context,
	workspaceID, sessionID, model string,
	at time.Time,
	u Usage,
) error {
	if model == "" || sessionID == "" {
		return nil
	}
	model = ocsessions.NormalizeModelName(model)
	if model == "" {
		return nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	hour := at.UTC().Truncate(time.Hour).Format(time.RFC3339)
	total := u.TotalTokens
	if total <= 0 {
		total = u.InputTokens + u.OutputTokens
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("usage: begin tx: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			telemetry.WarnErr(ctx, "usage: rollback record failed", err)
		}
	}()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO model_usage (
			workspace_id, session_id, model, total_tokens,
			input_tokens, output_tokens, cache_read_tokens,
			cache_write_tokens, reasoning_tokens, latency_ms,
			calls, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace_id, session_id, model) DO UPDATE SET
			total_tokens = total_tokens + excluded.total_tokens,
			input_tokens = input_tokens + excluded.input_tokens,
			output_tokens = output_tokens + excluded.output_tokens,
			cache_read_tokens = cache_read_tokens + excluded.cache_read_tokens,
			cache_write_tokens = cache_write_tokens + excluded.cache_write_tokens,
			reasoning_tokens = reasoning_tokens + excluded.reasoning_tokens,
			latency_ms = latency_ms + excluded.latency_ms,
			calls = calls + excluded.calls,
			updated_at = excluded.updated_at
	`,
		workspaceID, sessionID, model, total,
		u.InputTokens, u.OutputTokens, u.CacheReadTokens,
		u.CacheWriteTokens, u.ReasoningTokens, u.LatencyMs,
		u.Calls,
		time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("usage: record %s: %w", model, err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO model_usage_hourly (
			model, hour,
			input_tokens, output_tokens, cache_read_tokens,
			cache_write_tokens, reasoning_tokens, latency_ms, calls
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(model, hour) DO UPDATE SET
			input_tokens = input_tokens + excluded.input_tokens,
			output_tokens = output_tokens + excluded.output_tokens,
			cache_read_tokens = cache_read_tokens + excluded.cache_read_tokens,
			cache_write_tokens = cache_write_tokens + excluded.cache_write_tokens,
			reasoning_tokens = reasoning_tokens + excluded.reasoning_tokens,
			latency_ms = latency_ms + excluded.latency_ms,
			calls = calls + excluded.calls
	`,
		model, hour,
		u.InputTokens, u.OutputTokens, u.CacheReadTokens,
		u.CacheWriteTokens, u.ReasoningTokens, u.LatencyMs,
		u.Calls,
	); err != nil {
		return fmt.Errorf("usage: record hourly %s: %w", model, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("usage: commit: %w", err)
	}
	return nil
}

// RecordSessionUsage accumulates one session usage delta into the
// user-level per-model tables. It is the adapter shared by the desktop
// and headless Hosts, so turn usage and auto-title usage land in the
// same model_usage rows regardless of which entry point ran the turn.
// at follows the UsageRecorder contract: the engine report-arrival
// moment that drives the hourly bucket.
func (s *Store) RecordSessionUsage(
	ctx context.Context,
	workspaceID, sessionID string,
	u ocsessions.Usage,
	at time.Time,
) error {
	if u.Model == "" || u.TotalTokens <= 0 {
		return nil
	}
	calls := u.Calls
	if calls <= 0 {
		calls = 1
	}
	return s.Record(ctx, workspaceID, sessionID, u.Model, at, Usage{
		TotalTokens:      u.TotalTokens,
		InputTokens:      u.InputTokens,
		OutputTokens:     u.OutputTokens,
		CacheReadTokens:  u.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens,
		ReasoningTokens:  u.ReasoningTokens,
		LatencyMs:        u.LatencyMs,
		Calls:            calls,
	})
}

// SummaryRow aggregates one model's usage across all workspaces and
// sessions.
type SummaryRow struct {
	Model            string
	TotalTokens      int64
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	ReasoningTokens  int64
	LatencyMs        int64
	Calls            int64
	Workspaces       int
	Sessions         int
	UpdatedAt        string
}

// Summary returns per-model usage, most used first.
func (s *Store) Summary(ctx context.Context) ([]SummaryRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			model,
			SUM(total_tokens),
			SUM(input_tokens), SUM(output_tokens), SUM(cache_read_tokens),
			SUM(cache_write_tokens), SUM(reasoning_tokens), SUM(latency_ms),
			SUM(calls),
			COUNT(DISTINCT workspace_id), COUNT(DISTINCT session_id),
			MAX(updated_at)
		FROM model_usage
		GROUP BY model
		ORDER BY (SUM(input_tokens) + SUM(output_tokens)) DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("usage: summary: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "usage: close summary rows failed", rows.Close())
	}()
	var out []SummaryRow
	for rows.Next() {
		var r SummaryRow
		if err := rows.Scan(
			&r.Model,
			&r.TotalTokens,
			&r.InputTokens, &r.OutputTokens, &r.CacheReadTokens,
			&r.CacheWriteTokens, &r.ReasoningTokens, &r.LatencyMs,
			&r.Calls,
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

// SessionCount returns the number of distinct (workspace, session)
// pairs that have any recorded user-level usage. Unlike summing the
// per-model Sessions fields of Summary, one session that used several
// models is counted exactly once.
func (s *Store) SessionCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM (SELECT 1 FROM model_usage
		      GROUP BY workspace_id, session_id)
	`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("usage: session count: %w", err)
	}
	return n, nil
}

// Granularity selects the time bucket for a usage series.
type Granularity string

const (
	GranularityHour Granularity = "hour"
	GranularityDay  Granularity = "day"
)

// Point is one time-bucketed usage sample for a model.
type Point struct {
	Time             string
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	ReasoningTokens  int64
}

// Series returns one model's usage bucketed by hour or day, oldest
// first. Hour buckets keep the stored UTC hour string; day buckets are
// local calendar days computed with utcOffsetMinutes, so boundaries
// match the viewer's timezone. start and end bound the recorded UTC
// hours ([start, end)); rows are whole UTC hours, and a row is included
// when its hour starts inside the window. Empty strings leave that
// side unbounded.
func (s *Store) Series(
	ctx context.Context,
	model string,
	granularity Granularity,
	utcOffsetMinutes int,
	start, end string,
) ([]Point, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			hour,
			SUM(input_tokens), SUM(output_tokens), SUM(cache_read_tokens),
			SUM(cache_write_tokens), SUM(reasoning_tokens)
		FROM model_usage_hourly
		WHERE model = ?
			AND (? = '' OR hour >= ?)
			AND (? = '' OR hour < ?)
		GROUP BY hour
		ORDER BY hour
	`, model, start, start, end, end)
	if err != nil {
		return nil, fmt.Errorf("usage: series: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "usage: close series rows failed", rows.Close())
	}()
	type raw struct {
		hour string
		p    Point
	}
	var rawRows []raw
	for rows.Next() {
		var h string
		var p Point
		if err := rows.Scan(
			&h,
			&p.InputTokens, &p.OutputTokens, &p.CacheReadTokens,
			&p.CacheWriteTokens, &p.ReasoningTokens,
		); err != nil {
			return nil, fmt.Errorf("usage: scan series: %w", err)
		}
		rawRows = append(rawRows, raw{hour: h, p: p})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if granularity != GranularityDay {
		out := make([]Point, 0, len(rawRows))
		for _, r := range rawRows {
			r.p.Time = r.hour
			out = append(out, r.p)
		}
		return out, nil
	}
	// Day buckets are computed in Go from the raw UTC hours so every
	// row is labelled with the same local date (hour + offset) that the
	// viewer sees, independent of the SQL hour-string comparison.
	zone := time.FixedZone("", utcOffsetMinutes*60)
	byDay := make(map[string]*Point, len(rawRows))
	var days []string
	for _, r := range rawRows {
		t, err := time.Parse(time.RFC3339, r.hour)
		if err != nil {
			return nil, fmt.Errorf("usage: series: parse hour %q: %w", r.hour, err)
		}
		day := t.In(zone).Format("2006-01-02")
		p := byDay[day]
		if p == nil {
			p = &Point{Time: day}
			byDay[day] = p
			days = append(days, day)
		}
		p.InputTokens += r.p.InputTokens
		p.OutputTokens += r.p.OutputTokens
		p.CacheReadTokens += r.p.CacheReadTokens
		p.CacheWriteTokens += r.p.CacheWriteTokens
		p.ReasoningTokens += r.p.ReasoningTokens
	}
	out := make([]Point, 0, len(days))
	for _, day := range days {
		out = append(out, *byDay[day])
	}
	return out, nil
}
