// Package usage owns the user-level token usage database
// (~/.opencraft/user.db): per-model usage across every workspace and
// session, aggregated on demand.
package usage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver.
)

// Store is the user-level usage database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if necessary) the usage database at path.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("usage: create directory: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("usage: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("usage: enable WAL: %w", err)
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
		_ = db.Close()
		return nil, fmt.Errorf("usage: create schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the database handle.
func (s *Store) Close() error { return s.db.Close() }

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
	if _, err := s.db.ExecContext(ctx, `
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
	defer rows.Close()
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
