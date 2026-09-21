package telemetry

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// graphWarningBody is the record the flowcraft analyzer emits for every
// build-time finding, once per runtime assembly.
const graphWarningBody = "graph build warning"

// graphWarningFilter drops repeats of a build finding already logged in
// this process, and reports the number it dropped when the pipeline
// shuts down.
//
// The analyzer's findings are static: the same graph reports the same
// findings on every assembly. The shipped assistant graph reports five
// host-seeded board references on each one (a graph definition has no way
// to declare external inputs — see foundation/config/graph_warnings_test.go),
// so a session with a few dozen assemblies wrote hundreds of identical
// WARN lines and buried everything else in the file. The first occurrence
// of each finding is kept: that is the line that says the analyzer ran
// and what it sees. A finding that appears for the first time — a real
// change to a graph — still logs.
type graphWarningFilter struct {
	inner sdklog.Processor

	mu         sync.Mutex
	seen       map[string]struct{}
	suppressed int
}

func newGraphWarningFilter(inner sdklog.Processor) *graphWarningFilter {
	return &graphWarningFilter{inner: inner, seen: make(map[string]struct{})}
}

func (f *graphWarningFilter) Enabled(
	ctx context.Context,
	params sdklog.EnabledParameters,
) bool {
	return f.inner.Enabled(ctx, params)
}

func (f *graphWarningFilter) OnEmit(
	ctx context.Context,
	record *sdklog.Record,
) error {
	if f.repeat(record) {
		return nil
	}
	return f.inner.OnEmit(ctx, record)
}

// repeat reports whether this record is a build finding already seen,
// counting it when it is.
func (f *graphWarningFilter) repeat(record *sdklog.Record) bool {
	if record.Body().AsString() != graphWarningBody {
		return false
	}
	var graph, kind, node, message string
	record.WalkAttributes(func(kv log.KeyValue) bool {
		switch kv.Key {
		case "graph.name":
			graph = kv.Value.String()
		case "graph.warning.kind":
			kind = kv.Value.String()
		case "node.id":
			node = kv.Value.String()
		case "graph.warning.message":
			message = kv.Value.String()
		}
		return true
	})
	key := graph + "\x00" + kind + "\x00" + node + "\x00" + message
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.seen[key]; ok {
		f.suppressed++
		return true
	}
	f.seen[key] = struct{}{}
	return false
}

// Shutdown reports the suppressed count as one line, then shuts the inner
// processor down: the suppression is visible in the log rather than
// implied by silence.
func (f *graphWarningFilter) Shutdown(ctx context.Context) error {
	f.mu.Lock()
	suppressed := f.suppressed
	f.suppressed = 0
	f.mu.Unlock()
	if suppressed > 0 {
		var record sdklog.Record
		record.SetTimestamp(time.Now())
		record.SetSeverity(log.SeverityInfo)
		record.SetBody(log.StringValue(
			"graph build findings: repeated warnings suppressed"))
		record.AddAttributes(log.Int("count", suppressed))
		if err := f.inner.OnEmit(ctx, &record); err != nil {
			return err
		}
	}
	return f.inner.Shutdown(ctx)
}

func (f *graphWarningFilter) ForceFlush(ctx context.Context) error {
	return f.inner.ForceFlush(ctx)
}
