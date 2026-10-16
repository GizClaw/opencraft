package headless

import (
	"encoding/json"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/capabilities/rollout"
	"github.com/GizClaw/opencraft/internal/testing/logcapture"
)

// waitForBody polls the captured telemetry for one log body.
func waitForBody(
	recorder *logcapture.Recorder, body string, timeout time.Duration,
) bool {
	deadline := time.Now().Add(timeout)
	for {
		for _, record := range recorder.Records() {
			if record.Body().AsString() == body {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestStreamRecorderGoesQuietAfterBrokenPipe pins the consumer-gone
// path: the first event that cannot be written reports once, arms the
// cancel callback exactly once, and every later event is dropped
// instead of warning per line — the stream had one reader, and it left.
func TestStreamRecorderGoesQuietAfterBrokenPipe(t *testing.T) {
	recorder := logcapture.Install(t)
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pw.Close() }()
	if err := pr.Close(); err != nil {
		t.Fatal(err)
	}

	rec := &streamRecorder{enc: json.NewEncoder(pw)}
	var cancelled atomic.Int32
	rec.armOutputGone(func() { cancelled.Add(1) })

	const body = "headless: output consumer went away; cancelling the run"
	for i := 0; i < 3; i++ {
		rec.emit(rollout.Event{Type: rollout.TypeTurnStarted})
	}
	if got := cancelled.Load(); got != 1 {
		t.Fatalf("output-gone callback fired %d times, want 1", got)
	}
	if !waitForBody(recorder, body, 5*time.Second) {
		t.Fatalf("the broken pipe was not reported (bodies: %v)",
			recorder.Bodies())
	}
	var reports int
	for _, record := range recorder.Records() {
		if record.Body().AsString() == body {
			reports++
		}
	}
	if reports != 1 {
		t.Fatalf("broken-pipe reports = %d, want 1", reports)
	}
}
