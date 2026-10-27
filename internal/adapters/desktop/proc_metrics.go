package desktop

import (
	"context"
	"errors"
	"runtime"
	"runtime/debug"
	"strconv"

	"github.com/GizClaw/flowcraft/core/telemetry"

	metricstore "github.com/GizClaw/opencraft/internal/capabilities/telemetry/metric"
	"github.com/GizClaw/opencraft/internal/foundation/platform/procmem"
)

// The process-family series. Go memory says what the heap in this process
// does; these say what the app costs the machine — the WebKit renderers a
// window keeps alive are several times the heap and invisible to Go, so
// they are the series a window or a preview pane leaking into shows up in.
const (
	// procMetricFootprint is one sample per family member, split by role
	// (self, child, webcontent, gpu, networking, helper) and carrying the
	// process name and pid in its attributes.
	procMetricFootprint = "proc.mem.footprint"
	// procMetricFamilyTotal is their sum, the headline number, with the
	// number of processes and how membership was resolved.
	procMetricFamilyTotal = "proc.mem.family_total"
)

// procMetricEvery is how many Go-memory ticks pass between two family
// snapshots (a minute at runtimeMetricInterval). A snapshot writes a row
// per process, and what it measures — a window or a document the webview
// keeps alive — grows over minutes: the resolution is spent where it costs
// no series the panel needs.
const procMetricEvery = 4

// heapIdleFloor is how much allocated-but-idle heap is worth returning. Go
// grows the heap to twice what is live and does not give the pages back on its
// own; a desktop process that folds a large conversation settles at the peak and
// stays there, which reads as an RSS that never drops. Returning them costs one
// collection and is only worth it when there is something to return.
const heapIdleFloor = 64 << 20

// returnIdleHeap hands back the pages a peak left behind, once the heap is
// mostly idle. It runs on the metrics tick rather than on every collection: the
// point is to reclaim after the burst, not to pay a full sweep during it.
func returnIdleHeap() {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	if stats.HeapSys < stats.HeapAlloc {
		return
	}
	if stats.HeapSys-stats.HeapAlloc < heapIdleFloor {
		return
	}
	debug.FreeOSMemory()
}

// procSamples renders one snapshot of the process family, or nothing on a
// platform without a probe.
func procSamples(now int64) []metricstore.Sample {
	family, err := procmem.Snapshot()
	if err != nil {
		if !errors.Is(err, procmem.ErrUnsupported) {
			telemetry.WarnErr(context.Background(),
				"desktop: process family snapshot failed", err)
		}
		return nil
	}
	return familySamples(now, family)
}

// familySamples renders a snapshot: one sample per member plus the family
// total.
func familySamples(now int64, family procmem.Family) []metricstore.Sample {
	samples := make([]metricstore.Sample, 0, len(family.Members)+1)
	for _, member := range family.Members {
		samples = append(samples, metricstore.Sample{
			Name:  procMetricFootprint,
			Ts:    now,
			Value: float64(member.Footprint),
			Attrs: map[string]string{
				"role": string(member.Role),
				"name": member.Name,
				"pid":  strconv.Itoa(member.PID),
			},
		})
	}
	samples = append(samples, metricstore.Sample{
		Name:  procMetricFamilyTotal,
		Ts:    now,
		Value: float64(family.Total()),
		Attrs: map[string]string{
			"source":    family.Source,
			"processes": strconv.Itoa(len(family.Members)),
		},
	})
	return samples
}
