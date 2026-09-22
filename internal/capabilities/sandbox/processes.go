package sandbox

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/resource"
	coresandbox "github.com/GizClaw/flowcraft/core/sandbox"
	"github.com/GizClaw/flowcraft/core/telemetry"
)

// ProcessFeedResourceKind is the deploy resource kind for the shared
// process feed.
const ProcessFeedResourceKind = "opencraft.processes"

const (
	// processTailBytes bounds the output tail the feed keeps per
	// process. The card shows a glanceable suffix, not a log viewer.
	processTailBytes = 16 << 10
	// processReadChunk matches the one-shot Exec drain size: one read
	// per chunk, so a chatty command is caught up in a few reads.
	processReadChunk = 64 << 10
	// processReadWindow bounds one drain read. A window (instead of an
	// open-ended read) keeps the feed from holding a backend's single
	// blocking read slot: on the execd child every other operation of
	// that process queues behind an in-flight read.
	processReadWindow = 250 * time.Millisecond
	// processIdleGap paces the drain after a read that brought nothing.
	// An empty read is the normal answer for a silent process, and it
	// costs a request the child has to serve, so asking again in a
	// tight loop would keep the child busy and the conversation's
	// processes perpetually re-read. Half a second is invisible next to
	// the card's own poll interval while cutting the steady-state
	// traffic to about two reads a second.
	processIdleGap = 500 * time.Millisecond
	// processSalvageWait bounds the whole close-time salvage, and
	// processExitWait the exit-status query after the output drained.
	// The process is already gone in both cases, so these only wait
	// out a wedged backend.
	processSalvageWait = 5 * time.Second
	processExitWait    = 5 * time.Second
	// processKeepPerConversation caps the tapped processes one
	// conversation keeps. Long conversations run a lot of commands;
	// the oldest stopped ones fall off first.
	processKeepPerConversation = 32
	// processExitedTTL is how long a stopped process stays readable
	// after its output drained. A read is a snapshot, not a
	// subscription: one that lands after a command ended still finds
	// how it ended instead of finding nothing.
	processExitedTTL = 10 * time.Minute
)

// Process is one immutable snapshot of a tapped child process.
type Process struct {
	// ID is the sandbox session id; it is what exec_session calls the
	// process id.
	ID             string
	ConversationID string
	Argv           []string
	Workdir        string
	TTY            bool
	PID            int
	StartedAt      time.Time
	Running        bool
	// ExitCode is only set for processes the feed saw exit; a killed
	// or released session reports the reason instead.
	ExitCode   *int
	ExitReason string
	// Tail is the chronological output suffix with stdout, stderr and
	// tty bytes merged (the card reads as a terminal).
	Tail string
	// Truncated reports that Tail is a suffix, not the whole output.
	Truncated bool
	// Seq is the drain cursor: it only moves when new output arrived,
	// so UI pollers can skip re-rendering untouched entries.
	Seq int64
}

// ProcessFeed is the conversation-scoped, read-only tap of sandboxed
// child processes. Every session started inside a session run (RunInfo
// carrying a conversation id) is registered and drained into a bounded
// tail, so the UI can show what a command or an exec_session prints
// without waiting for the model to read it.
//
// Draining never consumes bytes: sandbox output logs are cursor-based
// and replayable, so the feed reads with its own cursor while the model
// keeps reading with its own. Reads are bounded by processReadWindow so
// tapping cannot stall the model's own session operations, and the
// sequence-gap case (the backend trimmed output faster than the feed
// read it) is treated as "the tail is all we have": the entry freezes
// instead of retrying with a dead cursor.
//
// Closing a tapped session salvages the backend's remaining buffered
// output before the handle closes, so a short-lived command (exec_command
// closes its session as soon as the command ends) still lands in the
// feed complete instead of racing the drain.
//
// The feed is a per-generation resource: it is closed with its runtime
// (every drainer stops with it) and the Host rebinds to the next
// generation's instance after a reload.
type ProcessFeed struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	convs  map[string][]*processEntry
	closed bool
}

var _ io.Closer = (*ProcessFeed)(nil)

// NewProcessFeed creates an empty feed. The returned feed owns the
// lifetime of every drainer it starts: Close stops them all.
func NewProcessFeed() *ProcessFeed {
	ctx, cancel := context.WithCancel(context.Background())
	return &ProcessFeed{
		ctx:    ctx,
		cancel: cancel,
		convs:  make(map[string][]*processEntry),
	}
}

