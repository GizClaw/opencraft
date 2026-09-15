package execd

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/testing/logcapture"
)

// TestForwardChildStderrLogsLines pins the forwarding rules: one record
// per line, tagged with the child it came from.
func TestForwardChildStderrLogsLines(t *testing.T) {
	capture := logcapture.Install(t)
	forwardChildStderr(context.Background(), 4242, "/tmp/execd-test.sock",
		strings.NewReader("first line\nsecond line\n"))

	records := capture.Records()
	if len(records) != 2 {
		t.Fatalf("forwarded %d records, want 2", len(records))
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
