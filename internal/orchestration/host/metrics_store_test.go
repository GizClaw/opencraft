package host

import (
	"context"
	"testing"
)

func TestManagerMetricsStoreLifecycle(t *testing.T) {
	m := NewManagerAt(t.TempDir(), t.TempDir())
	ctx := context.Background()
	if got := m.MetricsStore(); got != nil {
		t.Fatal("metrics store must be nil before OpenUserDB")
	}
	if err := m.OpenUserDB(ctx); err != nil {
		t.Fatalf("open user db: %v", err)
	}
	store := m.MetricsStore()
	if store == nil {
		t.Fatal("metrics store must attach after OpenUserDB")
	}
	m.RecordMetric(ctx, "turn.duration_ms", 123,
		map[string]string{"status": "completed"})
	got, err := store.Range(ctx, "turn.duration_ms", 0, 0, 10)
	if err != nil {
		t.Fatalf("range metric: %v", err)
	}
	if len(got) != 1 || got[0].Value != 123 {
		t.Fatalf("recorded metric = %+v, want one 123ms sample", got)
	}
	m.CloseUserDB()
	if got := m.MetricsStore(); got != nil {
		t.Fatal("metrics store must clear after CloseUserDB")
	}
	m.RecordMetric(ctx, "turn.duration_ms", 1, nil) // no-op, no panic
}
