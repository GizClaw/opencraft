package execd

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/sandbox"
	"github.com/GizClaw/flowcraft/core/sandbox/local"
)

func testPair(t *testing.T) (*Client, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	root := t.TempDir()
	backend := local.New(root)

	serverConn, clientConn := net.Pipe()
	go func() {
		_ = New(backend, serverConn, serverConn).Serve(ctx)
	}()

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

func TestServeEnvironment(t *testing.T) {
	client, _ := testPair(t)
	ctx := context.Background()

	info, err := client.EnvironmentInfo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Shell != "/bin/sh" || !strings.Contains(strings.Join(info.Capabilities, ","), "signal") {
		t.Errorf("info = %+v", info)
	}
}

func TestServeStartRead(t *testing.T) {
	client, _ := testPair(t)
	ctx := context.Background()

	resp, err := client.Start(ctx, ExecParams{
		ProcessID: "p1",
		Argv:      []string{"/bin/sh", "-c", "echo hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ProcessID != "p1" {
		t.Errorf("processId = %q", resp.ProcessID)
	}

	var all []byte
	after := int64(0)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("timed out; output so far %q", all)
		}
		read, err := client.Read(ctx, ReadParams{
			ProcessID: "p1",
			AfterSeq:  &after,
			MaxBytes:  intPtr(4096),
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, ch := range read.Chunks {
			all = append(all, ch.Data...)
		}
		after = read.NextSeq
		if read.EOF {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(string(all), "hello") {
		t.Errorf("output = %q", all)
	}
}

func TestServeStartReadStderr(t *testing.T) {
	client, _ := testPair(t)
	ctx := context.Background()

	if _, err := client.Start(ctx, ExecParams{
		ProcessID: "p2",
		Argv:      []string{"/bin/sh", "-c", "echo err 1>&2"},
	}); err != nil {
		t.Fatal(err)
	}

	var all []byte
	after := int64(0)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("timed out; output so far %q", all)
		}
		read, err := client.Read(ctx, ReadParams{
			ProcessID: "p2",
			AfterSeq:  &after,
			MaxBytes:  intPtr(4096),
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, ch := range read.Chunks {
			if ch.Stream != StreamStderr {
				t.Errorf("stream = %q, want stderr", ch.Stream)
			}
			all = append(all, ch.Data...)
		}
		after = read.NextSeq
		if read.EOF {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(string(all), "err") {
		t.Errorf("output = %q", all)
	}
}

func TestServeTerminate(t *testing.T) {
	client, _ := testPair(t)
	ctx := context.Background()

	if _, err := client.Start(ctx, ExecParams{
		ProcessID: "p3",
		Argv:      []string{"/bin/sh", "-c", "sleep 100"},
	}); err != nil {
		t.Fatal(err)
	}
	resp, err := client.Terminate(ctx, TerminateParams{ProcessID: "p3"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Running {
		t.Error("process still running after terminate")
	}
}

func TestServeWriteIdempotent(t *testing.T) {
	client, _ := testPair(t)
	ctx := context.Background()

	if _, err := client.Start(ctx, ExecParams{
		ProcessID: "p4",
		Argv:      []string{"/bin/cat"},
		PipeStdin: true,
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	first, err := client.Write(ctx, WriteParams{
		ProcessID: "p4", Chunk: []byte("x"), WriteID: "w1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != WriteAccepted {
		t.Errorf("first write = %q", first.Status)
	}
	second, err := client.Write(ctx, WriteParams{
		ProcessID: "p4", Chunk: []byte("x"), WriteID: "w1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != WriteAccepted {
		t.Errorf("duplicate write = %q", second.Status)
	}
}

func TestServeResize(t *testing.T) {
	client, _ := testPair(t)
	ctx := context.Background()

	if _, err := client.Start(ctx, ExecParams{
		ProcessID: "rz",
		Argv:      []string{"/bin/sh"},
		TTY:       true,
		Rows:      24,
		Cols:      80,
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := client.Resize(ctx, ResizeParams{
		ProcessID: "rz",
		Rows:      40,
		Cols:      120,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestServeEventNotifications(t *testing.T) {
	client, _ := testPair(t)
	ctx := context.Background()

	var mu sync.Mutex
	var outputs []string
	exited := make(chan struct{}, 1)
	closed := make(chan struct{}, 1)
	client.OnNotification(MethodProcessOutput, func(raw json.RawMessage) {
		var n OutputNotification
		if err := json.Unmarshal(raw, &n); err != nil {
			return
		}
		mu.Lock()
		outputs = append(outputs, string(n.Data))
		mu.Unlock()
	})
	client.OnNotification(MethodProcessExited, func(raw json.RawMessage) {
		exited <- struct{}{}
	})
	client.OnNotification(MethodProcessClosed, func(raw json.RawMessage) {
		closed <- struct{}{}
	})

	if _, err := client.Start(ctx, ExecParams{
		ProcessID: "evt",
		Argv:      []string{"/bin/sh", "-c", "echo event-ok"},
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for process/exited notification")
	}
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for process/closed notification")
	}

	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(outputs, "")
	if !strings.Contains(joined, "event-ok") {
		t.Errorf("output notifications = %q", joined)
	}
}

// TestServeReadExitedFromWatcherEvent verifies that Exited is driven
// by the watcher's exit event, not by output EOF. With a stub session
// whose Read reports EOF while the process is still alive, Read must
// stay Exited=false until the exit event arrives — and then carry the
// real exit code (never Exited=true with a nil code).
func TestServeReadExitedFromWatcherEvent(t *testing.T) {
	backend := &stubRunner{sess: &stubSession{}}
	ctx, cancel := context.WithCancel(context.Background())
	serverConn, clientConn := net.Pipe()
	go func() {
		_ = New(backend, serverConn, serverConn).Serve(ctx)
	}()
	client, err := Dial(ctx, clientConn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		cancel()
	})

	if _, err := client.Start(ctx, ExecParams{
		ProcessID: "stub",
		Argv:      []string{"/bin/true"},
	}); err != nil {
		t.Fatal(err)
	}

	// Output EOF alone must not report Exited.
	after := int64(0)
	read, err := client.Read(ctx, ReadParams{
		ProcessID: "stub",
		AfterSeq:  &after,
		MaxBytes:  intPtr(4096),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !read.EOF {
		t.Fatalf("EOF = false, want true")
	}
	if read.Exited {
		t.Fatalf("Exited = true before the watcher delivered the exit event")
	}

	// Deliver the exit event; the next Read must report the real code.
	backend.sess.watcher.emit(sandbox.SessionEvent{
		Seq:  1,
		Type: sandbox.SessionEventExited,
		Exit: &sandbox.SessionExit{Code: 7, Reason: sandbox.SessionExited},
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for Exited")
		}
		read, err = client.Read(ctx, ReadParams{
			ProcessID: "stub",
			AfterSeq:  &after,
			MaxBytes:  intPtr(4096),
		})
		if err != nil {
			t.Fatal(err)
		}
		after = read.NextSeq
		if read.Exited {
			if read.ExitCode == nil || *read.ExitCode != 7 {
				t.Fatalf("ExitCode = %v, want 7", read.ExitCode)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestServeReadExitCode verifies the real exit code of a short-lived
// process survives the race between output EOF and the watcher's exit
// event: Exited is never reported without an attached ExitCode.
func TestServeReadExitCode(t *testing.T) {
	client, _ := testPair(t)
	ctx := context.Background()

	if _, err := client.Start(ctx, ExecParams{
		ProcessID: "p-code",
		Argv:      []string{"/bin/sh", "-c", "exit 7"},
	}); err != nil {
		t.Fatal(err)
	}

	after := int64(0)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for exited")
		}
		read, err := client.Read(ctx, ReadParams{
			ProcessID: "p-code",
			AfterSeq:  &after,
			MaxBytes:  intPtr(4096),
		})
		if err != nil {
			t.Fatal(err)
		}
		after = read.NextSeq
		if read.Exited {
			if read.ExitCode == nil || *read.ExitCode != 7 {
				t.Fatalf("ExitCode = %v, want 7", read.ExitCode)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// stubSession is a controllable sandbox.Session for exercising server
// semantics without real processes: Read reports EOF immediately while
// the process stays alive until the test emits the exit event.
type stubSession struct {
	watcher *stubWatcher
}

func (s *stubSession) ID() string { return "stub" }
func (s *stubSession) PID() int   { return 42 }
func (s *stubSession) Capabilities() sandbox.SessionCapabilities {
	return sandbox.SessionCapabilities{Signal: true, Events: true}
}
func (s *stubSession) Read(context.Context, int64, int) (sandbox.SessionOutput, error) {
	return sandbox.SessionOutput{NextSeq: 0, EOF: true}, nil
}
func (s *stubSession) Write(context.Context, []byte) error                 { return nil }
func (s *stubSession) CloseInput() error                                   { return nil }
func (s *stubSession) Resize(context.Context, int, int) error              { return nil }
func (s *stubSession) Signal(context.Context, sandbox.SessionSignal) error { return nil }
func (s *stubSession) Terminate(context.Context) error                     { return nil }
func (s *stubSession) Wait(context.Context) (sandbox.SessionExit, error) {
	return sandbox.SessionExit{}, nil
}
func (s *stubSession) Watch(context.Context) (sandbox.SessionWatcher, error) {
	s.watcher = &stubWatcher{events: make(chan sandbox.SessionEvent, 8)}
	return s.watcher, nil
}
func (s *stubSession) Close() error { return nil }

type stubWatcher struct {
	events chan sandbox.SessionEvent
}

func (w *stubWatcher) Events() <-chan sandbox.SessionEvent { return w.events }
func (w *stubWatcher) Close() error                        { return nil }
func (w *stubWatcher) emit(ev sandbox.SessionEvent)        { w.events <- ev }

type stubRunner struct {
	sess *stubSession
}

func (r *stubRunner) Close() error { return nil }
func (r *stubRunner) Capabilities() sandbox.Capabilities {
	return sandbox.Capabilities{
		Features: sandbox.SessionFeatures{Signal: true, Events: true},
	}
}
func (r *stubRunner) Start(context.Context, sandbox.SessionSpec) (sandbox.Session, error) {
	return r.sess, nil
}
func (r *stubRunner) List(context.Context) ([]sandbox.SessionInfo, error) { return nil, nil }
func (r *stubRunner) Terminate(context.Context, string) error             { return nil }

func intPtr(v int) *int { return &v }
