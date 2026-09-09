package desktop

import (
	"context"
	"runtime"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"

	metricstore "github.com/GizClaw/opencraft/internal/capabilities/telemetry/metric"
)

// runtimeMetricInterval is the Go memory/GC sampling period.
const runtimeMetricInterval = 15 * time.Second

// startRuntimeMetrics begins sampling Go memory and GC counters into the
// user-level metric store. It is a no-op when the sampler already runs or
// the user database is unavailable.
func (d *Desktop) startRuntimeMetrics() {
	if d.runtimeMetricsStop != nil {
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	d.runtimeMetricsStop = stop
	d.runtimeMetricsDone = done
	go func() {
		defer close(done)
		ticker := time.NewTicker(runtimeMetricInterval)
		defer ticker.Stop()
		d.sampleRuntimeMetrics()
		for {
			select {
			case <-ticker.C:
				d.sampleRuntimeMetrics()
			case <-stop:
				return
			}
		}
	}()
}

// sampleRuntimeMetrics reads MemStats once and persists one batch. Writes
// use a background context because the application context is already
// canceled when service shutdown starts.
func (d *Desktop) sampleRuntimeMetrics() {
	ctx := context.Background()
	store := d.metricsStore()
	if store == nil {
		return
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	now := time.Now().UTC().UnixMilli()
	samples := []metricstore.Sample{
		{Name: "go.mem.heap_alloc", Ts: now,
			Value: float64(ms.HeapAlloc)},
		{Name: "go.mem.heap_sys", Ts: now,
			Value: float64(ms.HeapSys)},
		{Name: "go.mem.heap_objects", Ts: now,
			Value: float64(ms.HeapObjects)},
		{Name: "go.mem.alloc_bytes", Ts: now,
			Value: float64(ms.TotalAlloc)},
		{Name: "go.mem.alloc_ops", Ts: now,
			Value: float64(ms.Mallocs)},
		{Name: "go.gc.count", Ts: now,
			Value: float64(ms.NumGC)},
		{Name: "go.gc.pause_total_ms", Ts: now,
			Value: float64(ms.PauseTotalNs) / 1e6},
		{Name: "go.runtime.goroutines", Ts: now,
			Value: float64(runtime.NumGoroutine())},
	}
	if err := store.RecordBatch(ctx, samples); err != nil {
		telemetry.WarnErr(ctx,
			"desktop: record runtime metrics failed", err)
	}
}

// metricsStore returns the user-level metric store when the user database is
// open, nil otherwise.
func (d *Desktop) metricsStore() *metricstore.Store {
	if d == nil || d.core == nil || d.core.Runtime == nil {
		return nil
	}
	mgr := d.core.Runtime.Manager()
	if mgr == nil {
		return nil
	}
	return mgr.MetricsStore()
}

// stopRuntimeMetrics stops the sampler and waits for its final write to
// drain before the user database closes.
func (d *Desktop) stopRuntimeMetrics() {
	stop := d.runtimeMetricsStop
	if stop == nil {
		return
	}
	close(stop)
	if done := d.runtimeMetricsDone; done != nil {
		<-done
	}
	d.runtimeMetricsStop = nil
	d.runtimeMetricsDone = nil
}
