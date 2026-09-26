// Usage and metrics: the per-turn accounting that reaches the
// user-level recorder and the telemetry metric store.

package host

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	metricstore "github.com/GizClaw/opencraft/internal/capabilities/telemetry/metric"

	otellog "go.opentelemetry.io/otel/log"
)

// UsageRecorder receives one model usage delta (a finished turn, one
// auto-title call, or an imported session) so adapters can persist
// user-level accounting rows. workspaceID and sessionID are explicit;
// the recorder must not re-derive them from process state. at is the
// moment the engine reported the usage (report-arrival time) and
// drives the user-level hourly bucket. Errors are logged by the host
// and never fail the turn.
type UsageRecorder func(
	ctx context.Context,
	workspaceID, sessionID string,
	usage sessions.Usage,
	at time.Time,
) error

// usageDelta is one model usage slice with the moment it was reported.
// A turn can span several models and hours; each engine report becomes
// its own bucket so statistics keep both dimensions accurate.
type usageDelta struct {
	usage sessions.Usage
	at    time.Time
}

// SetUsageObserver installs a host-level usage reporter for
// non-run generations such as automatic titles.
func (m *Manager) SetUsageObserver(fn func(context.Context, inference.Usage)) {
	m.mu.Lock()
	m.usageObserver = fn
	m.mu.Unlock()
}

// SetUsageRecorder installs the user-level usage sink. A finished turn
// may deliver several deltas (one per model + hour bucket), plus one
// for each auto-title/background generation. UI and automation turns
// share the Host, so one recorder covers both paths. A nil fn restores
// the default recorder, which writes into the usage store attached by
// OpenUserDB (a no-op before the store is attached).
func (m *Manager) SetUsageRecorder(fn UsageRecorder) {
	m.mu.Lock()
	if fn == nil {
		fn = m.recordUserUsage
	}
	m.usageRecorder = fn
	m.mu.Unlock()
}

// RecordMetric persists one local metric sample through the attached store.
// It is a no-op before OpenUserDB and never fails the caller: persistence is
// best-effort and errors are logged.
func (m *Manager) RecordMetric(
	ctx context.Context,
	name string,
	value float64,
	attrs map[string]string,
) {
	m.mu.Lock()
	store := m.userMetrics
	m.mu.Unlock()
	if store == nil {
		return
	}
	if err := store.Record(ctx, name, value, attrs); err != nil {
		telemetry.WarnErr(ctx, "host: record local metric failed",
			err, otellog.String("metric", name))
	}
}

// MetricSample is one local metric observation staged for a batch write.
// The timestamp is not part of the payload: RecordMetrics stamps every
// sample in one call with the same ts, so correlated series (the renderer
// probe reports several per batch) line up on the charts.
type MetricSample struct {
	Name  string
	Value float64
	Attrs map[string]string
}

// RecordMetrics persists a batch of samples in one transaction with one
// shared timestamp. Like RecordMetric it is a no-op before OpenUserDB and
// never fails the caller.
func (m *Manager) RecordMetrics(ctx context.Context, samples []MetricSample) {
	m.mu.Lock()
	store := m.userMetrics
	m.mu.Unlock()
	if store == nil || len(samples) == 0 {
		return
	}
	ts := time.Now().UTC().UnixMilli()
	batch := make([]metricstore.Sample, 0, len(samples))
	for _, sample := range samples {
		batch = append(batch, metricstore.Sample{
			Name:  sample.Name,
			Ts:    ts,
			Value: sample.Value,
			Attrs: sample.Attrs,
		})
	}
	if err := store.RecordBatch(ctx, batch); err != nil {
		telemetry.WarnErr(ctx, "host: record local metrics failed", err,
			otellog.String("count", fmt.Sprint(len(batch))))
	}
}

// RecordUsage invokes the currently installed user-level usage
// recorder. It lets callers persist usage outside a run lifecycle
// (imports, tests) through the same sink the hosts use.
func (m *Manager) RecordUsage(
	ctx context.Context,
	workspaceID, sessionID string,
	usage sessions.Usage,
	at time.Time,
) error {
	m.mu.Lock()
	fn := m.usageRecorder
	m.mu.Unlock()
	if fn == nil {
		return nil
	}
	return fn(ctx, workspaceID, sessionID, usage, at)
}

// recordUserUsage is the default usage recorder: it writes into the
// usage store attached by OpenUserDB and no-ops before then, so usage
// accounting can never fail a turn when the database is unavailable.
func (m *Manager) recordUserUsage(
	ctx context.Context,
	workspaceID, sessionID string,
	usage sessions.Usage,
	at time.Time,
) error {
	m.mu.Lock()
	store := m.userUsage
	m.mu.Unlock()
	if store == nil {
		return nil
	}
	return store.RecordSessionUsage(ctx, workspaceID, sessionID, usage, at)
}

// reportUsage routes an engine usage report to the run that owns it.
func (h *Host) reportUsage(ctx context.Context, usage inference.Usage) {
	runID := ""
	conversationID := ""
	if info, ok := agent.RunInfoFromContext(ctx); ok {
		runID = info.RunID
		conversationID = info.ConversationID
	}
	delta := sessions.UsageFromReport(usage)
	h.mu.Lock()
	d := h.runs[RunID(runID)]
	if d == nil {
		h.mu.Unlock()
		// The run is gone: a generation that outlives its turn (the
		// post-turn review reports here, detached) still spent the
		// model call, so the late report lands in the user-level tables
		// and the usage observer instead of being dropped silently.
		if delta.TotalTokens <= 0 {
			return
		}
		if conversationID != "" {
			h.forwardUsageRecorder(ctx, conversationID, delta, time.Now().UTC())
		}
		if fn := h.usage; fn != nil {
			fn(ctx, usage)
		}
		return
	}
	d.usage = sessions.AddUsage(d.usage, delta)
	if delta.Model != "" && d.usageHours != nil {
		hour := time.Now().UTC().Truncate(time.Hour).Format(time.RFC3339)
		key := modelHourKey(delta.Model, hour)
		d.usageHours[key] = sessions.AddUsage(d.usageHours[key], delta)
	}
	fn := d.notify
	h.mu.Unlock()
	if fn != nil {
		fn(ctx, usage)
	}
}

func modelHourKey(model, hour string) string {
	return model + "\x00" + hour
}

func (h *Host) takeUsage(runID string) sessions.Usage {
	h.mu.Lock()
	defer h.mu.Unlock()
	if d := h.runs[RunID(runID)]; d != nil {
		usage := d.usage
		d.usage = sessions.Usage{}
		return usage
	}
	return sessions.Usage{}
}

// takeUsageDeltas drains the per-model, per-hour usage buckets of one
// run. Delays inside the run do not shift usage between hourly buckets
// because each report's hour was captured when the report arrived.
func (h *Host) takeUsageDeltas(runID string) []usageDelta {
	h.mu.Lock()
	defer h.mu.Unlock()
	d := h.runs[RunID(runID)]
	if d == nil || len(d.usageHours) == 0 {
		return nil
	}
	out := make([]usageDelta, 0, len(d.usageHours))
	for key, usage := range d.usageHours {
		model, hour, ok := strings.Cut(key, "\x00")
		if !ok || model == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339, hour)
		if err != nil {
			// Bucket keys are only written by reportUsage, so the hour
			// always parses. Ignore rather than silently mis-bucket.
			continue
		}
		usage.Model = model
		out = append(out, usageDelta{usage: usage, at: at})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].usage.Model != out[j].usage.Model {
			return out[i].usage.Model < out[j].usage.Model
		}
		return out[i].at.Before(out[j].at)
	})
	return out
}
