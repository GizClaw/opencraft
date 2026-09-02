package execd

import (
	"context"
	"net"
	"sync"
	"testing"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/sandbox"
)

var errStub = errdefs.NotAvailablef("stub backend")

// stubBackend records which runner a start request landed on without
// actually spawning a process.
type stubBackend struct {
	mu    sync.Mutex
	name  string
	start int
}

func (b *stubBackend) Close() error { return nil }

func (b *stubBackend) Capabilities() sandbox.Capabilities {
	return sandbox.Capabilities{}
}

func (b *stubBackend) Start(
	context.Context, sandbox.SessionSpec,
) (sandbox.Session, error) {
	b.mu.Lock()
	b.start++
	b.mu.Unlock()
	return nil, errStub
}

func (b *stubBackend) List(context.Context) ([]sandbox.SessionInfo, error) {
	return nil, nil
}

func (b *stubBackend) Terminate(context.Context, string) error { return nil }

func (b *stubBackend) starts() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.start
}

func newPipeServer(
	t *testing.T,
	confined, unconfined sandbox.Runner,
) (*Client, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	serverConn, clientConn := net.Pipe()
	srv := New(confined, serverConn, serverConn)
	srv.SetUnconfinedBackend(unconfined)
	go func() { _ = srv.Serve(ctx) }()
	client, err := Dial(ctx, clientConn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		cancel()
	})
	return client, cancel
}

func TestServeUnconfinedSelectsBackend(t *testing.T) {
	confined := &stubBackend{name: "confined"}
	unconfined := &stubBackend{name: "unconfined"}
	client, _ := newPipeServer(t, confined, unconfined)
	ctx := context.Background()

	_, _ = client.Start(ctx, ExecParams{
		ProcessID: "p1", Argv: []string{"x"},
	})
	_, _ = client.Start(ctx, ExecParams{
		ProcessID: "p2", Argv: []string{"x"}, Unconfined: true,
	})
	if got := confined.starts(); got != 1 {
		t.Errorf("confined starts = %d, want 1", got)
	}
	if got := unconfined.starts(); got != 1 {
		t.Errorf("unconfined starts = %d, want 1", got)
	}
}

func TestRemoteRunnerSendsUnconfinedFromMode(t *testing.T) {
	confined := &stubBackend{name: "confined"}
	unconfined := &stubBackend{name: "unconfined"}
	client, _ := newPipeServer(t, confined, unconfined)
	ctx := context.Background()

	rr, err := NewRemoteRunner(ctx, client, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = rr.Start(ctx, sandbox.SessionSpec{ID: "a", Argv: []string{"x"}})
	rr.SetModeFunc(func(context.Context) bool { return true })
	_, _ = rr.Start(ctx, sandbox.SessionSpec{ID: "b", Argv: []string{"x"}})
	if confined.starts() != 1 || unconfined.starts() != 1 {
		t.Errorf("starts = %d confined / %d unconfined, want 1/1",
			confined.starts(), unconfined.starts())
	}
}
