package headless

import (
	"context"
	"testing"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

func TestOpenUserUsageRecordsSessionUsage(t *testing.T) {
	udb, store, err := openUserUsage(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open user usage: %v", err)
	}
	t.Cleanup(func() { _ = udb.Close() })

	if err := store.RecordSessionUsage(
		context.Background(), "ws-headless", "s-1", ocsessions.Usage{
			Model:        "openai-1/gpt-test",
			InputTokens:  10,
			OutputTokens: 5,
			TotalTokens:  15,
		},
	); err != nil {
		t.Fatalf("record session usage: %v", err)
	}
	rows, err := store.Summary(context.Background())
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if len(rows) != 1 || rows[0].Model != "openai-1/gpt-test" ||
		rows[0].InputTokens != 10 || rows[0].OutputTokens != 5 {
		t.Fatalf("summary rows = %+v", rows)
	}
}
