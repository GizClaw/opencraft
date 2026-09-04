package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"
)

func TestNormalizeOTLP(t *testing.T) {
	cases := []struct {
		endpoint string
		insecure bool
		wantEP   string
		wantIns  bool
	}{
		{"otel-collector:4318", false, "otel-collector:4318", false},
		{"localhost:4318", false, "localhost:4318", true},
		{"127.0.0.1:4318", false, "127.0.0.1:4318", true},
		{"http://collector:4318", false, "collector:4318", true},
		{"https://collector:4318", true, "collector:4318", false},
		{"collector:4318", true, "collector:4318", true},
		{"", false, "", false},
	}
	for _, tc := range cases {
		gotEP, gotIns := normalizeOTLP(tc.endpoint, tc.insecure)
		if gotEP != tc.wantEP || gotIns != tc.wantIns {
			t.Errorf("normalizeOTLP(%q, %v) = (%q, %v), want (%q, %v)",
				tc.endpoint, tc.insecure, gotEP, gotIns, tc.wantEP, tc.wantIns)
		}
	}
}

func TestInit_NoopIsSafe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	shutdown, err := InitOtel(ctx, TelemetryOptions{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	// These must not panic even though no sink is attached.
	telemetry.Info(ctx, "noop info")
	telemetry.Warn(ctx, "noop warn")
	telemetry.Error(ctx, "noop error")
	if err := shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestInit_InvalidOTLPEndpointReportsError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// The exporter construction is lazy, but a malformed endpoint
	// fails at option-build time and must surface from Init.
	shutdown, err := InitOtel(ctx, TelemetryOptions{
		OTLPEndpoint: "://bad",
	})
	if err == nil {
		if shutdown != nil {
			_ = shutdown(ctx)
		}
		t.Fatal("expected error for malformed OTLP endpoint")
	}
}

func TestInit_LogFileSinkWritesRecords(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "logs", "opencraft.log")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	shutdown, err := InitOtel(ctx, TelemetryOptions{LogFile: logPath})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	telemetry.Info(ctx, "logged-info-record")
	telemetry.Warn(ctx, "logged-warning-record")
	telemetry.Error(ctx, "logged-error-record")
	if err := shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	out := string(b)
	for _, want := range []string{"logged-info-record", "logged-warning-record", "logged-error-record"} {
		if !strings.Contains(out, want) {
			t.Errorf("log file missing %q:\n%s", want, out)
		}
	}
}

func TestInit_OTLPMetricsDeliveredToCollector(t *testing.T) {
	var metricHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Accept traces/logs too; only metric exports are counted.
		if strings.HasPrefix(r.URL.Path, "/v1/metrics") {
			metricHits.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	shutdown, err := InitOtel(ctx, TelemetryOptions{OTLPEndpoint: strings.TrimPrefix(srv.URL, "http://")})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	counter, err := telemetry.Meter().Int64Counter("opencraft.test.counter")
	if err != nil {
		t.Fatalf("create counter: %v", err)
	}
	counter.Add(ctx, 1)

	// Shutdown forces the periodic reader's final collection and
	// export; this is the wiring boundary the test covers.
	if err := shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if metricHits.Load() == 0 {
		t.Fatalf("collector saw 0 metric requests; expected at least 1")
	}
}
