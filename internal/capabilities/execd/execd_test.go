package execd

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/sandbox"
	"github.com/GizClaw/flowcraft/core/sandbox/local"
)

// localFactory builds in-process runners for one Bind.
func localFactory(t *testing.T) RunnerFactory {
	t.Helper()
	return func(
		_ context.Context,
		workdir string,
		policy *SandboxPolicy,
	) (RunnerSet, error) {
		if workdir == "" {
			workdir = t.TempDir()
		}
		env := sandbox.EnvPolicy{}
		if policy.GetEnvAllowSet() {
			env.Allow = policy.GetEnvAllow()
			if env.Allow == nil {
				env.Allow = []string{}
			}
		}
		if inject := policy.GetEnvInject(); len(inject) > 0 {
			env.Inject = inject
		}
		return RunnerSet{
			Confined:   local.New(workdir),
			Unconfined: local.New(workdir),
			DefaultEnv: env,
		}, nil
	}
}

func testPairConfigured(
	t *testing.T,
	configure func(*Server),
) (*Client, *Server) {
	t.Helper()
	return testPairFull(t, localFactory(t), configure)
}

// testPairWithFactory builds a pair over an explicit runner factory, for
// tests that need a backend with different capabilities.
func testPairWithFactory(
	t *testing.T,
	factory RunnerFactory,
) (*Client, *Server) {
	t.Helper()
	return testPairFull(t, factory, nil)
}

func testPairFull(
	t *testing.T,
	factory RunnerFactory,
	configure func(*Server),
) (*Client, *Server) {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	srv := New(factory, serverConn, serverConn)
	if configure != nil {
		configure(srv)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Serve(ctx) }()
	client, err := Dial(ctx, clientConn)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		cancel()
	})
	return client, srv
}

func testPair(t *testing.T) (*Client, *Server) {
	t.Helper()
	return testPairConfigured(t, nil)
}

func testRunner(t *testing.T) *RemoteRunner {
	t.Helper()
	client, _ := testPair(t)
	runner, err := NewRemoteRunner(
		context.Background(), client, nil, t.TempDir(), &SandboxPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runner.Close() })
	return runner
}

