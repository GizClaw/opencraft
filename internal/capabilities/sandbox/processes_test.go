package sandbox

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	coresandbox "github.com/GizClaw/flowcraft/core/sandbox"
)

// fakeSession is a scriptable sandbox.Session: tests write output into
// it and decide how the stream ends (EOF, close, gap).
type fakeSession struct {
	id  string
	pid int

	mu      sync.Mutex
	chunks  []fakeChunk
	nextSeq int64
	wake    chan struct{}
	eof     bool
	closed  bool
	gap     bool
	// peek answers every read immediately with an empty result, the way
	// the execd child answers one whose wait window elapsed (or a read
	// that landed while the process's actor was busy).
	peek bool
	// timedOut answers every read with the backend's own timeout type
	// (not context.DeadlineExceeded), the way a backend that classifies
	// its window expiry itself would.
	timedOut bool
	exit     coresandbox.SessionExit
	waitErr  error
	reads    int
}

type fakeChunk struct {
	seq    int64
	stream coresandbox.SessionStream
	data   []byte
}

func newFakeSession(id string, pid int) *fakeSession {
	return &fakeSession{id: id, pid: pid, wake: make(chan struct{})}
}

func (s *fakeSession) ID() string { return s.id }
func (s *fakeSession) PID() int   { return s.pid }

func (s *fakeSession) Capabilities() coresandbox.SessionCapabilities {
	return coresandbox.SessionCapabilities{}
}

func (s *fakeSession) Read(
	ctx context.Context,
	afterSeq int64,
	maxBytes int,
) (coresandbox.SessionOutput, error) {
	for {
		s.mu.Lock()
		s.reads++
		if s.peek {
			s.mu.Unlock()
			return coresandbox.SessionOutput{NextSeq: afterSeq}, nil
		}
		if s.timedOut {
			s.mu.Unlock()
			return coresandbox.SessionOutput{}, errdefs.Timeoutf("read window elapsed")
		}
		if s.gap {
			s.mu.Unlock()
			return coresandbox.SessionOutput{}, coresandbox.ErrSequenceGap
		}
		if s.closed {
			s.mu.Unlock()
			return coresandbox.SessionOutput{}, coresandbox.ErrSessionClosed
		}
		if out, ok := s.collectLocked(afterSeq, maxBytes); ok {
			s.mu.Unlock()
			return out, nil
		}
		if s.eof {
			s.mu.Unlock()
			return coresandbox.SessionOutput{NextSeq: afterSeq, EOF: true}, nil
		}
		wake := s.wake
		s.mu.Unlock()
		select {
		case <-wake:
		case <-ctx.Done():
			return coresandbox.SessionOutput{}, ctx.Err()
		}
	}
}

func (s *fakeSession) collectLocked(
	afterSeq int64,
	maxBytes int,
) (coresandbox.SessionOutput, bool) {
	remaining := int64(maxBytes)
	next := afterSeq
	var out []coresandbox.OutputChunk
	for _, chunk := range s.chunks {
		if remaining <= 0 {
			break
		}
		end := chunk.seq + int64(len(chunk.data))
		if next >= end {
			continue
		}
		start := next - chunk.seq
		n := int64(len(chunk.data)) - start
		if n > remaining {
			n = remaining
		}
		out = append(out, coresandbox.OutputChunk{
			Seq:    next,
			Stream: chunk.stream,
			Data:   append([]byte(nil), chunk.data[start:start+n]...),
		})
		next += n
		remaining -= n
	}
	if len(out) == 0 {
		return coresandbox.SessionOutput{}, false
	}
	return coresandbox.SessionOutput{NextSeq: next, Chunks: out}, true
}

func (s *fakeSession) write(stream coresandbox.SessionStream, data string) {
	s.buffer(stream, data, true)
}

// writeQuiet buffers output without waking blocked readers: only a
// fresh read can see it, which is how the close-time salvage is
// exercised deterministically.
func (s *fakeSession) writeQuiet(stream coresandbox.SessionStream, data string) {
	s.buffer(stream, data, false)
}

func (s *fakeSession) buffer(
	stream coresandbox.SessionStream,
	data string,
	wake bool,
) {
	s.mu.Lock()
	s.chunks = append(s.chunks, fakeChunk{
		seq:    s.nextSeq,
		stream: stream,
		data:   []byte(data),
	})
	s.nextSeq += int64(len(data))
	if wake {
		s.wakeLocked()
	}
	s.mu.Unlock()
}

