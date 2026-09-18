package execd

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GizClaw/flowcraft/core/sandbox"
	"github.com/GizClaw/flowcraft/core/telemetry"
)

// processEntry tracks one sandbox session. Every operation on the
// session goes through entry.actor, so a process has exactly one
// in-flight operation at a time; entry.interrupt cancels that operation
// when terminate/release/cancel must get through.
type processEntry struct {
	proc     sandbox.Session
	watcher  sandbox.SessionWatcher
	writeIDs *recentWrites
	actor    *processActor
	// acting is set while the actor runs an operation, so a concurrent
	// read can degrade to a non-blocking peek.
	acting atomic.Bool

	mu            sync.Mutex
	exit          *sandbox.SessionExit
	exitedAt      time.Time
	currentCancel context.CancelFunc
}

// writeIDWindow is how many recent write ids one session remembers.
// The client mints a fresh id per write, so only a retry that lands
// inside this window needs to be deduplicated; a long-lived session
// must not grow one map entry per write forever.
const writeIDWindow = 1024

// recentWrites is a bounded FIFO set of write ids.
type recentWrites struct {
	buf  []string
	head int
	size int
	set  map[string]struct{}
}

func newRecentWrites() *recentWrites {
	return &recentWrites{
		buf: make([]string, writeIDWindow),
		set: make(map[string]struct{}, writeIDWindow),
	}
}

// note records id and reports whether it is new. An empty id is never
// deduplicated: it means the caller supplied no retry key.
func (w *recentWrites) note(id string) bool {
	if id == "" {
		return true
	}
	if _, ok := w.set[id]; ok {
		return false
	}
	if w.size == len(w.buf) {
		delete(w.set, w.buf[w.head])
	} else {
		w.size++
	}
	w.buf[w.head] = id
	w.set[id] = struct{}{}
	w.head = (w.head + 1) % len(w.buf)
	return true
}

func (e *processEntry) noteWrite(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.writeIDs.note(id)
}

func (e *processEntry) exitState() (*sandbox.SessionExit, time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.exit, e.exitedAt
}

type session struct {
	id        string
	processes map[string]*processEntry
	// starting reserves process IDs between the duplicate check and the
	// spawn, so concurrent start requests cannot both launch.
	starting map[string]struct{}
	mu       sync.Mutex
}

func (sess *session) get(id string) (*processEntry, bool) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	entry, ok := sess.processes[id]
	return entry, ok
}

// take removes and returns one entry; release, reaping, and teardown
// all go through it so an entry is never dropped twice.
func (sess *session) take(id string) (*processEntry, bool) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	entry, ok := sess.processes[id]
	if ok {
		delete(sess.processes, id)
	}
	return entry, ok
}

// expired lists processes that exited more than after ago.
func (sess *session) expired(now time.Time, after time.Duration) []string {
	sess.mu.Lock()
	entries := make(map[string]*processEntry, len(sess.processes))
	for id, entry := range sess.processes {
		entries[id] = entry
	}
	sess.mu.Unlock()
	ids := make([]string, 0, len(entries))
	for id, entry := range entries {
		if _, exitedAt := entry.exitState(); !exitedAt.IsZero() &&
			now.Sub(exitedAt) >= after {
			ids = append(ids, id)
		}
	}
	return ids
}

