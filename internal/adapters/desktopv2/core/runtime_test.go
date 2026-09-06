package core

import (
	"context"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

func TestRuntimeOpenUserDB(t *testing.T) {
	dir := t.TempDir()
	rt := NewRuntime(dir, dir)
	t.Cleanup(rt.Close)

	if err := rt.OpenUserDB(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rt.Usage() == nil {
		t.Fatal("usage store not attached")
	}
	if rt.Automations() == nil {
		t.Fatal("automation store not attached")
	}
	if err := rt.OpenUserDB(context.Background()); err != nil {
		t.Fatalf("second OpenUserDB should be idempotent: %v", err)
	}
}

func TestRuntimeRecordTurnUsagePersistsModelRows(t *testing.T) {
	dir := t.TempDir()
	rt := NewRuntime(dir, dir)
	t.Cleanup(rt.Close)

	if err := rt.OpenUserDB(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := rt.recordTurnUsage(context.Background(), "ws-a", "s-1", sessions.Usage{
		Model:            "gpt-test",
		InputTokens:      100,
		OutputTokens:     50,
		TotalTokens:      150,
		CacheReadTokens:  20,
		CacheWriteTokens: 10,
		ReasoningTokens:  5,
		LatencyMs:        456,
	}, time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("record turn usage: %v", err)
	}

	rows, err := rt.Usage().Summary(context.Background())
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("summary rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Model != "gpt-test" {
		t.Fatalf("model = %q", row.Model)
	}
	if row.InputTokens != 100 || row.OutputTokens != 50 ||
		row.CacheReadTokens != 20 || row.CacheWriteTokens != 10 ||
		row.ReasoningTokens != 5 || row.LatencyMs != 456 ||
		row.Calls != 1 {
		t.Fatalf("summary row = %+v", row)
	}
	if row.Workspaces != 1 || row.Sessions != 1 {
		t.Fatalf("summary counts = %+v", row)
	}
}
