package execd

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/testing/logcapture"
)

// newPipeClient returns a client reading one end of an in-memory
// connection; the test drives the other end.
func newPipeClient(t *testing.T) (*Client, net.Conn) {
	t.Helper()
	local, peer := net.Pipe()
	client := &Client{
		conn:     local,
		pending:  map[int64]chan Response{},
		handlers: map[string]func(json.RawMessage){},
		done:     make(chan struct{}),
		readDone: make(chan struct{}),
	}
	go client.readLoop()
	t.Cleanup(func() {
		_ = peer.Close()
		_ = client.Close()
	})
	return client, peer
}

// TestClientCloseLogsNoFailure pins the shutdown every sandbox teardown
// performs: closing the client ends the read loop with net.ErrClosed,
// which is not a failure and must not be reported as one.
func TestClientCloseLogsNoFailure(t *testing.T) {
	capture := logcapture.Install(t)
	client, _ := newPipeClient(t)

	if err := client.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.WaitReadLoop(ctx); err != nil {
		t.Fatalf("read loop did not end: %v", err)
	}
	if bodies := capture.Bodies(); len(bodies) != 0 {
		t.Fatalf("closing the client logged %v, want nothing", bodies)
	}
}

// TestClientDecodeFailureStillWarns is the positive control for the
// guard above: a peer that sends something unreadable is still a
// failure worth a record.
func TestClientDecodeFailureStillWarns(t *testing.T) {
	capture := logcapture.Install(t)
	client, peer := newPipeClient(t)
	if _, err := peer.Write([]byte("not json\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.WaitReadLoop(ctx); err != nil {
		t.Fatalf("read loop did not end: %v", err)
	}
	for _, body := range capture.Bodies() {
		if body == "execd: decode response failed; closing client" {
			return
		}
	}
	t.Fatalf("decode failure logged %v, want the decode warning",
		capture.Bodies())
}

// TestSignalledExitMatchesKilledChild pins the stop-path predicate
// against a real killed process, so a child we terminated is not
// reported as a failed wait.
func TestSignalledExitMatchesKilledChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("killing a process reports an exit code on windows")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=TestSignalledExitHelper")
	cmd.Env = append(os.Environ(), "OPENCRAFT_EXECD_SIGNAL_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill helper: %v", err)
	}
	waitErr := cmd.Wait()
	if !signalledExit(waitErr) {
		t.Fatalf("signalledExit(%v) = false, want true", waitErr)
	}
	if signalledExit(nil) {
		t.Fatal("signalledExit(nil) = true, want false")
	}
}

// TestSignalledExitHelper is re-exec'd by the test above and waits to be
// killed. It is a no-op without the marker.
func TestSignalledExitHelper(t *testing.T) {
	if os.Getenv("OPENCRAFT_EXECD_SIGNAL_HELPER") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
}