func (s *Server) start(
	ctx context.Context,
	st *boundState,
	p *Start,
) (*StartOk, *Response) {
	if p.GetProcessId() == "" || len(p.GetArgv()) == 0 {
		return nil, errorResponse(CodeInvalid,
			"start: processId and argv are required")
	}
	sess := st.sess
	sess.mu.Lock()
	if _, exists := sess.processes[p.GetProcessId()]; exists {
		sess.mu.Unlock()
		return nil, errorResponse(CodeInvalid, "duplicate processId")
	}
	if _, reserved := sess.starting[p.GetProcessId()]; reserved {
		sess.mu.Unlock()
		return nil, errorResponse(CodeInvalid, "duplicate processId")
	}
	sess.starting[p.GetProcessId()] = struct{}{}
	sess.mu.Unlock()
	defer func() {
		sess.mu.Lock()
		delete(sess.starting, p.GetProcessId())
		sess.mu.Unlock()
	}()

	opts, err := execOptionsFromProto(p.GetSandbox())
	if err != nil {
		return nil, errorResponse(CodeInvalid, "start: %v", err)
	}
	spec := sandbox.SessionSpec{
		ID:   p.GetProcessId(),
		Argv: p.GetArgv(),
		TTY:  p.GetTty(),
		Rows: int(p.GetRows()),
		Cols: int(p.GetCols()),
		Opts: opts,
	}
	// The working directory has exactly one source: the sandbox options
	// the client encodes from spec.Opts.WorkDir. Start.cwd stays as a
	// fallback for callers that predate them.
	if spec.Opts.WorkDir == "" && p.GetCwd() != "" {
		spec.Opts.WorkDir = strings.TrimPrefix(p.GetCwd(), "file://")
	}
	if p.GetTimeoutMs() > 0 {
		spec.Opts.Timeout = time.Duration(p.GetTimeoutMs()) * time.Millisecond
	}

	backend := st.runners.Confined
	if p.GetUnconfined() {
		if st.runners.Unconfined != nil {
			backend = st.runners.Unconfined
		}
		spec.Opts.Env = sandbox.EnvPolicy{}
	} else {
		if p.GetSandbox() == nil && len(p.GetEnv()) > 0 {
			spec.Opts.Env = sandbox.EnvPolicy{Inject: p.GetEnv()}
		}
		if spec.Opts.Env.Allow == nil && spec.Opts.Env.Inject == nil {
			spec.Opts.Env = st.runners.DefaultEnv
		}
	}

	proc, err := backend.Start(ctx, spec)
	if err != nil {
		return nil, errorResponse(CodeInternal, "start: %v", err)
	}
	entry := &processEntry{
		proc:     proc,
		writeIDs: newRecentWrites(),
	}
	entry.actor = newProcessActor(entry)
	if watcher, err := proc.Watch(context.WithoutCancel(ctx)); err == nil {
		entry.watcher = watcher
		go s.pushEvents(p.GetProcessId(), entry, watcher)
	}
	sess.mu.Lock()
	sess.processes[p.GetProcessId()] = entry
	sess.mu.Unlock()
	return &StartOk{Pid: int32(proc.PID())}, nil
}

func (s *Server) read(
	ctx context.Context,
	st *boundState,
	p *Read,
) (*ReadOk, *Response) {
	entry, ok := st.sess.get(p.GetProcessId())
	if !ok {
		return nil, errorResponse(CodeNotFound, "unknown process %q",
			p.GetProcessId())
	}
	// A read that lands while the actor is busy must not queue behind a
	// long-poll read or an outstanding wait: serve it as a non-blocking
	// peek and let the caller re-poll from the cursor it got back.
	if entry.busy() {
		value, errResp := s.readOp(ctx, entry, &Read{
			ProcessId: p.GetProcessId(),
			AfterSeq:  p.GetAfterSeq(),
			MaxBytes:  p.GetMaxBytes(),
		})
		if errResp != nil {
			return nil, errResp
		}
		return value.(*ReadOk), nil
	}
	value, errResp := entry.actor.submit(ctx, func(opCtx context.Context) (any, *Response) {
		return s.readOp(opCtx, entry, p)
	})
	if errResp != nil {
		return nil, errResp
	}
	return value.(*ReadOk), nil
}