func TestHelloRejectsVersionMismatch(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	srv := New(localFactory(t), serverConn, serverConn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()

	frame := &Frame{
		Id: 1,
		Body: &Frame_Request{Request: &Request{
			Method: &Request_Hello{Hello: &Hello{
				ProtocolVersion: ProtocolVersion + 1,
				Client:          "test",
			}},
		}},
	}
	encoded, err := encodeFrame(frame)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clientConn.Write(encoded); err != nil {
		t.Fatal(err)
	}
	response, err := decodeFrame(bufio.NewReader(clientConn))
	if err != nil {
		t.Fatal(err)
	}
	if response.GetResponse().GetError().GetCode() != CodeVersionMismatch {
		t.Fatalf("response = %v, want version mismatch", response)
	}
}

func TestBindRequiredBeforeProcessCalls(t *testing.T) {
	client, _ := testPair(t)
	_, err := client.Start(context.Background(), &Start{
		ProcessId: "p", Argv: []string{"/bin/sh", "-c", "true"},
	})
	if !IsCode(err, CodeNotBound) {
		t.Fatalf("err = %v, want not_bound", err)
	}
}

func TestStartReadWaitRelease(t *testing.T) {
	requirePOSIXChild(t)
	client, _ := testPair(t)
	ctx := context.Background()
	if _, err := client.Bind(ctx, t.TempDir(), &SandboxPolicy{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Start(ctx, &Start{
		ProcessId: "echo",
		Argv:      []string{"/bin/sh", "-c", "echo hello-exec"},
	}); err != nil {
		t.Fatal(err)
	}
	var output []byte
	var after int64
	deadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("timed out; output %q", output)
		}
		read, err := client.Read(ctx, &Read{
			ProcessId: "echo", AfterSeq: after, MaxBytes: 4096,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, chunk := range read.GetChunks() {
			output = append(output, chunk.GetData()...)
		}
		after = read.GetNextSeq()
		if read.GetEof() {
			break
		}
	}
	if !strings.Contains(string(output), "hello-exec") {
		t.Fatalf("output = %q", output)
	}
	wait, err := client.Wait(ctx, "echo")
	if err != nil {
		t.Fatal(err)
	}
	if wait.GetExitCode() != 0 {
		t.Fatalf("exit = %d", wait.GetExitCode())
	}
	released, err := client.Release(ctx, "echo")
	if err != nil {
		t.Fatal(err)
	}
	if !released.GetReleased() {
		t.Fatal("release reported no entry")
	}
	if _, err := client.Read(ctx, &Read{ProcessId: "echo"}); !IsNotFound(err) {
		t.Fatalf("read after release = %v, want not_found", err)
	}
}

// TestQuietReadDoesNotBlockOtherRequests pins the regression that made
// a quiet command wedge the whole connection.
func TestQuietReadDoesNotBlockOtherRequests(t *testing.T) {
	requirePOSIXChild(t)
	client, _ := testPair(t)
	ctx := context.Background()
	if _, err := client.Bind(ctx, t.TempDir(), &SandboxPolicy{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Start(ctx, &Start{
		ProcessId: "quiet",
		Argv:      []string{"/bin/sh", "-c", "sleep 30"},
	}); err != nil {
		t.Fatal(err)
	}
	readDone := make(chan error, 1)
	go func() {
		_, err := client.Read(ctx, &Read{ProcessId: "quiet", WaitMs: 500})
		readDone <- err
	}()
	time.Sleep(50 * time.Millisecond)

	start := time.Now()
	if _, err := client.Start(ctx, &Start{
		ProcessId: "other",
		Argv:      []string{"/bin/sh", "-c", "echo other-ok"},
	}); err != nil {
		t.Fatalf("start behind quiet read: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("start waited %v behind a quiet read", elapsed)
	}
	start = time.Now()
	if _, err := client.Terminate(ctx, "quiet", true); err != nil {
		t.Fatalf("terminate behind quiet read: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("terminate waited %v behind a quiet read", elapsed)
	}
	if err := <-readDone; err != nil {
		t.Fatalf("read: %v", err)
	}
}

// TestRemoteExecCancelKillsProcessGroup is the user-visible regression:
// cancelling the tool context must return promptly and kill the group.
func TestRemoteExecCancelKillsProcessGroup(t *testing.T) {
	requirePOSIXChild(t)
	runner := testRunner(t)
	marker := "sleep 41.75"
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := sandbox.Exec(ctx, runner, "/bin/sh",
			[]string{"-c", marker}, sandbox.ExecOptions{})
		done <- err
	}()
	time.Sleep(300 * time.Millisecond)
	start := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not return within 5s")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("cancel took %v", elapsed)
	}
	deadline := time.Now().Add(3 * time.Second)
	for pgrepMarker(marker) != "" {
		if time.Now().After(deadline) {
			t.Fatalf("process survived cancellation: %s", pgrepMarker(marker))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestRemoteExecsDoNotSerialize pins the two-session report: one exec
// sleeping must not delay another exec in the same workspace.
func TestRemoteExecsDoNotSerialize(t *testing.T) {
	requirePOSIXChild(t)
	runner := testRunner(t)
	sleepCtx, cancelSleep := context.WithCancel(context.Background())
	defer cancelSleep()
	sleepDone := make(chan error, 1)
	go func() {
		_, err := sandbox.Exec(sleepCtx, runner, "/bin/sh",
			[]string{"-c", "sleep 30"}, sandbox.ExecOptions{})
		sleepDone <- err
	}()
	time.Sleep(200 * time.Millisecond)

	start := time.Now()
	result, err := sandbox.Exec(context.Background(), runner, "/bin/sh",
		[]string{"-c", "echo hi"}, sandbox.ExecOptions{})
	if err != nil {
		t.Fatalf("second exec: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("second exec waited %v behind the sleeping one", elapsed)
	}
	if !strings.Contains(result.Stdout, "hi") {
		t.Fatalf("stdout = %q", result.Stdout)
	}
	start = time.Now()
	cancelSleep()
	select {
	case <-sleepDone:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled sleep did not return")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("cancel took %v", elapsed)
	}
}

func TestRequestDeadlineIsBounded(t *testing.T) {
	requirePOSIXChild(t)
	client, _ := testPair(t)
	ctx := context.Background()
	if _, err := client.Bind(ctx, t.TempDir(), &SandboxPolicy{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Start(ctx, &Start{
		ProcessId: "quiet",
		Argv:      []string{"/bin/sh", "-c", "sleep 30"},
	}); err != nil {
		t.Fatal(err)
	}
	readCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := client.Read(readCtx, &Read{ProcessId: "quiet", WaitMs: 5000})
	if err == nil {
		t.Fatal("read with a 100ms deadline succeeded")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("deadline took %v", elapsed)
	}
	// The connection stays healthy after a canceled request.
	if _, err := client.Ping(ctx); err != nil {
		t.Fatalf("ping after deadline: %v", err)
	}
}

// TestRemoteRunnerWatchdogRelaunches pins the self-healing path: a dead
// child is detected by the ping watchdog, and the next call rebinds the
// workspace on a fresh child.
func TestRemoteRunnerWatchdogRelaunches(t *testing.T) {
	requirePOSIXChild(t)
	client1, _ := testPair(t)
	workdir := t.TempDir()
	runner, err := NewRemoteRunner(
		context.Background(), client1, func() { _ = client1.Close() },
		workdir, &SandboxPolicy{},
		withWatchdog(20*time.Millisecond, 100*time.Millisecond, 1,
			[]time.Duration{10 * time.Millisecond}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runner.Close() })

	client2, _ := testPair(t)
	runner.SetRelauncher(func() (*Client, func(), error) {
		return client2, func() { _ = client2.Close() }, nil
	})
	if err := client1.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for runner.Stats().Restarts == 0 {
		if time.Now().After(deadline) {
			t.Fatal("watchdog never relaunched the child")
		}
		time.Sleep(10 * time.Millisecond)
	}
	result, err := sandbox.Exec(context.Background(), runner, "/bin/sh",
		[]string{"-c", "echo after-restart"}, sandbox.ExecOptions{})
	if err != nil {
		t.Fatalf("exec after restart: %v", err)
	}
	if !strings.Contains(result.Stdout, "after-restart") {
		t.Fatalf("stdout = %q", result.Stdout)
	}
}

func TestReleaseThenReap(t *testing.T) {
	requirePOSIXChild(t)
	client, srv := testPairConfigured(t, func(s *Server) {
		s.reapAfter = 20 * time.Millisecond
		s.reapInterval = 10 * time.Millisecond
	})
	ctx := context.Background()
	if _, err := client.Bind(ctx, t.TempDir(), &SandboxPolicy{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Start(ctx, &Start{
		ProcessId: "forgotten",
		Argv:      []string{"/bin/sh", "-c", "true"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Wait(ctx, "forgotten"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for srv.Stats().ReapedProcesses == 0 {
		if time.Now().After(deadline) {
			t.Fatal("exited entry was not reaped")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := client.Read(ctx, &Read{ProcessId: "forgotten"}); !IsNotFound(err) {
		t.Fatalf("read after reap = %v, want not_found", err)
	}
}

func TestWriteIdempotentAndCloseInput(t *testing.T) {
	requirePOSIXChild(t)
	client, _ := testPair(t)
	ctx := context.Background()
	if _, err := client.Bind(ctx, t.TempDir(), &SandboxPolicy{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Start(ctx, &Start{
		ProcessId: "cat",
		Argv:      []string{"/bin/cat"},
	}); err != nil {
		t.Fatal(err)
	}
	first, err := client.Write(ctx, &Write{
		ProcessId: "cat", Chunk: []byte("x"), WriteId: "w1",
	})
	if err != nil || first.GetStatus() != writeAccepted {
		t.Fatalf("write = %v / %q", err, first.GetStatus())
	}
	second, err := client.Write(ctx, &Write{
		ProcessId: "cat", Chunk: []byte("x"), WriteId: "w1",
	})
	if err != nil || second.GetStatus() != writeAccepted {
		t.Fatalf("duplicate write = %v / %q", err, second.GetStatus())
	}
	if err := client.CloseInput(ctx, "cat"); err != nil {
		t.Fatal(err)
	}
	var output []byte
	var after int64
	deadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("timed out; output %q", output)
		}
		read, err := client.Read(ctx, &Read{ProcessId: "cat", AfterSeq: after})
		if err != nil {
			t.Fatal(err)
		}
		for _, chunk := range read.GetChunks() {
			output = append(output, chunk.GetData()...)
		}
		after = read.GetNextSeq()
		if read.GetEof() {
			break
		}
	}
	if string(output) != "x" {
		t.Fatalf("cat output = %q, want the single written byte", output)
	}
}

func TestDiagnosticsAndPing(t *testing.T) {
	client, _ := testPair(t)
	ctx := context.Background()
	ping, err := client.Ping(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ping.GetVersion() == "" {
		t.Fatal("ping reported no version")
	}
	workdir := t.TempDir()
	if _, err := client.Bind(ctx, workdir, &SandboxPolicy{}); err != nil {
		t.Fatal(err)
	}
	diag, err := client.Diagnostics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !diag.GetBound() || diag.GetWorkdir() != workdir {
		t.Fatalf("diagnostics = %+v", diag)
	}
	if diag.GetProtocolVersion() != ProtocolVersion {
		t.Fatalf("protocol = %d", diag.GetProtocolVersion())
	}
}

// blockingConn blocks every write until released and every read until
// closed.
type blockingConn struct {
	writeStarted chan struct{}
	release      chan struct{}
	closeOnce    sync.Once
	closed       chan struct{}
}

func newBlockingConn() *blockingConn {
	return &blockingConn{
		writeStarted: make(chan struct{}),
		release:      make(chan struct{}),
		closed:       make(chan struct{}),
	}
}

func (c *blockingConn) Write(p []byte) (int, error) {
	c.closeOnce.Do(func() { close(c.writeStarted) })
	select {
	case <-c.release:
		return len(p), nil
	case <-c.closed:
		return 0, net.ErrClosed
	}
}

func (c *blockingConn) Read([]byte) (int, error) {
	<-c.closed
	return 0, net.ErrClosed
}

func (c *blockingConn) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return nil
}

// TestClientWriteDoesNotHoldRequestLock pins the lock split: a blocked
// transport write must not stop notification registration or response
// dispatch.
func TestClientWriteDoesNotHoldRequestLock(t *testing.T) {
	conn := newBlockingConn()
	client := &Client{
		conn:     conn,
		reader:   bufio.NewReader(conn),
		pending:  make(map[uint64]chan *Frame),
		done:     make(chan struct{}),
		readDone: make(chan struct{}),
	}
	go client.readLoop()
	t.Cleanup(func() { _ = client.Close() })

	callDone := make(chan error, 1)
	go func() {
		_, err := client.Ping(context.Background())
		callDone <- err
	}()
	select {
	case <-conn.writeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("call never reached the transport write")
	}
	registered := make(chan struct{})
	go func() {
		client.SetNotificationHandler(func(*Notification) {})
		close(registered)
	}()
	select {
	case <-registered:
	case <-time.After(2 * time.Second):
		t.Fatal("request bookkeeping mutex is held across a transport write")
	}
	close(conn.release)
}

func TestFrameWriterDropsNotificationsUnderBackpressure(t *testing.T) {
	out := newStallWriter()
	writer := newFrameWriter(out, context.Background())
	go writer.run()
	t.Cleanup(func() {
		close(out.release)
		writer.stop()
	})
	if err := writer.enqueue(&Frame{Id: 1}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-out.started:
	case <-time.After(2 * time.Second):
		t.Fatal("writer never started")
	}
	srv := &Server{out: out, writer: writer}
	start := time.Now()
	for i := 0; i < frameQueueSize*2; i++ {
		srv.notify(&Notification{Body: &Notification_Lag{Lag: &Lag{
			ProcessId: "p",
		}}})
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("notify blocked for %v under backpressure", elapsed)
	}
	if srv.Stats().DroppedNotifications == 0 {
		t.Fatal("no notifications were dropped under backpressure")
	}
}

type stallWriter struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newStallWriter() *stallWriter {
	return &stallWriter{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (w *stallWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	<-w.release
	return len(p), nil
}

func TestStderrTailKeepsRecentLines(t *testing.T) {
	tail := newStderrTail(16)
	tail.add("first line\n")
	tail.add("second line\n")
	if got := tail.String(); !strings.Contains(got, "second line") ||
		strings.Contains(got, "first line") {
		t.Fatalf("tail = %q", got)
	}
}

// waitActorIdle blocks until the named process's actor has finished the
// operation it was running.
//
// A cancellation case wants the read it cancels to be the actor's own long
// poll: a read that lands on a busy actor is served inline (it holds its
// window but does not queue), so it would be a read racing a previous
// read's teardown rather than the queueing this helper is here to pin.
func waitActorIdle(t *testing.T, srv *Server, processID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if state := srv.bound(); state != nil {
			if entry, ok := state.sess.get(processID); ok && !entry.busy() {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("process %q never went idle", processID)
}

// TestCancelNotificationBeatsHandlerStart pins the registration order:
// a Cancel that arrives right behind its request must find the request
// already registered instead of being dropped on the floor. Each attempt
// waits for the actor to go idle first, so the read it cancels is the
// actor's own long poll rather than one served inline (see waitActorIdle).
func TestCancelNotificationBeatsHandlerStart(t *testing.T) {
	requirePOSIXChild(t)
	client, srv := testPair(t)
	ctx := context.Background()
	if _, err := client.Bind(ctx, t.TempDir(), &SandboxPolicy{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Start(ctx, &Start{
		ProcessId: "quiet",
		Argv:      []string{"/bin/sh", "-c", "sleep 30"},
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		waitActorIdle(t, srv, "quiet")
		id, ch := client.register()
		if err := client.writeFrame(&Frame{Id: id, Body: &Frame_Request{
			Request: &Request{Method: &Request_Read{Read: &Read{
				ProcessId: "quiet", WaitMs: 800,
			}}},
		}}); err != nil {
			t.Fatal(err)
		}
		if err := client.writeFrame(&Frame{Body: &Frame_Notification{
			Notification: &Notification{Body: &Notification_Cancel{
				Cancel: &Cancel{RequestId: id},
			}},
		}}); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		resp := <-ch
		code := resp.GetResponse().GetError().GetCode()
		if code != CodeCanceled {
			t.Fatalf("attempt %d: error = %q after %v, want %q",
				i, code, time.Since(start), CodeCanceled)
		}
		if elapsed := time.Since(start); elapsed > 700*time.Millisecond {
			t.Fatalf("attempt %d: cancel took %v; the read ran to its window",
				i, elapsed)
		}
	}
}

// TestEmptyWriteIDIsNotDeduped pins that an empty write id means "no
// retry key" instead of deduplicating every write that omits one.
func TestEmptyWriteIDIsNotDeduped(t *testing.T) {
	requirePOSIXChild(t)
	client, _ := testPair(t)
	ctx := context.Background()
	if _, err := client.Bind(ctx, t.TempDir(), &SandboxPolicy{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Start(ctx, &Start{
		ProcessId: "cat", Argv: []string{"/bin/cat"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, chunk := range [][]byte{[]byte("a"), []byte("b")} {
		if _, err := client.Write(ctx, &Write{
			ProcessId: "cat", Chunk: chunk,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := client.CloseInput(ctx, "cat"); err != nil {
		t.Fatal(err)
	}
	var output []byte
	var after int64
	deadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("timed out; output %q", output)
		}
		read, err := client.Read(ctx, &Read{ProcessId: "cat", AfterSeq: after})
		if err != nil {
			t.Fatal(err)
		}
		for _, chunk := range read.GetChunks() {
			output = append(output, chunk.GetData()...)
		}
		after = read.GetNextSeq()
		if read.GetEof() {
			break
		}
	}
	if string(output) != "ab" {
		t.Fatalf("cat output = %q, want both writes", output)
	}
}

// TestQuietReadReturnsEmptySuccess pins the documented remote read
// semantics: a wait window that lapses with no output answers with an
// empty, non-EOF result the caller polls again from.
// TestSessionReadReportsWindowExpiryAsATimeout pins the error a windowed
// read leaves behind. The child answers a request that hit its own
// deadline with a wire deadline_exceeded code, and a caller — the
// process feed above all — has to see the same "no output yet" the
// local backend produces, not a hard error it would treat as a dead
// session and stop reading from.
func TestSessionReadReportsWindowExpiryAsATimeout(t *testing.T) {
	requirePOSIXChild(t)
	runner := testRunner(t)
	ctx := context.Background()
	sess, err := runner.Start(ctx, sandbox.SessionSpec{
		Argv: []string{"/bin/sh", "-c", "sleep 30"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = sess.Close() }()

	readCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	out, err := sess.Read(readCtx, 0, 4096)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("read error = %v, want context.DeadlineExceeded", err)
	}
	if len(out.Chunks) != 0 || out.EOF {
		t.Fatalf("output = %+v, want the empty window result", out)
	}
}

func TestQuietReadReturnsEmptySuccess(t *testing.T) {
	requirePOSIXChild(t)
	client, _ := testPair(t)
	ctx := context.Background()
	if _, err := client.Bind(ctx, t.TempDir(), &SandboxPolicy{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Start(ctx, &Start{
		ProcessId: "quiet",
		Argv:      []string{"/bin/sh", "-c", "sleep 30"},
	}); err != nil {
		t.Fatal(err)
	}
	read, err := client.Read(ctx, &Read{ProcessId: "quiet", WaitMs: 100})
	if err != nil {
		t.Fatalf("quiet read = %v", err)
	}
	if len(read.GetChunks()) != 0 || read.GetEof() || read.GetExited() {
		t.Fatalf("quiet read = %+v, want an empty non-EOF result", read)
	}
}

// TestSessionCloseAfterReapIsIdempotent pins that closing a session the
// reaper already dropped succeeds instead of surfacing not_found.
func TestSessionCloseAfterReapIsIdempotent(t *testing.T) {
	requirePOSIXChild(t)
	client, srv := testPairConfigured(t, func(s *Server) {
		s.reapAfter = 20 * time.Millisecond
		s.reapInterval = 10 * time.Millisecond
	})
	runner, err := NewRemoteRunner(
		context.Background(), client, nil, t.TempDir(), &SandboxPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runner.Close() }()
	session, err := runner.Start(context.Background(), sandbox.SessionSpec{
		ID: "gone", Argv: []string{"/bin/sh", "-c", "true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for srv.Stats().ReapedProcesses == 0 {
		if time.Now().After(deadline) {
			t.Fatal("entry was never reaped")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("close after reap = %v, want nil", err)
	}
}

// TestBindAdvertisesBackendCapabilities pins that the client adopts the
// capabilities of the bound backend: the Hello answer only describes the
// static surface (no workspace is bound yet), so pty/signal/events have
// to travel on BindOk or they are silently reported as absent.
func TestBindAdvertisesBackendCapabilities(t *testing.T) {
	runner := testRunner(t)
	want := local.New(t.TempDir()).Capabilities().Features
	got := runner.Capabilities().Features
	if got.TTY != want.TTY || got.Signal != want.Signal {
		t.Fatalf("features = %+v, want the backend's %+v", got, want)
	}
	// The protocol's event stream has no consumers yet, so the remote
	// surface must not claim it even though the backend supports it.
	if got.Events {
		t.Fatal("runner advertises events while remote Watch is NotAvailable")
	}
}

// busyPair starts one process and holds its actor with a Wait, so a read
// that arrives now is served inline (see Server.read) rather than taking
// the actor itself. It returns only once the actor is busy, not about to
// be: a read that arrives a moment early takes the actor and would pass
// the same assertions for the wrong reason.
func busyPair(t *testing.T, argv ...string) (*Client, chan error) {
	t.Helper()
	client, srv := testPair(t)
	ctx := context.Background()
	if _, err := client.Bind(ctx, t.TempDir(), &SandboxPolicy{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Start(ctx, &Start{
		ProcessId: "busy",
		Argv:      argv,
	}); err != nil {
		t.Fatal(err)
	}
	waitDone := make(chan error, 1)
	go func() {
		_, err := client.Wait(ctx, "busy")
		waitDone <- err
	}()
	waitActorBusy(t, srv, "busy")
	return client, waitDone
}

// waitActorBusy is waitActorIdle's inverse: it blocks until the named
// process's actor is running an operation, i.e. until a read that lands
// is served inline.
func waitActorBusy(t *testing.T, srv *Server, processID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if state := srv.bound(); state != nil {
			if entry, ok := state.sess.get(processID); ok && entry.busy() {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("process %q never went busy", processID)
}

// stopBusy terminates what busyPair started and asserts that interrupting
// its outstanding wait reports a cancellation rather than a server fault.
func stopBusy(t *testing.T, client *Client, waitDone chan error) {
	t.Helper()
	ctx := context.Background()
	if _, err := client.Terminate(ctx, "busy", true); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-waitDone:
		if !IsCanceled(err) {
			t.Fatalf("interrupted wait = %v, want a cancelled code", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("interrupted wait did not return")
	}
}

// chunkText is the output of one read, joined for assertions.
func chunkText(out *ReadOk) string {
	var text strings.Builder
	for _, chunk := range out.GetChunks() {
		text.WriteString(string(chunk.GetData()))
	}
	return text.String()
}

// TestReadBehindBusyActorDeliversOutputThenHoldsItsWindow pins both halves
// of the inline read path: a read that lands while the actor is busy is
// served off the ring instead of queueing behind the actor, it returns the
// moment there is something to return, and otherwise it waits out the
// window it asked for instead of answering at once.
//
// The last part is what a whole class of callers depends on. The engine's
// one-shot exec reads until EOF and comes straight back for more; it cannot
// tell an early empty answer from "no output yet", so a server that answers
// at once turns that loop into a spin — 142,489 reads in one second when
// measured against the old behaviour, and 208 GB of a 51-hour allocation
// profile on a running desktop. The complementary mistake, always holding
// the window even when output is buffered, would put up to a window of
// latency in front of every chunk of a chatty command, which is why the
// fast half is asserted too.
func TestReadBehindBusyActorDeliversOutputThenHoldsItsWindow(t *testing.T) {
	requirePOSIXChild(t)
	client, waitDone := busyPair(t, "/bin/sh", "-c", "echo hi; sleep 30")
	ctx := context.Background()

	// The window is deliberately not defaultReadWaitMs: a server that
	// ignored the field and held its own constant would miss the lower
	// bound below.
	const (
		window = 1200 * time.Millisecond
		waitMs = 1200
	)

	start := time.Now()
	out, err := client.Read(ctx, &Read{ProcessId: "busy", WaitMs: waitMs})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= window/2 {
		t.Fatalf("a read that found output took %v, want it back well inside its %v window",
			elapsed, window)
	}
	if got := chunkText(out); !strings.Contains(got, "hi") {
		t.Fatalf("read = %q, want the process's output", got)
	}
	// The cursor travels between reads: the drain loop keeps it, so a
	// second read asks only for what came after the first.
	after := out.GetNextSeq()

	// The ring is empty from here on, so the same read has to hold the
	// window. The outstanding wait it is not queueing behind is 30s.
	start = time.Now()
	out, err = client.Read(ctx, &Read{
		ProcessId: "busy", WaitMs: waitMs, AfterSeq: after,
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < window/2 {
		t.Fatalf("an empty read returned in %v, want it to hold its %v window",
			elapsed, window)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("an empty read took %v, want at most its %v window",
			elapsed, window)
	}
	if len(out.GetChunks()) != 0 || out.GetEof() {
		t.Fatalf("read = %+v, want an empty non-EOF result", out)
	}

	stopBusy(t, client, waitDone)
}

// TestReadLoopBehindBusyActorDoesNotSpin pins what an unpaced drain loop
// costs against a busy actor. The engine's one-shot exec reads until EOF
// and re-issues immediately; while a command runs the ring is empty most of
// the time, so every one of those reads lands on the inline path. If that
// path answers at once, the loop runs at the speed of the round trip —
// thousands of frames a second, a core busy in the child, and a frame
// allocated per iteration. The wait window is the only thing pacing it, so
// a second of looping must cost a handful of reads: the band catches both
// failures, a server that dropped the window and one that substituted its
// own for the caller's.
func TestReadLoopBehindBusyActorDoesNotSpin(t *testing.T) {
	requirePOSIXChild(t)
	client, waitDone := busyPair(t, "/bin/sh", "-c", "sleep 30")
	ctx := context.Background()

	const (
		window  = 100 * time.Millisecond
		measure = time.Second
	)
	deadline := time.Now().Add(measure)
	reads := 0
	for time.Now().Before(deadline) {
		out, err := client.Read(ctx, &Read{
			ProcessId: "busy",
			WaitMs:    int64(window / time.Millisecond),
		})
		if err != nil {
			t.Fatalf("read %d: %v", reads, err)
		}
		if len(out.GetChunks()) != 0 || out.GetEof() {
			t.Fatalf("read %d = %+v, want an empty non-EOF result", reads, out)
		}
		reads++
	}
	// Ten is what a 100ms window allows in one second. The band is wide
	// because a loaded machine stretches each iteration, but it stays two
	// orders of magnitude below a spin and above the two reads a server
	// holding a constant 500ms window would allow.
	if reads < 6 || reads > 25 {
		t.Fatalf("%d reads in %v behind a busy actor, want a %v window to pace them (about %d)",
			reads, measure, window, measure/window)
	}

	// The request's own deadline is the other bound: a caller that cannot
	// afford its window (the process feed asks for 250ms) has to be able
	// to cut the read short.
	readCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := client.Read(readCtx, &Read{ProcessId: "busy", WaitMs: 5000})
	if err == nil {
		t.Fatal("read past its deadline succeeded")
	}
	if !IsCode(err, CodeDeadlineExceeded) && !IsCanceled(err) {
		t.Fatalf("read past its deadline = %v, want a deadline error", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("deadline took %v, want the caller's 100ms to bound the read",
			elapsed)
	}

	stopBusy(t, client, waitDone)
}

func pgrepMarker(marker string) string {
	out, _ := exec.Command("pgrep", "-f", marker).Output()
	return strings.TrimSpace(string(out))
}
