package desktop

import (
	"context"
	"errors"
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