func (s *Server) readOp(
	ctx context.Context,
	entry *processEntry,
	p *Read,
) (any, *Response) {
	afterSeq := p.GetAfterSeq()
	maxBytes := int(p.GetMaxBytes())
	if maxBytes <= 0 || maxBytes > maxReadBytes {
		maxBytes = maxReadBytes
	}
	wait := time.Duration(p.GetWaitMs()) * time.Millisecond
	if wait > maxReadWaitMs*time.Millisecond {
		wait = maxReadWaitMs * time.Millisecond
	}
	var (
		readCtx context.Context
		cancel  context.CancelFunc
	)
	if wait > 0 {
		readCtx, cancel = context.WithTimeout(ctx, wait)
	} else {
		// A non-blocking read: an expired context makes Read return
		// buffered output if there is any and a deadline error
		// otherwise.
		readCtx, cancel = context.WithCancel(ctx)
		cancel()
	}
	defer cancel()

	out, err := entry.proc.Read(readCtx, afterSeq, maxBytes)
	switch {
	case err == nil:
	case errors.Is(err, sandbox.ErrSequenceGap):
		return nil, errorResponse(CodeSequenceGap,
			"sequence gap; restart from cursor 0")
	case errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, context.Canceled):
		// The wait window elapsed, or an interrupt (terminate/release)
		// cancelled the poll. Either way report the current tail state
		// so the client can re-poll; a genuinely cancelled request is
		// mapped to CodeCanceled by the dispatch layer.
		out = sandbox.SessionOutput{NextSeq: afterSeq}
	default:
		return nil, errorResponse(CodeInternal, "read: %v", err)
	}

	chunks := make([]*Chunk, 0, len(out.Chunks))
	for _, chunk := range out.Chunks {
		chunks = append(chunks, &Chunk{
			Seq:    chunk.Seq,
			Stream: streamToProto(chunk.Stream),
			Data:   chunk.Data,
		})
	}
	exit, _ := entry.exitState()
	exited := out.EOF
	if entry.watcher != nil {
		exited = exit != nil
	}
	resp := &ReadOk{
		Chunks:  chunks,
		NextSeq: out.NextSeq,
		Eof:     out.EOF,
		Exited:  exited,
	}
	if exit != nil {
		resp.ExitCode = int32(exit.Code)
		resp.Reason = exitReasonToProto(exit.Reason)
	}
	return resp, nil
}

// Write statuses.
const (
	writeAccepted    = "accepted"
	writeUnknownProc = "unknown_process"
	writeStdinClosed = "stdin_closed"
)

func (s *Server) write(
	ctx context.Context,
	st *boundState,
	p *Write,
) (string, *Response) {
	entry, ok := st.sess.get(p.GetProcessId())
	if !ok {
		return writeUnknownProc, nil
	}
	if !entry.noteWrite(p.GetWriteId()) {
		return writeAccepted, nil // idempotent
	}
	value, errResp := entry.actor.submit(ctx, func(opCtx context.Context) (any, *Response) {
		timeout := s.writeTimeout
		if timeout <= 0 {
			timeout = defaultWriteTimeout
		}
		writeCtx, cancel := context.WithTimeout(opCtx, timeout)
		defer cancel()
		if err := entry.proc.Write(writeCtx, p.GetChunk()); err != nil {
			if errors.Is(err, sandbox.ErrSessionClosed) {
				return writeStdinClosed, nil
			}
			if resp := contextErrorResponse(writeCtx, "write", err); resp != nil {
				return nil, resp
			}
			return nil, errorResponse(CodeInternal, "write: %v", err)
		}
		return writeAccepted, nil
	})
	if errResp != nil {
		return "", errResp
	}
	return value.(string), nil
}

func (s *Server) closeInput(
	ctx context.Context, st *boundState, p *CloseInput,
) *Response {
	entry, ok := st.sess.get(p.GetProcessId())
	if !ok {
		return errorResponse(CodeNotFound, "unknown process %q", p.GetProcessId())
	}
	_, errResp := entry.actor.submit(ctx,
		func(opCtx context.Context) (any, *Response) {
			if err := entry.proc.CloseInput(); err != nil {
				if resp := contextErrorResponse(opCtx, "close_input", err); resp != nil {
					return nil, resp
				}
				return nil, errorResponse(CodeInternal, "close_input: %v", err)
			}
			return nil, nil
		})
	return errResp
}

