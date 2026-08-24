package usage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestRecordAndSummary(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "user.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
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
