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

func TestManagerRecordMetricsSharesTimestamp(t *testing.T) {
	m := NewManagerAt(t.TempDir(), t.TempDir())
	ctx := context.Background()
	if err := m.OpenUserDB(ctx); err != nil {
		t.Fatalf("open user db: %v", err)
	}
	store := m.MetricsStore()
	m.RecordMetrics(ctx, []MetricSample{
		{
			Name: "frontend.dom_nodes", Value: 10,
			Attrs: map[string]string{"unit": "1"},
		},
		{
			Name: "frontend.frames", Value: 3,
			Attrs: map[string]string{"unit": "1"},
		},
		{
			Name: "frontend.dom_nodes", Value: 11,
			Attrs: map[string]string{"unit": "1"},
		},
	})
	nodes, err := store.Range(ctx, "frontend.dom_nodes", 0, 0, 10)
	if err != nil {
		t.Fatalf("range dom_nodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("dom_nodes samples = %d, want 2", len(nodes))
	}
	if nodes[0].Ts != nodes[1].Ts {
		t.Fatalf("batch samples must share one ts, got %d and %d",
			nodes[0].Ts, nodes[1].Ts)
	}
	frames, err := store.Range(ctx, "frontend.frames", 0, 0, 10)
	if err != nil {
		t.Fatalf("range frames: %v", err)
	}
	if len(frames) != 1 || frames[0].Ts != nodes[0].Ts {
		t.Fatalf("frames must share the batch ts, got %+v", frames)
	}
	// After CloseUserDB the store is detached: the call must be a no-op.
	m.CloseUserDB()
	m.RecordMetrics(ctx, []MetricSample{{Name: "frontend.frames", Value: 1}})
}
