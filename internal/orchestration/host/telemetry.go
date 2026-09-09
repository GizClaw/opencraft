package host

import (
	"context"

	"github.com/GizClaw/flowcraft/core/telemetry"
	octelemetry "github.com/GizClaw/opencraft/internal/capabilities/telemetry"

	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
)

var (
	turnDurationHistogram = octelemetry.MustFloat64Histogram(
		"turn.duration_ms",
		metric.WithUnit("ms"),
		metric.WithDescription("Host-measured wall time of one turn"))
	turnCompletedCounter = octelemetry.MustInt64Counter(
		"turn.completed.total",
		metric.WithDescription("Completed turns by terminal status"))
)

// recordTurnMetrics records one finished turn: a completion counter and a
// duration histogram (both by status), plus an informational log record so
// the sample is visible in the local log file even without an OTLP sink.
func recordTurnMetrics(ctx context.Context, status string, durationMs int64) {
	attrs := metric.WithAttributes(attribute.String("status", status))
	turnCompletedCounter.Add(ctx, 1, attrs)
	turnDurationHistogram.Record(ctx, float64(durationMs), attrs)
	telemetry.Info(ctx, "host: turn finished",
		otellog.String("status", status),
		otellog.Int64("duration_ms", durationMs))
}
