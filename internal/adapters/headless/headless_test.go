package headless

import (
	"context"
	"testing"
	"time"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
)

// TestOpenUserUsageRecordsSessionUsage verifies the manager-owned
// user database path the headless run uses: OpenUserDB attaches the
// usage store and the default recorder persists through it.
func TestOpenUserUsageRecordsSessionUsage(t *testing.T) {
	dir := t.TempDir()
	mgr := host.NewManagerAt(dir, dir)
	t.Cleanup(mgr.CloseUserDB)

	if err := mgr.OpenUserDB(context.Background()); err != nil {
		t.Fatalf("open user db: %v", err)
	}
	store := mgr.UsageStore()
	if store == nil {
		t.Fatal("usage store not attached")
	}
	if err := mgr.RecordUsage(
		context.Background(), "ws-headless", "s-1", ocsessions.Usage{
			Model:        "gpt-test",
			InputTokens:  10,
			OutputTokens: 5,
			TotalTokens:  15,
		},
		time.Now().UTC(),
	); err != nil {
		t.Fatalf("record session usage: %v", err)
	}
	rows, err := store.Summary(context.Background())
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if len(rows) != 1 || rows[0].Model != "gpt-test" ||
		rows[0].InputTokens != 10 || rows[0].OutputTokens != 5 {
		t.Fatalf("summary rows = %+v", rows)
	}
}
