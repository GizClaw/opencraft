package execd

import (
	"errors"
	"io"
	"net"
	"strings"
	"syscall"
	"testing"

	"github.com/GizClaw/flowcraft/core/sandbox/local"

	"github.com/GizClaw/opencraft/internal/testing/logcapture"
)

// failingWriter fails every write with a non-connection error.
type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func id(n int64) *int64 { return &n }

// TestServerWriteWarnsOnlyForRealFailures pins the rule a session
// watcher's notification follows: a client that already hung up is
// teardown, while a real transport failure still warns. The background
// context those writes use means a spurious record lands in whatever
// log pipeline is installed by then — in tests, the next test's
// capture, which is how a closed client failed someone else's
// assertion.
func TestServerWriteWarnsOnlyForRealFailures(t *testing.T) {
	t.Run("peer gone is silent", func(t *testing.T) {
		capture := logcapture.Install(t)
		serverConn, clientConn := net.Pipe()
		srv := New(local.New(t.TempDir()), serverConn, serverConn)
		t.Cleanup(func() { _ = serverConn.Close() })
		if err := clientConn.Close(); err != nil {
			t.Fatal(err)
		}

		srv.notify(MethodProcessExited, ExitedNotification{ProcessID: "p1"})
		srv.respond(Response{JSONRPC: "2.0", ID: id(1)})

		for _, record := range capture.Records() {
			if body := record.Body().AsString(); strings.Contains(
				body, "encode JSON-RPC",
			) {
				t.Fatalf("closed peer logged %q", body)
			}
		}
	})

	t.Run("real failure still warns", func(t *testing.T) {
		capture := logcapture.Install(t)
		srv := New(
			local.New(t.TempDir()),
			strings.NewReader(""),
			failingWriter{err: errors.New("disk full")},
		)

		srv.notify(MethodProcessExited, ExitedNotification{ProcessID: "p1"})

		for _, record := range capture.Records() {
			if strings.Contains(
				record.Body().AsString(),
				"encode JSON-RPC notification failed",
			) {
				return
			}
		}
		t.Fatal("a real encode failure must still be logged")
	})
}

// TestConnectionClosedClassifiesPeerHangup pins the error vocabulary
// the client read loop and the server write path both branch on.
func TestConnectionClosedClassifiesPeerHangup(t *testing.T) {
	for _, err := range []error{
		net.ErrClosed,
		io.EOF,
		io.ErrClosedPipe,
		syscall.EPIPE,
	} {
		if !connectionClosed(err) {
			t.Fatalf("connectionClosed(%v) = false, want true", err)
		}
	}
	if connectionClosed(errors.New("protocol error")) {
		t.Fatal("a protocol failure must not count as a closed connection")
	}
}