// finish makes the process exit: every buffered byte is drainable, then
// the reads report EOF.
func (s *fakeSession) finish(exit coresandbox.SessionExit) {
	s.mu.Lock()
	s.eof = true
	s.exit = exit
	s.wakeLocked()
	s.mu.Unlock()
}

func (s *fakeSession) cut(closed bool, gap bool) {
	s.mu.Lock()
	s.closed = closed
	s.gap = gap
	s.wakeLocked()
	s.mu.Unlock()
}

func (s *fakeSession) wakeLocked() {
	close(s.wake)
	s.wake = make(chan struct{})
}

func (s *fakeSession) readCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

func (s *fakeSession) Wait(context.Context) (coresandbox.SessionExit, error) {
	if s.waitErr != nil {
		return coresandbox.SessionExit{}, s.waitErr
	}
	return s.exit, nil
}

func (s *fakeSession) Write(context.Context, []byte) error { return nil }
func (s *fakeSession) CloseInput() error                   { return nil }
func (s *fakeSession) Resize(context.Context, int, int) error {
	return nil
}
func (s *fakeSession) Signal(context.Context, coresandbox.SessionSignal) error {
	return nil
}
func (s *fakeSession) Terminate(context.Context) error { return nil }
func (s *fakeSession) Watch(context.Context) (coresandbox.SessionWatcher, error) {
	return nil, coresandbox.ErrSessionClosed
}
func (s *fakeSession) Close() error { s.cut(true, false); return nil }

// waitFor polls until cond holds or the deadline passes.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !cond() {
		t.Fatal("condition not met before deadline")
	}
}

func tapSession(
	t *testing.T,
	feed *ProcessFeed,
	conversation string,
	spec coresandbox.SessionSpec,
	sess coresandbox.Session,
) coresandbox.Session {
	t.Helper()
	ctx := context.Background()
	if conversation != "" {
		ctx = sessionCtx(conversation)
	}
	tapped := feed.Tap(ctx, spec, sess)
	if tapped.ID() != sess.ID() {
		t.Fatalf("tapped session id = %q, want %q", tapped.ID(), sess.ID())
	}
	return tapped
}

func TestProcessFeedTapsConversationSessions(t *testing.T) {
	feed := NewProcessFeed()
	defer func() { _ = feed.Close() }()

	sess := newFakeSession("p-1", 4321)
	tapSession(t, feed, "conv-1", coresandbox.SessionSpec{
		Argv: []string{"npm", "run", "dev"},
		Opts: coresandbox.ExecOptions{WorkDir: "/ws"},
	}, sess)

	sess.write(coresandbox.SessionStreamStdout, "VITE ready in 412 ms\n")
	waitFor(t, func() bool {
		procs := feed.List("conv-1")
		return len(procs) == 1 && strings.Contains(procs[0].Tail, "VITE ready")
	})
	proc := feed.List("conv-1")[0]
	if proc.ID != "p-1" || proc.PID != 4321 {
		t.Errorf("identity = %q/%d, want p-1/4321", proc.ID, proc.PID)
	}
	if got := strings.Join(proc.Argv, " "); got != "npm run dev" {
		t.Errorf("argv = %q, want %q", got, "npm run dev")
	}
	if proc.Workdir != "/ws" {
		t.Errorf("workdir = %q, want /ws", proc.Workdir)
	}
	if !proc.Running {
		t.Error("process reported as stopped while it is still running")
	}
	if proc.Truncated {
		t.Error("short output reported as truncated")
	}
	if procs := feed.List("conv-2"); len(procs) != 0 {
		t.Errorf("other conversation sees %d processes, want 0", len(procs))
	}
	if procs := feed.List(""); len(procs) != 0 {
		t.Errorf("empty conversation id sees %d processes, want 0", len(procs))
	}
}

func TestProcessFeedSkipsSpawnsWithoutConversation(t *testing.T) {
	feed := NewProcessFeed()
	defer func() { _ = feed.Close() }()

	sess := newFakeSession("p-1", 1)
	if tapped := tapSession(t, feed, "",
		coresandbox.SessionSpec{Argv: []string{"true"}}, sess); tapped != sess {
		t.Error("Tap must return untapped sessions unchanged")
	}
	time.Sleep(50 * time.Millisecond)
	if got := sess.readCount(); got != 0 {
		t.Errorf("untapped session was read %d times, want 0", got)
	}
}

