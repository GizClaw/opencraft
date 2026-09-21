package telemetry

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// recordingProcessor is a terminal processor that keeps everything it is
// handed, so a test can see what survived the filter.
type recordingProcessor struct {
	mu      sync.Mutex
	records []sdklog.Record
	flushes int
	downs   int
}

func (r *recordingProcessor) Enabled(
	context.Context,
	sdklog.EnabledParameters,
) bool {
	return true
}

func (r *recordingProcessor) OnEmit(_ context.Context, record *sdklog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, record.Clone())
	return nil
}

func (r *recordingProcessor) Shutdown(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.downs++
	return nil
}

func (r *recordingProcessor) ForceFlush(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.flushes++
	return nil
}

func (r *recordingProcessor) bodies() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.records))
	for _, record := range r.records {
		out = append(out, record.Body().AsString())
	}
	return out
}

func (r *recordingProcessor) countBody(body string) int {
	count := 0
	for _, got := range r.bodies() {
		if got == body {
			count++
		}
	}
	return count
}

// warningEmitter emits findings the way flowcraft's analyzer does: through
// a logger, not by building sdklog.Record values by hand.
//
// The distinction matters. A zero-value Record carries attribute limits of
// zero, and AddAttributes truncates every string attribute on such a record
// to the empty string, so a hand-built record reaches the filter with four
// empty values: every finding keys the same and the filter looks like it
// suppresses everything after the first line. Records that come out of a
// provider carry the provider's limits (unset means no limit) and read back
// intact, which is also what the process itself sees.
type warningEmitter struct {
	filter *graphWarningFilter
	logger log.Logger
}

func newWarningEmitter(inner sdklog.Processor) *warningEmitter {
	filter := newGraphWarningFilter(inner)
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(filter))
	return &warningEmitter{
		filter: filter,
		logger: provider.Logger("graphwarning-test"),
	}
}

// warn emits one build finding of the assistant graph.
func (e *warningEmitter) warn(t *testing.T, node, message string) {
	t.Helper()
	var record log.Record
	record.SetTimestamp(time.Now())
	record.SetSeverity(log.SeverityWarn)
	record.SetBody(log.StringValue(graphWarningBody))
	record.AddAttributes(
		log.String("graph.name", "opencraft-assistant"),
		log.String("graph.warning.kind", "unresolved_reference"),
		log.String("node.id", node),
		log.String("graph.warning.message", message),
	)
	e.logger.Emit(context.Background(), record)
}

// info emits an unrelated record.
func (e *warningEmitter) info(t *testing.T, body string) {
	t.Helper()
	var record log.Record
	record.SetTimestamp(time.Now())
	record.SetSeverity(log.SeverityInfo)
	record.SetBody(log.StringValue(body))
	e.logger.Emit(context.Background(), record)
}

// TestGraphWarningFilterKeepsFirstFinding pins the noise fix: an assembly
// storm re-reports the same static findings, and the file log should carry
// each one once (the line that says the analyzer ran) plus a count of what
// it dropped, instead of hundreds of identical WARN lines.
func TestGraphWarningFilterKeepsFirstFinding(t *testing.T) {
	inner := &recordingProcessor{}
	emitter := newWarningEmitter(inner)
	ctx := context.Background()

	const assemblies = 16
	findings := []struct{ node, message string }{
		{"llm", "config references ${board:model} but no node declares a write for it"},
		{"llm", "config references ${board:think_level} but no node declares a write for it"},
	}
	for i := 0; i < assemblies; i++ {
		for _, finding := range findings {
			emitter.warn(t, finding.node, finding.message)
		}
	}
	if got := inner.countBody(graphWarningBody); got != 2 {
		t.Fatalf("kept %d findings, want one per distinct finding", got)
	}
	// A finding that is genuinely new still logs, including a new node in
	// a graph whose other findings were already seen.
	emitter.warn(t, "other", "new finding")
	if got := inner.countBody(graphWarningBody); got != 3 {
		t.Fatalf("kept %d findings, want a new finding to be logged", got)
	}
	// Records that are not build findings are untouched.
	emitter.info(t, "host: runtime assembled")
	if got := inner.countBody("host: runtime assembled"); got != 1 {
		t.Fatalf("plain records kept = %d, want 1", got)
	}

	if err := emitter.filter.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	const summary = "graph build findings: repeated warnings suppressed"
	if got := inner.countBody(summary); got != 1 {
		t.Fatalf("summary records = %d, want 1", got)
	}
	inner.mu.Lock()
	defer inner.mu.Unlock()
	if inner.downs != 1 {
		t.Fatalf("inner shutdown calls = %d, want 1", inner.downs)
	}
	var found bool
	for _, record := range inner.records {
		if record.Body().AsString() != summary {
			continue
		}
		found = true
		// The count is an int64 attribute: strings set on a record built
		// outside a provider would be truncated to nothing.
		record.WalkAttributes(func(kv log.KeyValue) bool {
			if kv.Key == "count" && kv.Value.AsInt64() != 2*(assemblies-1) {
				t.Fatalf("suppressed count = %d, want %d",
					kv.Value.AsInt64(), 2*(assemblies-1))
			}
			return true
		})
	}
	if !found {
		t.Fatal("no suppression summary was recorded")
	}
}

// TestGraphWarningFilterSilentWithoutRepeats pins the counterpart: a run
// whose findings each appear once gains no summary line.
func TestGraphWarningFilterSilentWithoutRepeats(t *testing.T) {
	inner := &recordingProcessor{}
	emitter := newWarningEmitter(inner)
	ctx := context.Background()
	emitter.warn(t, "llm", "config references ${board:model}")
	emitter.warn(t, "llm", "config references ${board:think_level}")
	if err := emitter.filter.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if got := inner.countBody(
		"graph build findings: repeated warnings suppressed"); got != 0 {
		t.Fatalf("summary records = %d, want none", got)
	}
	if got := inner.countBody(graphWarningBody); got != 2 {
		t.Fatalf("kept %d findings, want both distinct findings", got)
	}
}
