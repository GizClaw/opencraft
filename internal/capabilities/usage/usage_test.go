package usage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
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
	at := time.Date(2026, 9, 1, 8, 30, 0, 0, time.UTC)

	// Same model across two workspaces and several sessions.
	if err := store.Record(ctx, "ws-a", "s-1", "deepseek-chat", at, Usage{
		InputTokens:      100,
		OutputTokens:     20,
		CacheReadTokens:  10,
		CacheWriteTokens: 5,
		Calls:            2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(ctx, "ws-a", "s-2", "deepseek-chat", at, Usage{
		InputTokens:  50,
		OutputTokens: 10,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(ctx, "ws-b", "s-3", "deepseek-chat", at, Usage{
		InputTokens:     200,
		OutputTokens:    40,
		ReasoningTokens: 5,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(ctx, "ws-a", "s-1", "gpt-4o", at, Usage{
		InputTokens:  30,
		OutputTokens: 5,
	}); err != nil {
		t.Fatal(err)
	}
	// Empty model is ignored.
	if err := store.Record(ctx, "ws-a", "s-1", "", at, Usage{InputTokens: 1}); err != nil {
		t.Fatal(err)
	}

	rows, err := store.Summary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("summary rows = %d, want 2: %+v", len(rows), rows)
	}
	// deepseek-chat totals 350 in / 70 out across 2 workspaces / 3
	// sessions, with the cache-write and calls columns preserved.
	first := rows[0]
	if first.Model != "deepseek-chat" ||
		first.TotalTokens != 420 || first.InputTokens != 350 ||
		first.OutputTokens != 70 ||
		first.CacheReadTokens != 10 || first.CacheWriteTokens != 5 ||
		first.ReasoningTokens != 5 || first.Calls != 2 ||
		first.Workspaces != 2 || first.Sessions != 3 {
		t.Fatalf("deepseek summary = %+v", first)
	}
	if rows[1].Model != "gpt-4o" || rows[1].InputTokens != 30 ||
		rows[1].TotalTokens != 35 {
		t.Fatalf("gpt summary = %+v", rows[1])
	}
}

func TestRecordSessionUsage(t *testing.T) {
	store, _ := newUsageStore(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

	if err := store.RecordSessionUsage(ctx, "ws-a", "s-1", ocsessions.Usage{
		Model:            "deepseek-chat",
		InputTokens:      100,
		OutputTokens:     20,
		TotalTokens:      120,
		CacheReadTokens:  10,
		CacheWriteTokens: 2,
		ReasoningTokens:  5,
		LatencyMs:        456,
	}, at); err != nil {
		t.Fatal(err)
	}
	// A second delta accumulates onto the same (workspace, session,
	// model) row instead of replacing it.
	if err := store.RecordSessionUsage(ctx, "ws-a", "s-1", ocsessions.Usage{
		Model:        "deepseek-chat",
		InputTokens:  50,
		OutputTokens: 10,
		TotalTokens:  60,
	}, at); err != nil {
		t.Fatal(err)
	}
	// Empty model or zero totals are ignored.
	if err := store.RecordSessionUsage(ctx, "ws-a", "s-1", ocsessions.Usage{
		TotalTokens: 1,
	}, at); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordSessionUsage(ctx, "ws-a", "s-1", ocsessions.Usage{
		Model: "deepseek-chat",
	}, at); err != nil {
		t.Fatal(err)
	}

	rows, err := store.Summary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("summary rows = %d, want 1: %+v", len(rows), rows)
	}
	row := rows[0]
	if row.Model != "deepseek-chat" ||
		row.TotalTokens != 180 || row.InputTokens != 150 ||
		row.OutputTokens != 30 ||
		row.CacheReadTokens != 10 || row.CacheWriteTokens != 2 ||
		row.ReasoningTokens != 5 || row.LatencyMs != 456 ||
		row.Calls != 2 {
		t.Fatalf("summary row = %+v", row)
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
		write  int64
		reason int64
	}{
		{"2026-01-01T15:00:00Z", 100, 20, 10, 2, 0},
		{"2026-01-01T17:00:00Z", 50, 10, 0, 0, 5},
		{"2026-01-02T09:00:00Z", 30, 5, 0, 0, 0},
	} {
		if _, err := store.db.ExecContext(ctx, `
			INSERT INTO model_usage_hourly (
				model, hour, input_tokens, output_tokens,
				cache_read_tokens, cache_write_tokens,
				reasoning_tokens, latency_ms, calls
			) VALUES (?, ?, ?, ?, ?, ?, ?, 0, 1)
		`, "deepseek-chat", row.hour, row.input, row.output,
			row.cache, row.write, row.reason); err != nil {
			t.Fatal(err)
		}
	}

	hourly, err := store.Series(ctx, "deepseek-chat", GranularityHour, 0, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hourly) != 3 {
		t.Fatalf("hourly points = %d, want 3: %+v", len(hourly), hourly)
	}
	if hourly[0].Time != "2026-01-01T15:00:00Z" ||
		hourly[0].InputTokens != 100 || hourly[0].OutputTokens != 20 ||
		hourly[0].CacheWriteTokens != 2 {
		t.Fatalf("hourly[0] = %+v", hourly[0])
	}
	if hourly[2].ReasoningTokens != 0 {
		t.Fatalf("hourly[2] = %+v", hourly[2])
	}

	// UTC day grouping: all three rows fall on 2026-01-01 / 2026-01-02.
	utcDays, err := store.Series(ctx, "deepseek-chat", GranularityDay, 0, "", "")
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
	localDays, err := store.Series(ctx, "deepseek-chat", GranularityDay, 480, "", "")
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
		"deepseek-chat",
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

func TestRecordAttributionUsesReportTime(t *testing.T) {
	store, _ := newUsageStore(t)
	ctx := context.Background()
	// One turn reports at 23:50Z and again at 00:10Z the next day. The
	// hourly buckets follow the report hours, not the turn-finish time.
	if err := store.Record(
		ctx, "ws-a", "s-1", "deepseek-chat",
		time.Date(2026, 9, 1, 23, 50, 0, 0, time.UTC),
		Usage{InputTokens: 100, OutputTokens: 20},
	); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(
		ctx, "ws-a", "s-1", "deepseek-chat",
		time.Date(2026, 9, 2, 0, 10, 0, 0, time.UTC),
		Usage{InputTokens: 50, OutputTokens: 10},
	); err != nil {
		t.Fatal(err)
	}

	hourly, err := store.Series(ctx, "deepseek-chat", GranularityHour, 0, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hourly) != 2 ||
		hourly[0].Time != "2026-09-01T23:00:00Z" ||
		hourly[0].InputTokens != 100 ||
		hourly[1].Time != "2026-09-02T00:00:00Z" ||
		hourly[1].InputTokens != 50 {
		t.Fatalf("hourly attribution = %+v", hourly)
	}
	// In UTC+8 both reports land on the same local calendar day.
	localDays, err := store.Series(ctx, "deepseek-chat", GranularityDay, 480, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(localDays) != 1 ||
		localDays[0].Time != "2026-09-02" ||
		localDays[0].InputTokens != 150 {
		t.Fatalf("local day attribution = %+v", localDays)
	}
}

func TestRecordNormalizesProviderPrefixedModel(t *testing.T) {
	store, _ := newUsageStore(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 1, 8, 30, 0, 0, time.UTC)

	// Legacy import bundles and pre-migration rows can still carry
	// "provider/name" keys; both write paths must reduce them to the
	// name-only invariant so the same model never splits across rows.
	if err := store.Record(ctx, "ws-a", "s-1", "openai/gpt-test", at, Usage{
		TotalTokens:  120,
		InputTokens:  100,
		OutputTokens: 20,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordSessionUsage(ctx, "ws-a", "s-1", ocsessions.Usage{
		Model:        "azure/gpt-test",
		TotalTokens:  60,
		InputTokens:  50,
		OutputTokens: 10,
	}, at); err != nil {
		t.Fatal(err)
	}

	rows, err := store.Summary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("summary rows = %d, want 1: %+v", len(rows), rows)
	}
	if rows[0].Model != "gpt-test" ||
		rows[0].TotalTokens != 180 ||
		rows[0].InputTokens != 150 ||
		rows[0].Sessions != 1 {
		t.Fatalf("normalized summary = %+v", rows[0])
	}
}

func TestSessionCountCountsSessionOnceAcrossModels(t *testing.T) {
	store, _ := newUsageStore(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 1, 8, 30, 0, 0, time.UTC)
	for _, m := range []struct {
		workspace, session, model string
	}{
		{"ws-a", "s-1", "model-a"},
		{"ws-a", "s-1", "model-b"},
		{"ws-a", "s-2", "model-a"},
		{"ws-b", "s-3", "model-a"},
	} {
		if err := store.Record(ctx, m.workspace, m.session, m.model, at, Usage{
			TotalTokens: 10,
			InputTokens: 10,
		}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := store.SessionCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("session count = %d, want 3", n)
	}
}