func (s *Server) signal(
	ctx context.Context,
	st *boundState,
	p *Signal,
) *Response {
	coreSig, ok := signalToCore(p.GetSignal())
	if !ok {
		return errorResponse(CodeInvalid, "unsupported signal %d", p.GetSignal())
	}
	entry, ok := st.sess.get(p.GetProcessId())
	if !ok {
		return errorResponse(CodeNotFound, "unknown process %q", p.GetProcessId())
	}
	_, errResp := entry.actor.submit(ctx, func(opCtx context.Context) (any, *Response) {
		if err := entry.proc.Signal(opCtx, coreSig); err != nil {
			if resp := contextErrorResponse(opCtx, "signal", err); resp != nil {
				return nil, resp
			}
			return nil, errorResponse(CodeInternal, "signal: %v", err)
		}
		return nil, nil
	})
	return errResp
}

func (s *Server) resize(
	ctx context.Context,
	st *boundState,
	p *Resize,
) *Response {
	entry, ok := st.sess.get(p.GetProcessId())
	if !ok {
		return errorResponse(CodeNotFound, "unknown process %q", p.GetProcessId())
	}
	_, errResp := entry.actor.submit(ctx, func(opCtx context.Context) (any, *Response) {
		if err := entry.proc.Resize(opCtx, int(p.GetRows()), int(p.GetCols())); err != nil {
			if resp := contextErrorResponse(opCtx, "resize", err); resp != nil {
				return nil, resp
			}
			return nil, errorResponse(CodeInternal, "resize: %v", err)
		}
		return nil, nil
	})
	return errResp
}

func (s *Server) terminate(
	ctx context.Context,
	st *boundState,
	p *Terminate,
) (*TerminateOk, *Response) {
	entry, ok := st.sess.get(p.GetProcessId())
	if !ok {
		return nil, errorResponse(CodeNotFound, "unknown process %q",
			p.GetProcessId())
	}
	// Interrupt first: a long-poll read or a blocked wait must not keep
	// terminate queued behind it.
	entry.interrupt()
	value, errResp := entry.actor.submit(ctx,
		func(opCtx context.Context) (any, *Response) {
			// Force maps to the session's Close (SIGKILL to the whole
			// group, no grace window) — the same semantics a local
			// Session.Close has, which is what a cancelled tool gets.
			var err error
			if p.GetForce() {
				err = entry.proc.Close()
			} else {
				err = entry.proc.Terminate(opCtx)
			}
			if err != nil {
				if resp := contextErrorResponse(opCtx, "terminate", err); resp != nil {
					return nil, resp
				}
				return nil, errorResponse(CodeInternal, "terminate: %v", err)
			}
			// Running is a best-effort snapshot: the exit state is
			// published by the watcher, and no caller waits on it, so
			// terminate must not hold the actor's queue open for a
			// grace window of its own.
			exit, _ := entry.exitState()
			return &TerminateOk{Running: exit == nil}, nil
		})
	if errResp != nil {
		return nil, errResp
	}
	return value.(*TerminateOk), nil
}

func (s *Server) release(
	st *boundState,
	p *Release,
) (*ReleaseOk, *Response) {
	entry, ok := st.sess.take(p.GetProcessId())
	if !ok {
		return &ReleaseOk{Released: false}, nil
	}
	s.stopEntry(entry)
	s.stats.releasedProcesses.Add(1)
	return &ReleaseOk{Released: true}, nil
}