// TestProcessFeedSalvagesOnClose pins the short-command path: the
// session is closed right after the command ends, so the drain may
// never have seen the output. Close must capture it anyway.
func TestProcessFeedSalvagesOnClose(t *testing.T) {
	feed := NewProcessFeed()
	defer func() { _ = feed.Close() }()

	sess := newFakeSession("p-1", 1)
	tapped := tapSession(t, feed, "conv-1",
		coresandbox.SessionSpec{Argv: []string{"echo", "hi"}}, sess)
	// Let the drain enter its first (empty) read, then buffer output
	// without waking it: only the close-time salvage can see the bytes.
	waitFor(t, func() bool { return sess.readCount() > 0 })
	sess.writeQuiet(coresandbox.SessionStreamStdout, "late output\n")
	if err := tapped.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	procs := feed.List("conv-1")
	if len(procs) != 1 {
		t.Fatalf("processes = %d, want 1", len(procs))
	}
	if !strings.Contains(procs[0].Tail, "late output") {
		t.Errorf("tail = %q, want the salvaged output", procs[0].Tail)
	}
	if procs[0].Running {
		t.Error("closed session still reported as running")
	}
}

func TestProcessFeedTailIsBounded(t *testing.T) {
	feed := NewProcessFeed()
	defer func() { _ = feed.Close() }()

	sess := newFakeSession("p-1", 1)
	tapSession(t, feed, "conv-1", coresandbox.SessionSpec{Argv: []string{"yes"}}, sess)
	// 20 KiB in one burst: more than the 16 KiB tail, so the feed keeps
	// the newest bytes and flags the truncation.
	big := strings.Repeat("a", 4000) + strings.Repeat("b", 4000) +
		strings.Repeat("c", 4000) + strings.Repeat("d", 8200)
	sess.write(coresandbox.SessionStreamStdout, big)
	waitFor(t, func() bool {
		procs := feed.List("conv-1")
		return len(procs) == 1 && procs[0].Seq == int64(len(big))
	})
	proc := feed.List("conv-1")[0]
	if len(proc.Tail) != processTailBytes {
		t.Errorf("tail length = %d, want %d", len(proc.Tail), processTailBytes)
	}
	if !proc.Truncated {
		t.Error("truncated tail not reported")
	}
	if want := big[len(big)-processTailBytes:]; proc.Tail != want {
		t.Errorf("tail is not the newest %d bytes of the output",
			processTailBytes)
	}
}

func TestProcessFeedMergesStreamsInArrivalOrder(t *testing.T) {
	feed := NewProcessFeed()
	defer func() { _ = feed.Close() }()

	sess := newFakeSession("p-1", 1)
	tapSession(t, feed, "conv-1", coresandbox.SessionSpec{Argv: []string{"sh"}}, sess)
	sess.write(coresandbox.SessionStreamStdout, "out-1\n")
	sess.write(coresandbox.SessionStreamStderr, "err-1\n")
	sess.write(coresandbox.SessionStreamStdout, "out-2\n")
	waitFor(t, func() bool {
		procs := feed.List("conv-1")
		return len(procs) == 1 &&
			procs[0].Tail == "out-1\nerr-1\nout-2\n"
	})
}

func TestProcessFeedReapsExitCode(t *testing.T) {
	feed := NewProcessFeed()
	defer func() { _ = feed.Close() }()

	sess := newFakeSession("p-1", 1)
	tapSession(t, feed, "conv-1", coresandbox.SessionSpec{Argv: []string{"false"}}, sess)
	sess.write(coresandbox.SessionStreamStdout, "done\n")
	sess.finish(coresandbox.SessionExit{
		Code:   3,
		Reason: coresandbox.SessionExited,
	})
	waitFor(t, func() bool {
		procs := feed.List("conv-1")
		return len(procs) == 1 && !procs[0].Running
	})
	proc := feed.List("conv-1")[0]
	if proc.ExitCode == nil || *proc.ExitCode != 3 {
		t.Fatalf("exit code = %v, want 3", proc.ExitCode)
	}
	if proc.ExitReason != "exited" {
		t.Errorf("exit reason = %q, want exited", proc.ExitReason)
	}
	if !strings.Contains(proc.Tail, "done") {
		t.Errorf("tail = %q, want the drained output", proc.Tail)
	}
}

