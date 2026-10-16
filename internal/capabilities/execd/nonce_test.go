package execd

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// The launch nonce is what makes the parent's channel a conversation
// with the child it forked. On Unix the socketpair end is inherited and
// nobody else can be on the other end; on Windows the named pipe is
// dialable by any process its ACL admits, and the pipe exists before
// the child starts, so a peer that connects first is a real
// possibility. The handshake therefore carries a value only the forked
// child was told.
func TestDialVerifiesTheLaunchNonce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.Run("the child's own echo is accepted", func(t *testing.T) {
		client := dialServer(t, ctx, "expected-nonce",
			VerifyNonce("expected-nonce"))
		defer client.Close()
		if got := client.HelloInfo().GetNonce(); got != "expected-nonce" {
			t.Fatalf("HelloInfo nonce = %q, want the launch nonce", got)
		}
	})

	t.Run("a wrong echo is refused and the channel is closed", func(t *testing.T) {
		conn, server := newNoncePeers(t, ctx, "expected-nonce")
		defer conn.Close()
		defer server.Close()
		_, err := Dial(ctx, conn, VerifyNonce("someone-elses-nonce"))
		if !errors.Is(err, ErrPeerUnverified) {
			t.Fatalf("Dial error = %v, want ErrPeerUnverified", err)
		}
		if !strings.Contains(err.Error(), "expected-nonce") {
			t.Fatalf("error %q does not say what the peer echoed", err)
		}
		if !peerGone(server) {
			t.Fatal("the refused connection stayed open")
		}
	})

	t.Run("no expectation accepts an empty echo", func(t *testing.T) {
		// A child started outside LaunchExe has nothing to prove; the
		// in-process tests and embedded hosts dial it this way.
		client := dialServer(t, ctx, "")
		defer client.Close()
	})

	t.Run("no expectation also accepts a nonce it did not ask for", func(t *testing.T) {
		// Verification is the caller's choice: a dialer that does not
		// expect a nonce is trusting the channel's construction (the
		// Unix socketpair), which is the pre-existing behaviour.
		client := dialServer(t, ctx, "unrequested")
		defer client.Close()
	})
}

// peerGone reports whether the far end already closed. net.Pipe has no
// half-close, so a write from this side fails once the other side is
// gone; that write is the observation.
func peerGone(conn net.Conn) bool {
	_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
	_, err := conn.Write([]byte{0})
	return err != nil
}

// newNoncePeers starts a Server with the given launch nonce over one
// end of a fresh in-memory channel and returns both ends.
func newNoncePeers(
	t *testing.T, ctx context.Context, nonce string,
) (net.Conn, net.Conn) {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	server := New(
		func(context.Context, string, *SandboxPolicy) (RunnerSet, error) {
			return RunnerSet{}, nil
		},
		serverConn, serverConn, WithLaunchNonce(nonce),
	)
	go func() { _ = server.Serve(ctx) }()
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	})
	return clientConn, serverConn
}

// dialServer opens one verified client against a fresh server.
func dialServer(
	t *testing.T, ctx context.Context, nonce string, opts ...DialOption,
) *Client {
	t.Helper()
	clientConn, _ := newNoncePeers(t, ctx, nonce)
	client, err := Dial(ctx, clientConn, opts...)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	return client
}