func (s *Server) wait(
	ctx context.Context,
	st *boundState,
	p *Wait,
) (*WaitOk, *Response) {
	entry, ok := st.sess.get(p.GetProcessId())
	if !ok {
		return nil, errorResponse(CodeNotFound, "unknown process %q",
			p.GetProcessId())
	}
	value, errResp := entry.actor.submit(ctx, func(opCtx context.Context) (any, *Response) {
		exit, err := entry.proc.Wait(opCtx)
		if err != nil {
			if resp := contextErrorResponse(opCtx, "wait", err); resp != nil {
				return nil, resp
			}
			return nil, errorResponse(CodeInternal, "wait: %v", err)
		}
		return &WaitOk{
			ExitCode: int32(exit.Code),
			Reason:   exitReasonToProto(exit.Reason),
		}, nil
	})
	if errResp != nil {
		return nil, errResp
	}
	return value.(*WaitOk), nil
}

// stopEntry interrupts the actor, waits (bounded) for it to drain, then
// stops the process and forgets the entry. Callers must have removed it
// from the session map already.
func (s *Server) stopEntry(entry *processEntry) {
	if entry == nil {
		return
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	if entry.actor != nil {
		entry.actor.stop(stopCtx)
	}
	if entry.watcher != nil {
		telemetry.WarnErr(context.Background(),
			"execd: close session watcher on release failed",
			entry.watcher.Close())
	}
	telemetry.WarnErr(context.Background(),
		"execd: close released session failed", entry.proc.Close())
}

// reapLoop drops exited entries the client never released, so a
// long-lived connection does not accumulate one output ring per
// command.
func (s *Server) reapLoop(ctx context.Context, sess *session) {
	interval := s.reapInterval
	if interval <= 0 {
		interval = defaultReapInterval
	}
	after := s.reapAfter
	if after <= 0 {
		after = defaultReapAfter
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			for _, id := range sess.expired(now, after) {
				entry, ok := sess.take(id)
				if !ok {
					continue
				}
				s.stopEntry(entry)
				s.stats.reapedProcesses.Add(1)
			}
		}
	}
}

// pushEvents forwards watcher events as notifications. Notifications
// are observability: the authoritative state also rides on read/wait
// responses, so they may be dropped under backpressure.
func (s *Server) pushEvents(
	processID string,
	entry *processEntry,
	watcher sandbox.SessionWatcher,
) {
	defer func() {
		telemetry.WarnErr(context.Background(),
			"execd: close session watcher failed", watcher.Close())
	}()
	for ev := range watcher.Events() {
		switch ev.Type {
		case sandbox.SessionEventOutput:
			s.notify(&Notification{Body: &Notification_Output{Output: &Output{
				ProcessId: processID,
				Seq:       ev.Seq,
				Stream:    streamToProto(ev.Stream),
				Data:      ev.Data,
			}}})
		case sandbox.SessionEventExited:
			entry.mu.Lock()
			if ev.Exit != nil {
				entry.exit = ev.Exit
			}
			entry.exitedAt = time.Now()
			exit := entry.exit
			entry.mu.Unlock()
			code := int32(0)
			reason := exitReasonToProto(sandbox.SessionExited)
			if exit != nil {
				code = int32(exit.Code)
				reason = exitReasonToProto(exit.Reason)
			}
			s.notify(&Notification{Body: &Notification_Exited{Exited: &Exited{
				ProcessId: processID,
				Seq:       ev.Seq,
				ExitCode:  code,
				Reason:    reason,
			}}})
			s.notify(&Notification{Body: &Notification_Closed{Closed: &Closed{
				ProcessId: processID,
				Seq:       ev.Seq,
			}}})
			return
		case sandbox.SessionEventClosed:
			s.notify(&Notification{Body: &Notification_Closed{Closed: &Closed{
				ProcessId: processID,
				Seq:       ev.Seq,
			}}})
			return
		case sandbox.SessionEventLag:
			s.notify(&Notification{Body: &Notification_Lag{Lag: &Lag{
				ProcessId: processID,
				Seq:       ev.Seq,
			}}})
			return
		}
	}
}