func TestProcessFeedFreezesOnClosedSession(t *testing.T) {
	feed := NewProcessFeed()
	defer func() { _ = feed.Close() }()

	sess := newFakeSession("p-1", 1)
	tapSession(t, feed, "conv-1", coresandbox.SessionSpec{Argv: []string{"dev"}}, sess)
	sess.write(coresandbox.SessionStreamStdout, "before close\n")
	waitFor(t, func() bool {
		procs := feed.List("conv-1")
		return len(procs) == 1 && strings.Contains(procs[0].Tail, "before close")
	})
	// Close drops whatever the feed had not read yet (the backend's
	// read reports the closed handle before the buffered tail), so the
	// frozen entry keeps exactly the bytes captured before it.
	sess.cut(true, false)
	waitFor(t, func() bool {
		procs := feed.List("conv-1")
		return len(procs) == 1 && !procs[0].Running
	})
	proc := feed.List("conv-1")[0]
	if proc.ExitCode != nil {
		t.Errorf("exit code = %v, want none for a released session", *proc.ExitCode)
	}
	if !strings.Contains(proc.Tail, "before close") {
		t.Errorf("tail = %q, want the bytes read before close", proc.Tail)
	}
}

func TestProcessFeedFreezesTailOnSequenceGap(t *testing.T) {
	feed := NewProcessFeed()
	defer func() { _ = feed.Close() }()

	sess := newFakeSession("p-1", 1)
	tapSession(t, feed, "conv-1", coresandbox.SessionSpec{Argv: []string{"noisy"}}, sess)
	sess.write(coresandbox.SessionStreamStdout, "line one\n")
	waitFor(t, func() bool {
		procs := feed.List("conv-1")
		return len(procs) == 1 && strings.Contains(procs[0].Tail, "line one")
	})
	sess.cut(false, true)
	waitFor(t, func() bool {
		procs := feed.List("conv-1")
		return len(procs) == 1 && !procs[0].Running
	})
	proc := feed.List("conv-1")[0]
	if !proc.Truncated {
		t.Error("gap did not mark the tail truncated")
	}
	if !strings.Contains(proc.Tail, "line one") {
		t.Errorf("tail = %q, want the pre-gap bytes", proc.Tail)
	}
}

