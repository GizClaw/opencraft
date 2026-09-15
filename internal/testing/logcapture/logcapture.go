// Package logcapture records what flowcraft's convenience log helpers
// emit, so a test can assert on host logging (with attributes) without
// installing a sink of its own.
package logcapture

import (
	"context"
	"sync"
	"testing"

	coretelemetry "github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// Recorder collects the records emitted while it is installed.
type Recorder struct {
	mu      sync.Mutex
	records []sdklog.Record
}

// Install routes the telemetry convenience loggers into a Recorder for
// the rest of the test and restores the previous provider on cleanup.
func Install(t *testing.T) *Recorder {
	t.Helper()
	recorder := &Recorder{}
	stop, err := coretelemetry.InitLog(context.Background(),
		coretelemetry.WithLogProcessor(recorder))
	if err != nil {
		t.Fatalf("install log capture: %v", err)
	}
	t.Cleanup(func() {
		if err := stop(context.Background()); err != nil {
			t.Errorf("shutdown log capture: %v", err)
		}
	})
	return recorder
}

func (r *Recorder) Enabled(
	context.Context, sdklog.EnabledParameters,
) bool {
	return true
}

func (r *Recorder) OnEmit(
	_ context.Context, record *sdklog.Record,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, record.Clone())
	return nil
}

func (r *Recorder) Shutdown(context.Context) error   { return nil }
func (r *Recorder) ForceFlush(context.Context) error { return nil }

// Records returns a copy of the records emitted so far.
func (r *Recorder) Records() []sdklog.Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]sdklog.Record(nil), r.records...)
}

// Bodies returns the body of every record emitted so far, in order.
func (r *Recorder) Bodies() []string {
	var bodies []string
	for _, record := range r.Records() {
		bodies = append(bodies, record.Body().AsString())
	}
	return bodies
}

// Attribute returns the value of one attribute, or "" when the record
// does not carry it. Value.String covers every attribute kind, so an
// int64 pid reads the same way as a string.
func Attribute(record sdklog.Record, key string) string {
	value := ""
	record.WalkAttributes(func(kv otellog.KeyValue) bool {
		if kv.Key == key {
			value = kv.Value.String()
		}
		return true
	})
	return value
}