// Tap registers one started session and returns the handle callers
// should use: a thin wrapper whose Close salvages the output the drain
// had not read yet. Sessions started outside a session run (no RunInfo
// conversation id) are returned untouched: they are host plumbing, not
// something a conversation card shows. A nil feed is a no-op, so
// deployments without the resource keep working.
func (f *ProcessFeed) Tap(
	ctx context.Context,
	spec coresandbox.SessionSpec,
	sess coresandbox.Session,
) coresandbox.Session {
	if f == nil || sess == nil {
		return sess
	}
	info, ok := agent.RunInfoFromContext(ctx)
	if !ok || info.ConversationID == "" {
		return sess
	}
	entry := &processEntry{
		feed:           f,
		conversationID: info.ConversationID,
		id:             sess.ID(),
		argv:           append([]string(nil), spec.Argv...),
		workdir:        spec.Opts.WorkDir,
		tty:            spec.TTY,
		pid:            sess.PID(),
		startedAt:      time.Now(),
		session:        sess,
	}
	entry.ctx, entry.cancel = context.WithCancel(f.ctx)

	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		entry.cancel()
		return sess
	}
	f.pruneLocked(time.Now())
	conv := f.convs[entry.conversationID]
	conv = append(conv, entry)
	if excess := len(conv) - processKeepPerConversation; excess > 0 {
		conv = f.evictLocked(conv, excess)
	}
	f.convs[entry.conversationID] = conv
	f.mu.Unlock()

	go entry.drain()
	return &tappedSession{inner: sess, entry: entry}
}

// List returns the tapped processes of one conversation, oldest first.
// Stopped entries past their TTL, and every entry once the feed closed,
// are gone: the card shows what the current runtime generation knows.
func (f *ProcessFeed) List(conversationID string) []Process {
	if f == nil || conversationID == "" {
		return nil
	}
	f.mu.Lock()
	entries := append([]*processEntry(nil), f.convs[conversationID]...)
	f.mu.Unlock()
	out := make([]Process, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.snapshot())
	}
	return out
}

// Close stops every drainer. It is what runtime teardown calls; the
// entries themselves stay readable until then.
func (f *ProcessFeed) Close() error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil
	}
	f.closed = true
	entries := make([]*processEntry, 0, len(f.convs))
	for _, conv := range f.convs {
		entries = append(entries, conv...)
	}
	f.mu.Unlock()
	f.cancel()
	for _, entry := range entries {
		entry.stop()
	}
	return nil
}

// pruneLocked drops stopped entries past their TTL. Callers hold f.mu.
func (f *ProcessFeed) pruneLocked(now time.Time) {
	for conv, entries := range f.convs {
		kept := entries[:0]
		for _, entry := range entries {
			if entry.expired(now) {
				entry.stop()
				continue
			}
			kept = append(kept, entry)
		}
		if len(kept) == 0 {
			delete(f.convs, conv)
			continue
		}
		f.convs[conv] = kept
	}
}