func TestProcessFeedCloseStopsDrainers(t *testing.T) {
	feed := NewProcessFeed()
	sess := newFakeSession("p-1", 1)
	tapSession(t, feed, "conv-1", coresandbox.SessionSpec{Argv: []string{"dev"}}, sess)
	sess.write(coresandbox.SessionStreamStdout, "hello\n")
	waitFor(t, func() bool {
		procs := feed.List("conv-1")
		return len(procs) == 1 && strings.Contains(procs[0].Tail, "hello")
	})
	if err := feed.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitFor(t, func() bool {
		procs := feed.List("conv-1")
		return len(procs) == 1 && !procs[0].Running
	})
	// The drainer is gone: output after the close is not read, and the
	// tail stays frozen.
	before := sess.readCount()
	time.Sleep(50 * time.Millisecond)
	if after := sess.readCount(); after != before {
		t.Errorf("reads after Close = %d, want %d (drainer still running)",
			after, before)
	}
	if err := feed.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestProcessFeedCapsEntriesPerConversation(t *testing.T) {
	feed := NewProcessFeed()
	defer func() { _ = feed.Close() }()

	for i := 0; i < processKeepPerConversation+2; i++ {
		sess := newFakeSession("p", i)
		tapSession(t, feed, "conv-1", coresandbox.SessionSpec{
			Argv: []string{"cmd", fmt.Sprintf("run-%d", i)},
		}, sess)
	}
	procs := feed.List("conv-1")
	if len(procs) != processKeepPerConversation {
		t.Fatalf("kept %d processes, want %d", len(procs), processKeepPerConversation)
	}
	// The two oldest starters fell off, in order.
	if procs[0].Argv[1] != "run-2" {
		t.Errorf("oldest kept = %q, want %q", procs[0].Argv[1], "run-2")
	}
	if last := procs[len(procs)-1]; last.Argv[1] != fmt.Sprintf(
		"run-%d", processKeepPerConversation+1) {
		t.Errorf("newest kept = %q, want the last start", last.Argv[1])
	}
}

func TestProcessFeedPacesReadsOfASilentProcess(t *testing.T) {
	feed := NewProcessFeed()
	defer func() { _ = feed.Close() }()

	// A session that answers every read with an empty result right away
	// is the worst case: the drain has nothing to block on.
	sess := newFakeSession("p-1", 9)
	sess.peek = true
	tapSession(t, feed, "conv-1", coresandbox.SessionSpec{Argv: []string{"dev"}}, sess)

	window := 3 * processIdleGap
	time.Sleep(window)
	reads := sess.readCount()
	// One read per gap, plus the first one: anything close to the read
	// count a spinning drain would produce (thousands) is a regression.
	if want := int(window/processIdleGap) + 2; reads > want {
		t.Errorf("reads in %s = %d, want at most %d: the drain is polling a silent process in a tight loop",
			window, reads, want)
	}
	// Pacing is not the same as giving up: the entry is still running
	// and still readable.
	procs := feed.List("conv-1")
	if len(procs) != 1 || !procs[0].Running {
		t.Fatalf("processes = %+v, want one running entry", procs)
	}
	// Output written after the pause is picked up by the next read.
	sess.mu.Lock()
	sess.peek = false
	sess.mu.Unlock()
	sess.write(coresandbox.SessionStreamStdout, "listening on 5173\n")
	waitFor(t, func() bool {
		procs := feed.List("conv-1")
		return len(procs) == 1 && strings.Contains(procs[0].Tail, "listening on 5173")
	})
}

func TestProcessFeedReadsThroughABackendTimeoutType(t *testing.T) {
	feed := NewProcessFeed()
	defer func() { _ = feed.Close() }()

	// The execd child answers a read whose request deadline expired with
	// its own wire timeout (not context.DeadlineExceeded); a backend
	// that classifies its expiry itself looks the same to the feed. An
	// expired window means "no output yet", so the entry must stay alive
	// and keep reading instead of freezing its tail.
	sess := newFakeSession("p-1", 12)
	sess.timedOut = true
	tapSession(t, feed, "conv-1", coresandbox.SessionSpec{Argv: []string{"dev"}}, sess)

	time.Sleep(processIdleGap + processReadWindow)
	sess.mu.Lock()
	sess.timedOut = false
	sess.mu.Unlock()
	sess.write(coresandbox.SessionStreamStdout, "compiled\n")
	waitFor(t, func() bool {
		procs := feed.List("conv-1")
		return len(procs) == 1 && strings.Contains(procs[0].Tail, "compiled")
	})
	if procs := feed.List("conv-1"); !procs[0].Running {
		t.Errorf("entry stopped after a window expiry: %+v", procs[0])
	}
}

// tapRunner is a Runner whose Start returns a scripted session, so the
// HostSandbox tap path can be exercised without a real backend.
type tapRunner struct {
	sess *fakeSession
	mu   sync.Mutex
	got  []coresandbox.SessionSpec
}

func (r *tapRunner) Start(
	_ context.Context, spec coresandbox.SessionSpec,
) (coresandbox.Session, error) {
	r.mu.Lock()
	r.got = append(r.got, spec)
	r.mu.Unlock()
	return r.sess, nil
}

func (r *tapRunner) List(context.Context) ([]coresandbox.SessionInfo, error) {
	return nil, nil
}

func (r *tapRunner) Terminate(context.Context, string) error { return nil }
func (r *tapRunner) Close() error                            { return nil }

func (r *tapRunner) Capabilities() coresandbox.Capabilities {
	return coresandbox.Capabilities{}
}

func TestHostSandboxTapsStartedSessions(t *testing.T) {
	feed := NewProcessFeed()
	defer func() { _ = feed.Close() }()

	sess := newFakeSession("p-1", 77)
	host := &HostSandbox{
		sessions:   newTestStore(t),
		confined:   &tapRunner{sess: sess},
		unconfined: &tapRunner{sess: sess},
		procs:      feed,
	}
	ctx := sessionCtx("conv-1")
	if _, err := host.Start(ctx, coresandbox.SessionSpec{
		Argv: []string{"go", "test", "./..."},
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	sess.write(coresandbox.SessionStreamStdout, "ok\n")
	waitFor(t, func() bool {
		procs := feed.List("conv-1")
		return len(procs) == 1 && strings.Contains(procs[0].Tail, "ok")
	})
}
