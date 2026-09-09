// telemetry.go wires flowcraft's OpenTelemetry pipelines for
// opencraft: a tracer provider (so emitted log records carry
// trace_id/span_id), a metric pipeline, and log sinks. Logs go to a
// rotating file and/or an OTLP collector; nothing is written to the
// console, keeping the TUI and the execd stdio protocol clean.
//
// The convenience log helpers (Info/Warn/Error) forward to flowcraft's
// global OTel logger, whose scope name is set to "opencraft" by InitOtel.
package telemetry

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/GizClaw/flowcraft/core/telemetry"
	"github.com/GizClaw/flowcraft/core/telemetry/logfile"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/log"

	"github.com/GizClaw/opencraft/internal/foundation/version"
)

// ServiceName identifies opencraft in exported telemetry
// (service.name resource attribute).
const ServiceName = "opencraft"

// TelemetryOptions configures the telemetry pipelines. All fields are
// optional; a zero TelemetryOptions installs a working no-op setup
// (valid trace IDs are
// still generated for log correlation, nothing is exported).
type TelemetryOptions struct {
	// OTLPEndpoint is the OTLP/HTTP collector endpoint host[:port].
	// A leading http:// or https:// scheme is accepted and stripped.
	// Empty disables the OTLP sink. Examples: "otel-collector:4318",
	// "api.honeycomb.io", "localhost:4318".
	OTLPEndpoint string

	// OTLPInsecure disables TLS for the OTLP exporter. Endpoints with
	// an http:// scheme and loopback hosts are always treated as
	// insecure regardless of this field.
	OTLPInsecure bool

	// LogFile enables a rotating plain-text file sink at this path
	// (100MB / 7 backups / 30 days, matching flowcraft's logfile
	// defaults). Empty disables the file sink.
	LogFile string
}

// InitOtel builds the tracer and logger pipelines and installs them as the
// global OTel providers. It returns a shutdown function that flushes
// and tears down the pipelines in reverse order; call it on exit (with
// a timeout context) so batched records drain.
func InitOtel(ctx context.Context, opts TelemetryOptions) (shutdown func(context.Context) error, err error) {
	endpoint, insecure := normalizeOTLP(opts.OTLPEndpoint, opts.OTLPInsecure)

	// The tracer is always initialized: flowcraft emits spans during
	// graph execution, and without a provider the log records would
	// never carry trace_id/span_id for correlation.
	initOpts := []telemetry.Option{
		telemetry.TracerOpts(
			telemetry.WithServiceName(ServiceName),
			telemetry.WithServiceVersion(version.ServiceVersion),
		),
	}

	logOpts := []telemetry.LogOption{
		telemetry.WithLogServiceName(ServiceName),
		telemetry.WithLogServiceVersion(version.ServiceVersion),
	}

	if endpoint != "" {
		cfg := telemetry.OTLPConfig{
			Endpoint: endpoint,
			Insecure: insecure,
		}
		initOpts = append(initOpts,
			telemetry.TracerOpts(telemetry.WithOTLPTraceExporter(cfg)),
			telemetry.MeterOpts(
				telemetry.WithOTLPMeterExporter(cfg),
				// Go runtime metrics (GC, memory, goroutines)
				// with the default 15s read interval, so the
				// collector gets signal even before app
				// instruments fire.
				telemetry.WithRuntimeMetrics(0),
			),
			telemetry.LoggerOpts(telemetry.WithOTLPLogProcessor(cfg)),
		)
	}

	if opts.LogFile != "" {
		if err := os.MkdirAll(filepath.Dir(opts.LogFile), 0o700); err != nil {
			return nil, fmt.Errorf("telemetry: create log dir: %w", err)
		}
		exp, err := logfile.NewExporter(logfile.Config{Path: opts.LogFile})
		if err != nil {
			return nil, fmt.Errorf("telemetry: log file: %w", err)
		}
		logOpts = append(logOpts,
			telemetry.WithLogProcessor(log.NewBatchProcessor(exp)),
		)
	}

	initOpts = append(initOpts, telemetry.LoggerOpts(logOpts...))
	telemetry.SetLoggerName(ServiceName)
	return telemetry.InitAll(ctx, initOpts...)
}

// Meter returns the application-scoped meter ("opencraft"). Instruments
// created from it are no-ops until InitOtel installs a provider, and the
// OpenTelemetry global meter delegates recreate pre-init instruments once the
// SDK is set, so package-level instrumentation may safely be created early.
func Meter() metric.Meter {
	return otel.Meter("opencraft")
}

// MustFloat64Histogram builds a named histogram or panics; instrument names
// and units are static, so an error here is a programming mistake that should
// fail startup instead of silently disabling metrics.
func MustFloat64Histogram(
	name string, opts ...metric.Float64HistogramOption,
) metric.Float64Histogram {
	hist, err := Meter().Float64Histogram(name, opts...)
	if err != nil {
		panic(fmt.Sprintf("telemetry: create histogram %q: %v", name, err))
	}
	return hist
}

// MustInt64Counter builds a named counter or panics; see MustFloat64Histogram.
func MustInt64Counter(
	name string, opts ...metric.Int64CounterOption,
) metric.Int64Counter {
	counter, err := Meter().Int64Counter(name, opts...)
	if err != nil {
		panic(fmt.Sprintf("telemetry: create counter %q: %v", name, err))
	}
	return counter
}

// SampleHistogram records one value on a named opencraft histogram. It is a
// convenience for one-off samples (for example desktop startup time) where a
// persistent instrument is not worth caching; instruments are no-ops when no
// OTLP metric pipeline is configured.
func SampleHistogram(ctx context.Context, name, unit string, value float64) {
	hist, err := Meter().Float64Histogram(
		name, metric.WithUnit(unit))
	if err != nil {
		return
	}
	hist.Record(ctx, value)
}

// normalizeOTLP strips an optional scheme (http:// forces insecure,
// https:// forces TLS) and auto-enables insecure mode for loopback
// endpoints so local collectors work without an extra flag.
func normalizeOTLP(endpoint string, insecure bool) (string, bool) {
	switch {
	case strings.HasPrefix(endpoint, "http://"):
		return strings.TrimPrefix(endpoint, "http://"), true
	case strings.HasPrefix(endpoint, "https://"):
		return strings.TrimPrefix(endpoint, "https://"), false
	}
	if !insecure && isLoopback(endpoint) {
		return endpoint, true
	}
	return endpoint, insecure
}

func isLoopback(endpoint string) bool {
	host := endpoint
	if h, _, err := net.SplitHostPort(endpoint); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	return host == "localhost" || host == "127.0.0.1" || strings.HasPrefix(host, "::1")
}
