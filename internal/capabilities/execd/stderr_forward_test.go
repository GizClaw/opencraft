package execd

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	coretelemetry "github.com/GizClaw/flowcraft/core/telemetry"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/GizClaw/opencraft/internal/testing/logcapture"
)

// TestForwardChildStderrLogsLines pins the forwarding rules: one record
// per line, tagged with the child it came from.
func TestForwardChildStderrLogsLines(t *testing.T) {
	capture := logcapture.Install(t)
	forwardChildStderr(context.Background(), 4242, "/tmp/execd-test.sock",
		strings.NewReader("first line\nsecond line\n"))

	// The capture is process wide, so another component's record can
	// land in this window (a session watcher notifying a client that
	// already hung up, say). Only the forwarder's records carry the
	// child's pid attribute, and those are what this test owns.
	var records []sdklog.Record
	for _, record := range capture.Records() {
		if logcapture.Attribute(record, "execd.pid") != "" {
			records = append(records, record)
		}
	}
	if len(records) != 2 {
		var leak strings.Builder
		for i, record := range records {
			fmt.Fprintf(&leak, "\n  [%d] body=%q line=%q socket=%q pid=%q",
				i,
				record.Body().AsString(),
				logcapture.Attribute(record, "execd.line"),
				logcapture.Attribute(record, "execd.socket"),
				logcapture.Attribute(record, "execd.pid"),
			)
		}
		var ignored strings.Builder
		for _, record := range capture.Records() {
			if logcapture.Attribute(record, "execd.pid") == "" {
				fmt.Fprintf(&ignored, "\n  body=%q", record.Body().AsString())
			}
		}
		t.Fatalf("forwarded %d records, want 2:%s\n  (ignored, not forwarded: %s)",
			len(records), leak.String(), ignored.String())
	}
	for i, want := range []string{"first line", "second line"} {
		record := records[i]
		if body := record.Body().AsString(); body != "execd stderr" {
			t.Fatalf("record %d body = %q, want %q", i, body, "execd stderr")
		}
		if got := logcapture.Attribute(record, "execd.line"); got != want {
			t.Fatalf("record %d execd.line = %q, want %q", i, got, want)
		}
		if got := logcapture.Attribute(record, "execd.socket"); got != "/tmp/execd-test.sock" {
			t.Fatalf("record %d execd.socket = %q", i, got)
		}
		if got := logcapture.Attribute(record, "execd.pid"); got != "4242" {
			t.Fatalf("record %d execd.pid = %q, want 4242", i, got)
		}
	}
}

// TestChildStderrReachesHostLog is the end-to-end half: a warning the
// child logs reaches the application log tagged with that child's
// socket, even though the child has no log pipeline of the desktop's
// own and never touches the app's stderr.
func TestChildStderrReachesHostLog(t *testing.T) {
	capture := logcapture.Install(t)
	bin := buildOpencraft(t)
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	client, sock, stop, err := LaunchExe(context.Background(), root, bin, "")
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer stop()
	_ = client

	// A second connection that speaks nonsense fails the child's serve
	// loop, and the child logs that at warning level — the line this test
	// waits for.
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if _, err := conn.Write([]byte("this is not json\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.Close()

	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, record := range capture.Records() {
			if record.Body().AsString() != "execd stderr" {
				continue
			}
			line := logcapture.Attribute(record, "execd.line")
			if !strings.Contains(line, "serve connection failed") {
				continue
			}
			if got := logcapture.Attribute(record, "execd.socket"); got != sock {
				t.Fatalf("execd.socket = %q, want %q", got, sock)
			}
			if pid := logcapture.Attribute(record, "execd.pid"); pid == "" || pid == "0" {
				t.Fatalf("execd.pid = %q, want the child pid", pid)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("child warning never reached the host log: %v",
				capture.Bodies())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestStopDrainsForwardedStderr pins the teardown ordering: stop returns
// only once everything the child wrote to stderr has been logged, so the
// warnings a child writes while it shuts down cannot land in the log
// window that runs next.
//
// The recorder keeps every forwarded line in flight for a while, and the
// test only stops the child after the first line arrived: whatever else
// the child wrote is demonstrably still unlogged, so a stop that does not
// drain is caught here instead of winning a race.
func TestStopDrainsForwardedStderr(t *testing.T) {
	recorder := &slowRecorder{delay: 100 * time.Millisecond}
	stopLog, err := coretelemetry.InitLog(context.Background(),
		coretelemetry.WithLogProcessor(recorder))
	if err != nil {
		t.Fatalf("install slow log capture: %v", err)
	}
	t.Cleanup(func() {
		if err := stopLog(context.Background()); err != nil {
			t.Errorf("shutdown log capture: %v", err)
		}
	})

	bin := buildOpencraft(t)
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	client, sock, stop, err := LaunchExe(context.Background(), root, bin, "")
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	_ = client

	// Every connection that speaks nonsense fails the child's serve loop
	// for that connection and is written to its stderr.
	for i := 0; i < 6; i++ {
		conn, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		if _, err := conn.Write([]byte("this is not json\n")); err != nil {
			t.Fatalf("write: %v", err)
		}
		_ = conn.Close()
	}

	deadline := time.Now().Add(10 * time.Second)
	for recorder.forwarded(sock) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("child never logged to stderr")
		}
		time.Sleep(10 * time.Millisecond)
	}

	stop()

	drained := recorder.forwarded(sock)
	time.Sleep(500 * time.Millisecond)
	if after := recorder.forwarded(sock); after != drained {
		t.Fatalf("stop returned before the child's stderr was drained: "+
			"%d forwarded records became %d", drained, after)
	}
	// The drain ends at EOF (the child's exit), not at the grace period:
	// a stop that has to force the reader closed means the parent kept
	// the pipe's write end open, which drops whatever the child wrote
	// after the grace period and can still emit a line after stop
	// returned — the cross-test leak TestForwardChildStderrLogsLines
	// catches as "3 records, want 2".
	for _, record := range recorder.snapshot() {
		if body := record.Body().AsString(); strings.Contains(
			body, "still draining during stop",
		) {
			t.Fatalf("stop fell back to the drain grace period: %q", body)
		}
	}
}

// slowRecorder is a log processor that holds each record back for a
// while, so a stop that returns mid-drain is observed instead of racing
// the forwarder to completion.
type slowRecorder struct {
	delay time.Duration

	mu      sync.Mutex
	records []sdklog.Record
}

func (r *slowRecorder) Enabled(context.Context, sdklog.EnabledParameters) bool {
	return true
}

func (r *slowRecorder) OnEmit(_ context.Context, record *sdklog.Record) error {
	time.Sleep(r.delay)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, record.Clone())
	return nil
}

func (r *slowRecorder) Shutdown(context.Context) error   { return nil }
func (r *slowRecorder) ForceFlush(context.Context) error { return nil }

// forwarded counts the records that came from one child.
func (r *slowRecorder) forwarded(sock string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, record := range r.records {
		if logcapture.Attribute(record, "execd.socket") == sock {
			count++
		}
	}
	return count
}

// snapshot returns a copy of every record the processor has seen.
func (r *slowRecorder) snapshot() []sdklog.Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]sdklog.Record(nil), r.records...)
}
