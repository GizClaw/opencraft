package usage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/db"
	"github.com/GizClaw/opencraft/internal/orchestration/migrations"
)

func newUsageStore(t *testing.T) (*Store, *db.DB) {
	t.Helper()
	handle, err := db.Open(filepath.Join(t.TempDir(), "user.db"))
	if err != nil {
		t.Fatalf("open user db: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := migrations.User(context.Background(), handle); err != nil {
		t.Fatalf("migrate user db: %v", err)
	}
	store, err := Attach(handle)
	if err != nil {
		t.Fatalf("attach usage store: %v", err)
	}
	return store, handle
}

func TestRecordAndSummary(t *testing.T) {
	store, _ := newUsageStore(t)
	ctx := context.Background()

	// Same model across two workspaces and several sessions.
	if err := store.Record(ctx, "ws-a", "s-1", "deepseek-1/m", Usage{
		InputTokens: 100, OutputTokens: 20, CacheReadTokens: 10,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(ctx, "ws-a", "s-2", "deepseek-1/m", Usage{
		InputTokens: 50, OutputTokens: 10,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(ctx, "ws-b", "s-3", "deepseek-1/m", Usage{
		InputTokens: 200, OutputTokens: 40, ReasoningTokens: 5,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(ctx, "ws-a", "s-1", "openai-1/g", Usage{
		InputTokens: 30, OutputTokens: 5,
	}); err != nil {
		t.Fatal(err)
	}
	// Empty model is ignored.
	if err := store.Record(ctx, "ws-a", "s-1", "", Usage{InputTokens: 1}); err != nil {
		t.Fatal(err)
	}

	rows, err := store.Summary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("summary rows = %d, want 2: %+v", len(rows), rows)
	}
	// deepseek-1/m totals 350 in / 70 out across 2 workspaces / 3 sessions.
	first := rows[0]
	if first.Model != "deepseek-1/m" ||
		first.InputTokens != 350 || first.OutputTokens != 70 ||
		first.CacheReadTokens != 10 || first.ReasoningTokens != 5 ||
		first.Workspaces != 2 || first.Sessions != 3 {
		t.Fatalf("deepseek summary = %+v", first)
	}
	if rows[1].Model != "openai-1/g" || rows[1].InputTokens != 30 {
		t.Fatalf("openai summary = %+v", rows[1])
	}
}

func TestSeriesHourAndDay(t *testing.T) {
	store, _ := newUsageStore(t)
	ctx := context.Background()

	// Directly seed UTC hours around a day boundary: 15:00Z and 16:00Z
	// are 23:00 and 00:00 the next day in UTC+8.
	for _, row := range []struct {
		hour   string
		input  int64
		output int64
		cache  int64
		reason int64
	}{
		{"2026-01-01T15:00:00Z", 100, 20, 10, 0},
		{"2026-01-01T17:00:00Z", 50, 10, 0, 5},
		{"2026-01-02T09:00:00Z", 30, 5, 0, 0},
	} {
		if _, err := store.db.ExecContext(ctx, `
			INSERT INTO model_usage_hourly (
				model, hour, input_tokens, output_tokens,
				cache_read_tokens, reasoning_tokens, latency_ms
			) VALUES (?, ?, ?, ?, ?, ?, 0)
		`, "deepseek-1/m", row.hour, row.input, row.output, row.cache, row.reason); err != nil {
			t.Fatal(err)
		}
	}

	hourly, err := store.Series(ctx, "deepseek-1/m", GranularityHour, 0, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hourly) != 3 {
		t.Fatalf("hourly points = %d, want 3: %+v", len(hourly), hourly)
	}
	if hourly[0].Time != "2026-01-01T15:00:00Z" ||
		hourly[0].InputTokens != 100 || hourly[0].OutputTokens != 20 {
		t.Fatalf("hourly[0] = %+v", hourly[0])
	}
	if hourly[2].ReasoningTokens != 0 {
		t.Fatalf("hourly[2] = %+v", hourly[2])
	}

	// UTC day grouping: all three rows fall on 2026-01-01 / 2026-01-02.
	utcDays, err := store.Series(ctx, "deepseek-1/m", GranularityDay, 0, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(utcDays) != 2 ||
		utcDays[0].Time != "2026-01-01" ||
		utcDays[1].Time != "2026-01-02" ||
		utcDays[0].InputTokens != 150 || utcDays[1].InputTokens != 30 {
		t.Fatalf("utc day points = %+v", utcDays)
	}

	// UTC+8 day grouping: 15:00Z stays on 2026-01-01 local (23:00)
	// while 17:00Z and the next-day 09:00Z move to 2026-01-02.
	localDays, err := store.Series(ctx, "deepseek-1/m", GranularityDay, 480, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(localDays) != 2 ||
		localDays[0].Time != "2026-01-01" ||
		localDays[1].Time != "2026-01-02" ||
		localDays[0].InputTokens != 100 ||
		localDays[1].InputTokens != 80 {
		t.Fatalf("local day points = %+v", localDays)
	}

	// Range filter: only the hour after 16:00Z remains, with the
	// 17:00Z row still landing on 2026-01-02 local.
	windowed, err := store.Series(
		ctx,
		"deepseek-1/m",
		GranularityDay,
		480,
		"2026-01-01T16:00:00Z",
		"2026-01-02T10:00:00Z",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(windowed) != 1 ||
		windowed[0].Time != "2026-01-02" ||
		windowed[0].InputTokens != 80 {
		t.Fatalf("windowed day points = %+v", windowed)
	}
}
