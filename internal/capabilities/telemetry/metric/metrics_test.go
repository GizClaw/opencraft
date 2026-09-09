package metric

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/foundation/db"
	"github.com/GizClaw/opencraft/internal/orchestration/migrations"
)

func newMetricStore(t *testing.T) *Store {
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
		t.Fatalf("attach metrics store: %v", err)
	}
	return store
}

func TestStoreRangeOrdersAndReturnsAttrs(t *testing.T) {
	store := newMetricStore(t)
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Hour).UnixMilli()
	samples := []Sample{
		{Name: "turn.duration_ms", Ts: base, Value: 100,
			Attrs: map[string]string{"status": "completed"}},
		{Name: "turn.duration_ms", Ts: base + 10, Value: 300,
			Attrs: map[string]string{"status": "failed"}},
		{Name: "turn.duration_ms", Ts: base + 20, Value: 200,
			Attrs: map[string]string{"status": "completed"}},
		{Name: "other.metric", Ts: base, Value: 1},
	}
	if err := store.RecordBatch(ctx, samples); err != nil {
		t.Fatal(err)
	}
	got, err := store.Range(ctx, "turn.duration_ms", base-1, base+30, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("range len = %d, want 3", len(got))
	}
	for i, want := range []float64{100, 300, 200} {
		if got[i].Value != want {
			t.Errorf("sample %d value = %v, want %v", i, got[i].Value, want)
		}
	}
	if got[0].Attrs["status"] != "completed" {
		t.Errorf("attrs = %v, want status=completed", got[0].Attrs)
	}
}

func TestStorePrunesOutsideRetention(t *testing.T) {
	store := newMetricStore(t)
	ctx := context.Background()
	old := time.Now().UTC().Add(-retention - time.Hour).UnixMilli()
	fresh := time.Now().UTC().UnixMilli()
	if err := store.RecordBatch(ctx, []Sample{
		{Name: "go.gc.count", Ts: old, Value: 1},
		{Name: "go.gc.count", Ts: fresh, Value: 2},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Range(ctx, "go.gc.count", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Value != 2 {
		t.Fatalf("range after prune = %+v, want only fresh sample", got)
	}
}

func TestStoreRangeCapsLimit(t *testing.T) {
	store := newMetricStore(t)
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Hour).UnixMilli()
	samples := make([]Sample, 0, 10)
	for i := 0; i < 10; i++ {
		samples = append(samples, Sample{
			Name: "desktop.startup_ms", Ts: base + int64(i), Value: float64(i),
		})
	}
	if err := store.RecordBatch(ctx, samples); err != nil {
		t.Fatal(err)
	}
	got, err := store.Range(ctx, "desktop.startup_ms", 0, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("limited range len = %d, want 4", len(got))
	}
}

func TestStoreRangeNewestPrefersRecentSamples(t *testing.T) {
	store := newMetricStore(t)
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Hour).UnixMilli()
	samples := make([]Sample, 0, 10)
	for i := 0; i < 10; i++ {
		samples = append(samples, Sample{
			Name: "go.gc.count", Ts: base + int64(i), Value: float64(i),
		})
	}
	if err := store.RecordBatch(ctx, samples); err != nil {
		t.Fatal(err)
	}
	got, err := store.RangeNewest(ctx, "go.gc.count", base, base+20, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("newest range len = %d, want 4", len(got))
	}
	if got[0].Ts != base+6 || got[3].Ts != base+9 {
		t.Fatalf("newest range ts = [%d..%d], want base+6..base+9",
			got[0].Ts, got[len(got)-1].Ts)
	}
}