// evictLocked drops n entries, oldest stopped first, then oldest
// outright (a conversation that never stops its processes must not grow
// the feed without bound). It returns the kept slice.
func (f *ProcessFeed) evictLocked(
	entries []*processEntry,
	n int,
) []*processEntry {
	drop := make(map[int]bool, n)
	for i, entry := range entries {
		if n == 0 {
			break
		}
		if !entry.isRunning() {
			drop[i] = true
			n--
		}
	}
	for i := 0; n > 0 && i < len(entries); i++ {
		if !drop[i] {
			drop[i] = true
			n--
		}
	}
	kept := entries[:0]
	for i, entry := range entries {
		if drop[i] {
			entry.stop()
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

// tappedSession is the handle the feed hands back in place of the
// started session. It forwards everything and makes Close a last read:
// by then the process is done producing, so whatever the backend still
// buffers is captured before the handle goes away.
type tappedSession struct {
	inner coresandbox.Session
	entry *processEntry
}

var _ coresandbox.Session = (*tappedSession)(nil)

func (s *tappedSession) Close() error {
	s.entry.finish()
	return s.inner.Close()
}

func (s *tappedSession) ID() string { return s.inner.ID() }
func (s *tappedSession) PID() int   { return s.inner.PID() }

func (s *tappedSession) Capabilities() coresandbox.SessionCapabilities {
	return s.inner.Capabilities()
}

func (s *tappedSession) Read(
	ctx context.Context, afterSeq int64, maxBytes int,
) (coresandbox.SessionOutput, error) {
	return s.inner.Read(ctx, afterSeq, maxBytes)
}

func (s *tappedSession) Write(ctx context.Context, data []byte) error {
	return s.inner.Write(ctx, data)
}

func (s *tappedSession) CloseInput() error { return s.inner.CloseInput() }

func (s *tappedSession) Resize(ctx context.Context, rows, cols int) error {
	return s.inner.Resize(ctx, rows, cols)
}

func (s *tappedSession) Signal(
	ctx context.Context, sig coresandbox.SessionSignal,
) error {
	return s.inner.Signal(ctx, sig)
}

func (s *tappedSession) Terminate(ctx context.Context) error {
	return s.inner.Terminate(ctx)
}

func (s *tappedSession) Wait(
	ctx context.Context,
) (coresandbox.SessionExit, error) {
	return s.inner.Wait(ctx)
}

func (s *tappedSession) Watch(
	ctx context.Context,
) (coresandbox.SessionWatcher, error) {
	return s.inner.Watch(ctx)
}

// processEntry tracks one tapped session: its identity, its bounded
// output tail, and the shared drain cursor.
//
// Two readers can touch the session — the background drain and the
// close-time salvage — so every read goes through readMu and advances
// the same cursor. That keeps the tail in order (a reader appends what
// it read, once) while letting either reader run alone when the other
// is gone.
type processEntry struct {
	feed           *ProcessFeed
	conversationID string
	id             string
	argv           []string
	workdir        string
	tty            bool
	pid            int
	startedAt      time.Time

	session coresandbox.Session
	ctx     context.Context
	cancel  context.CancelFunc

	readMu   sync.Mutex
	finishOn sync.Once

	mu         sync.Mutex
	tail       []byte
	truncated  bool
	seq        int64
	stopped    bool
	stoppedAt  time.Time
	exitCode   *int
	exitReason string
}

// drain reads the session's output into the tail until the process
// ends, the session closes, or the feed shuts down.
func (e *processEntry) drain() {
	for {
		if e.ctx.Err() != nil {
			return
		}
		ctx, cancel := context.WithTimeout(e.ctx, processReadWindow)
		e.readMu.Lock()
		if e.isStopped() {
			e.readMu.Unlock()
			cancel()
			return
		}
		out, err := e.session.Read(ctx, e.cursor(), processReadChunk)
		e.readMu.Unlock()
		cancel()
		progressed := len(out.Chunks) > 0
		switch {
		case err == nil:
			e.append(out.Chunks)
			e.advance(out.NextSeq)
			if out.EOF {
				e.reap()
				return
			}
		case errors.Is(err, context.DeadlineExceeded),
			errdefs.IsTimeout(err),
			errors.Is(err, context.Canceled):
			// No output within the window. A backend that reports its
			// own timeout type lands here too: an expired window is
			// never a dead session, and treating one as a failure
			// would freeze the tail of a silent process. A canceled
			// parent means the feed (or the entry) went away.
			if e.ctx.Err() != nil {
				return
			}
		case errors.Is(err, coresandbox.ErrSessionClosed):
			e.stop()
			return
		case errors.Is(err, coresandbox.ErrSequenceGap):
			// The backend dropped output the feed never read. The
			// cursor is dead for good, so freeze with what we have.
			e.markTruncated()
			e.stop()
			return
		default:
			telemetry.WarnErr(context.WithoutCancel(e.ctx),
				"sandbox: process feed read failed", err)
			e.stop()
			return
		}
		// A read that brought nothing is followed by a pause, so a
		// silent process is polled at processIdleGap rather than as
		// fast as the backend answers.
		if !progressed && !e.pause() {
			return
		}
	}
}

// pause waits out the idle gap between two reads of a process that
// produced nothing. It reports false when the entry (or the feed) went
// away while waiting.
func (e *processEntry) pause() bool {
	timer := time.NewTimer(processIdleGap)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-e.ctx.Done():
		return false
	}
}

// finish ends the entry from the close path: stop the drain, then read
// whatever the backend still buffers from the drain cursor. It runs
// once; later calls are no-ops.
func (e *processEntry) finish() {
	e.finishOn.Do(func() {
		e.cancel()
		e.stop()
		e.salvage()
	})
}

// salvage drains the remaining output with the entry's cursor. The
// process has stopped by the time a session closes, so a read that
// reports nothing left is the end of the salvage (the handle is about
// to be released either way).
func (e *processEntry) salvage() {
	ctx, cancel := context.WithTimeout(
		context.WithoutCancel(e.ctx), processSalvageWait)
	defer cancel()
	for {
		readCtx, readCancel := context.WithTimeout(ctx, processReadWindow)
		e.readMu.Lock()
		out, err := e.session.Read(readCtx, e.cursor(), processReadChunk)
		e.readMu.Unlock()
		readCancel()
		if err != nil {
			return
		}
		e.append(out.Chunks)
		e.advance(out.NextSeq)
		if out.EOF {
			e.queryExit(ctx)
			return
		}
		if len(out.Chunks) == 0 {
			return
		}
	}
}

// reap freezes an entry whose output reached EOF and asks the session
// for the exit status.
func (e *processEntry) reap() {
	e.stop()
	e.queryExit(context.WithoutCancel(e.ctx))
}

// queryExit records the exit status while the session can still answer:
// after Close the status is gone on the remote backend, so this runs
// before the handle is released.
func (e *processEntry) queryExit(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, processExitWait)
	defer cancel()
	exit, err := e.session.Wait(ctx)
	if err != nil {
		return
	}
	code := exit.Code
	e.mu.Lock()
	e.exitCode = &code
	e.exitReason = exit.Reason.String()
	e.mu.Unlock()
}

// stop freezes the entry and cancels its drain. Safe to call from the
// drainer, from eviction, from Close, and repeatedly.
func (e *processEntry) stop() {
	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		return
	}
	e.stopped = true
	e.stoppedAt = time.Now()
	e.mu.Unlock()
	e.cancel()
}

