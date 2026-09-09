// Package metrics owns the user-level local metric time series shown in the
// desktop Diagnostics panel (~/.opencraft/user.db). It complements the OTel
// pipelines: the same samples that go to logs/OTLP metrics are also recorded
// here so the app can render its own charts without an external collector.
// The table is created by orchestration/migrations (user migration 005);
// Attach binds this store to the shared migrated handle.
package metric

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// retention is how long samples survive before RecordBatch prunes them.
const retention = 30 * 24 * time.Hour

// Sample is one local metric observation.
type Sample struct {
	Name  string
	Ts    int64 // unix milliseconds, UTC
	Value float64
	Attrs map[string]string
}

// Store is the user-level metric database.
type Store struct {
	db *sql.DB
}

// Attach binds the metric store to an existing foundation/db handle that
// orchestration/migrations.User already migrated.
func Attach(handle *db.DB) (*Store, error) {
	if handle == nil {
		return nil, fmt.Errorf("metrics: nil database")
	}
	return &Store{db: handle.SQLDB()}, nil
}

// Record writes one sample.
func (s *Store) Record(
	ctx context.Context,
	name string,
	value float64,
	attrs map[string]string,
) error {
	return s.RecordBatch(ctx, []Sample{{
		Name:  name,
		Ts:    time.Now().UTC().UnixMilli(),
		Value: value,
		Attrs: attrs,
	}})
}

// RecordBatch writes samples in one transaction and prunes rows older than
// the retention window. Failures are returned so the caller can log them;
// persistence is best-effort by convention and must never fail a turn.
func (s *Store) RecordBatch(ctx context.Context, samples []Sample) error {
	if len(samples) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("metrics: begin tx: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil &&
			!errors.Is(err, sql.ErrTxDone) {
			telemetry.WarnErr(ctx, "metrics: rollback record failed", err)
		}
	}()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO metric_samples (name, ts, value, attrs)
		VALUES (?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("metrics: prepare insert: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "metrics: close insert stmt failed", stmt.Close())
	}()
	for _, sample := range samples {
		attrsJSON, err := json.Marshal(sample.Attrs)
		if err != nil {
			return fmt.Errorf("metrics: marshal attrs: %w", err)
		}
		if sample.Ts == 0 {
			sample.Ts = time.Now().UTC().UnixMilli()
		}
		if _, err := stmt.ExecContext(ctx,
			sample.Name, sample.Ts, sample.Value, string(attrsJSON)); err != nil {
			return fmt.Errorf("metrics: insert %q: %w", sample.Name, err)
		}
	}

	cutoff := time.Now().Add(-retention).UTC().UnixMilli()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM metric_samples WHERE ts < ?`, cutoff); err != nil {
		return fmt.Errorf("metrics: prune: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("metrics: commit: %w", err)
	}
	return nil
}

// Range returns samples for one metric in [from, to] (unix ms, both
// inclusive; to == 0 means no upper bound), oldest first, capped at limit.
func (s *Store) Range(
	ctx context.Context,
	name string,
	from, to int64,
	limit int,
) ([]Sample, error) {
	if limit <= 0 || limit > 5000 {
		limit = 5000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT ts, value, attrs FROM metric_samples
		WHERE name = ? AND ts >= ?
		  AND (? = 0 OR ts <= ?)
		ORDER BY ts ASC
		LIMIT ?`, name, from, to, to, limit)
	if err != nil {
		return nil, fmt.Errorf("metrics: range %q: %w", name, err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "metrics: close range rows failed", rows.Close())
	}()
	var out []Sample
	for rows.Next() {
		var (
			ts        int64
			value     float64
			attrsText string
		)
		if err := rows.Scan(&ts, &value, &attrsText); err != nil {
			return nil, fmt.Errorf("metrics: scan range: %w", err)
		}
		var attrs map[string]string
		if attrsText != "" && attrsText != "{}" {
			if err := json.Unmarshal([]byte(attrsText), &attrs); err != nil {
				telemetry.WarnErr(ctx,
					"metrics: skip sample with invalid attrs", err)
				continue
			}
		}
		out = append(out, Sample{
			Name: name, Ts: ts, Value: value, Attrs: attrs,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("metrics: range rows: %w", err)
	}
	return out, nil
}

// RangeNewest returns the most recent samples for one metric in
// [from, to] (unix ms; to == 0 means no upper bound), oldest first after the
// newest-first LIMIT has been applied. Charts use it so a 5000-point cap
// always covers the latest window instead of the oldest rows.
func (s *Store) RangeNewest(
	ctx context.Context,
	name string,
	from, to int64,
	limit int,
) ([]Sample, error) {
	if limit <= 0 || limit > 5000 {
		limit = 5000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT ts, value, attrs FROM metric_samples
		WHERE name = ? AND ts >= ?
		  AND (? = 0 OR ts <= ?)
		ORDER BY ts DESC
		LIMIT ?`, name, from, to, to, limit)
	if err != nil {
		return nil, fmt.Errorf("metrics: range newest %q: %w", name, err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "metrics: close range rows failed", rows.Close())
	}()
	var newest []Sample
	for rows.Next() {
		var (
			ts        int64
			value     float64
			attrsText string
		)
		if err := rows.Scan(&ts, &value, &attrsText); err != nil {
			return nil, fmt.Errorf("metrics: scan range: %w", err)
		}
		var attrs map[string]string
		if attrsText != "" && attrsText != "{}" {
			if err := json.Unmarshal([]byte(attrsText), &attrs); err != nil {
				telemetry.WarnErr(ctx,
					"metrics: skip sample with invalid attrs", err)
				continue
			}
		}
		newest = append(newest, Sample{
			Name: name, Ts: ts, Value: value, Attrs: attrs,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("metrics: range rows: %w", err)
	}
	for i, j := 0, len(newest)-1; i < j; i, j = i+1, j-1 {
		newest[i], newest[j] = newest[j], newest[i]
	}
	return newest, nil
}