// append records output chunks in arrival order. Chunks from every
// stream share one tail: the card reads like a terminal, and the exit
// status is reported separately.
func (e *processEntry) append(chunks []coresandbox.OutputChunk) {
	if len(chunks) == 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, chunk := range chunks {
		e.appendLocked(chunk.Data)
	}
}

func (e *processEntry) appendLocked(data []byte) {
	if len(data) == 0 {
		return
	}
	if len(data) >= processTailBytes {
		e.tail = append(e.tail[:0], data[len(data)-processTailBytes:]...)
		e.truncated = true
		return
	}
	if len(e.tail)+len(data) > processTailBytes {
		drop := len(e.tail) + len(data) - processTailBytes
		e.tail = append(e.tail[:0], e.tail[drop:]...)
		e.truncated = true
	}
	e.tail = append(e.tail, data...)
}

// cursor returns the drain cursor: the seq of the next unread byte.
func (e *processEntry) cursor() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.seq
}

// advance records how far the drain cursor got.
func (e *processEntry) advance(seq int64) {
	e.mu.Lock()
	e.seq = seq
	e.mu.Unlock()
}

func (e *processEntry) markTruncated() {
	e.mu.Lock()
	e.truncated = true
	e.mu.Unlock()
}

func (e *processEntry) isRunning() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return !e.stopped
}

func (e *processEntry) isStopped() bool {
	return !e.isRunning()
}

// expired reports whether a stopped entry outlived the TTL. Callers
// hold the feed lock; running entries never expire.
func (e *processEntry) expired(now time.Time) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.stopped || e.stoppedAt.IsZero() {
		return false
	}
	return now.Sub(e.stoppedAt) > processExitedTTL
}

func (e *processEntry) snapshot() Process {
	e.mu.Lock()
	defer e.mu.Unlock()
	snap := Process{
		ID:             e.id,
		ConversationID: e.conversationID,
		Argv:           append([]string(nil), e.argv...),
		Workdir:        e.workdir,
		TTY:            e.tty,
		PID:            e.pid,
		StartedAt:      e.startedAt,
		Running:        !e.stopped,
		ExitReason:     e.exitReason,
		Tail:           string(e.tail),
		Truncated:      e.truncated,
		Seq:            e.seq,
	}
	if e.exitCode != nil {
		code := *e.exitCode
		snap.ExitCode = &code
	}
	return snap
}

// ProcessFeedFactory builds the opencraft.processes resource.
type ProcessFeedFactory struct{}

var _ resource.Factory = ProcessFeedFactory{}

func (ProcessFeedFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: ProcessFeedResourceKind,
		Impl: "local",
	}
}

func (ProcessFeedFactory) New(
	context.Context, resource.Input,
) (any, error) {
	return NewProcessFeed(), nil
}
