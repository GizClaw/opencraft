package execd

import (
	"bufio"
	"context"
	"errors"
	"io"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GizClaw/flowcraft/core/sandbox"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/rs/xid"
)

const (
	// defaultWriteTimeout bounds one process/write RPC so a child that
	// never drains stdin cannot pin a handler forever.
	defaultWriteTimeout = 2 * time.Minute
	// defaultReapAfter is how long an exited process stays readable
	// before the reaper drops its entry. Clients normally release the
	// entry explicitly; the TTL is the safety net.
	defaultReapAfter = 5 * time.Minute
	// defaultReapInterval is how often the reaper sweeps the session.
	defaultReapInterval = time.Minute
	// frameQueueSize bounds the outbound frame queue.
	frameQueueSize = 256
	// maxQueuedNotifyBytes soft-caps the queued notification payload;
	// responses are never dropped and may exceed it briefly.
	maxQueuedNotifyBytes = 2 << 20
	// maxReadWaitMs caps one long-poll read window.
	maxReadWaitMs = 30_000
	// maxReadBytes caps one process/read response.
	maxReadBytes = 1 << 20
	// stopHandlerGrace bounds how long Serve waits for in-flight request
	// handlers after teardown interrupted them.
	stopHandlerGrace = 3 * time.Second
)

var errFrameWriterStopped = errors.New("execd: frame writer stopped")

// RunnerSet is the pair of sandbox runners bound to one workspace.
type RunnerSet struct {
	Confined   sandbox.Runner
	Unconfined sandbox.Runner
	// DefaultEnv is applied to a start request that carries no explicit
	// environment policy.
	DefaultEnv sandbox.EnvPolicy
}

// RunnerFactory builds the workspace-bound runners for one Bind.
type RunnerFactory func(
	ctx context.Context,
	workdir string,
	policy *SandboxPolicy,
) (RunnerSet, error)

// Server serves the execd protocol over one bidirectional channel.
//
// A connection starts unbound, receives Hello, then Bind. Every
// request after Hello runs on its own goroutine; a blocking read or
// wait must never stall another request. Outbound frames go through
// one writer goroutine, notifications are dropped under backpressure,
// and responses are never dropped.
type Server struct {
	in         io.Reader
	out        io.Writer
	newRunners RunnerFactory

	mu     sync.Mutex
	writer *frameWriter

	stateMu sync.RWMutex
	state   *boundState

	inflightMu sync.Mutex
	inflight   map[uint64]*inflightRequest

	handlers sync.WaitGroup

	stats     serverStats
	startedAt time.Time

	// Test hooks. Zero values fall back to the package defaults.
	writeTimeout time.Duration
	reapAfter    time.Duration
	reapInterval time.Duration
}

type boundState struct {
	workdir    string
	sess       *session
	runners    RunnerSet
	reapCancel context.CancelFunc
}

// inflightRequest is the registration a Cancel notification resolves
// to. It is a pointer so a finishing handler can tell its own
// registration from a newer request that reused the same id.
type inflightRequest struct {
	cancel context.CancelFunc
}

type serverStats struct {
	droppedNotifications atomic.Int64
	releasedProcesses    atomic.Int64
	reapedProcesses      atomic.Int64
}

// New creates a Server over the given transport. factory is required
// and is called once per Bind.
func New(factory RunnerFactory, in io.Reader, out io.Writer) *Server {
	return &Server{
		in:         in,
		out:        out,
		newRunners: factory,
		inflight:   make(map[uint64]*inflightRequest),
		startedAt:  time.Now(),
	}
}

// Stats returns the current counter snapshot.
func (s *Server) Stats() ServerStats {
	return ServerStats{
		DroppedNotifications: s.stats.droppedNotifications.Load(),
		ReleasedProcesses:    s.stats.releasedProcesses.Load(),
		ReapedProcesses:      s.stats.reapedProcesses.Load(),
	}
}

// ServerStats is a snapshot of the server's resilience counters.
type ServerStats struct {
	DroppedNotifications int64 `json:"droppedNotifications"`
	ReleasedProcesses    int64 `json:"releasedProcesses"`
	ReapedProcesses      int64 `json:"reapedProcesses"`
}

// frameWriter serialises outbound frames through one goroutine and a
// bounded queue: a slow peer backpressures the queue instead of holding
// a lock (or a handler goroutine) across a transport write.
type frameWriter struct {
	ctx         context.Context
	out         io.Writer
	queue       chan []byte
	closed      chan struct{}
	once        sync.Once
	queuedBytes atomic.Int64
	dropped     atomic.Int64
}

func newFrameWriter(out io.Writer, ctx context.Context) *frameWriter {
	if ctx == nil {
		ctx = context.Background()
	}
	return &frameWriter{
		ctx:    ctx,
		out:    out,
		queue:  make(chan []byte, frameQueueSize),
		closed: make(chan struct{}),
	}
}

func (w *frameWriter) run() {
	for {
		select {
		case frame := <-w.queue:
			w.write(frame)
		case <-w.closed:
			for {
				select {
				case frame := <-w.queue:
					w.write(frame)
				default:
					return
				}
			}
		}
	}
}

func (w *frameWriter) write(frame []byte) {
	defer w.queuedBytes.Add(-int64(len(frame)))
	for len(frame) > 0 {
		n, err := w.out.Write(frame)
		if err != nil {
			if !connectionClosed(err) {
				telemetry.WarnErr(context.Background(),
					"execd: write frame failed", err)
			}
			w.stop()
			return
		}
		if n <= 0 {
			w.stop()
			return
		}
		frame = frame[n:]
	}
}

func (w *frameWriter) stop() { w.once.Do(func() { close(w.closed) }) }

// enqueue delivers a frame that must not be dropped (a response).
func (w *frameWriter) enqueue(frame *Frame) error {
	raw, err := encodeFrame(frame)
	if err != nil {
		return err
	}
	select {
	case w.queue <- raw:
		w.queuedBytes.Add(int64(len(raw)))
		return nil
	case <-w.closed:
		return errFrameWriterStopped
	case <-w.ctx.Done():
		return w.ctx.Err()
	}
}

// offer delivers a frame that may be dropped (a notification). The
// authoritative state always rides on read/wait responses, so dropping
// a notification is safe; it is counted for diagnostics.
func (w *frameWriter) offer(frame *Frame) bool {
	raw, err := encodeFrame(frame)
	if err != nil {
		return false
	}
	if len(w.queue) >= frameQueueSize/2 ||
		w.queuedBytes.Load() >= maxQueuedNotifyBytes {
		w.dropped.Add(1)
		return false
	}
	select {
	case w.queue <- raw:
		w.queuedBytes.Add(int64(len(raw)))
		return true
	case <-w.closed:
		return false
	default:
		w.dropped.Add(1)
		return false
	}
}

func (w *frameWriter) depth() (frames int, bytes int64) {
	return len(w.queue), w.queuedBytes.Load()
}

// Serve processes frames until EOF, the channel closes, or ctx is
// canceled. On return every process the child owns has been stopped.
func (s *Server) Serve(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	writer := newFrameWriter(s.out, ctx)
	s.mu.Lock()
	s.writer = writer
	s.mu.Unlock()
	go writer.run()

	done := make(chan error, 1)
	go func() { done <- s.serveLoop(ctx) }()

	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
	}
	// Tear down first: stopping the processes is what interrupts the
	// handlers still parked in a long-poll read, so they can answer and
	// finish. Only then stop the writer, so their responses still go
	// through the single writer (and its queue drains) instead of racing
	// an inline write on the same transport.
	s.teardown()
	s.waitHandlers(stopHandlerGrace)
	writer.stop()
	s.mu.Lock()
	if s.writer == writer {
		s.writer = nil
	}
	s.mu.Unlock()
	return err
}

func (s *Server) serveLoop(ctx context.Context) error {
	decoder := bufio.NewReaderSize(s.in, 64<<10)
	hello := false
	for {
		frame, err := decodeFrame(decoder)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if notification := frame.GetNotification(); notification != nil {
			s.handleNotification(notification)
			continue
		}
		req := frame.GetRequest()
		if req == nil {
			continue
		}
		if !hello {
			if req.GetHello() == nil {
				s.respond(frame.GetId(),
					errorResponse(CodeInvalid, "hello must be the first request"))
				continue
			}
			resp := s.handle(ctx, req)
			s.respond(frame.GetId(), resp)
			if resp.GetError() == nil {
				hello = true
			}
			continue
		}
		// Register the request's cancellation *before* the handler
		// goroutine starts: a Cancel notification arrives on this same
		// connection right behind the request, and resolving it against
		// an empty table would silently drop the cancellation.
		reqCtx, cancel := requestContext(ctx, frame)
		registered := s.registerRequest(frame.GetId(), cancel)
		s.handlers.Add(1)
		go func(frame *Frame, req *Request, reqCtx context.Context) {
			defer s.handlers.Done()
			defer s.finishRequest(frame.GetId(), registered)
			s.dispatch(reqCtx, frame, req)
		}(frame, req, reqCtx)
	}
}

// requestContext applies the frame's relative deadline to the parent
// context.
func requestContext(
	parent context.Context, frame *Frame,
) (context.Context, context.CancelFunc) {
	if frame.GetDeadlineMs() > 0 {
		return context.WithTimeout(
			parent, time.Duration(frame.GetDeadlineMs())*time.Millisecond)
	}
	return context.WithCancel(parent)
}

// registerRequest records one in-flight request and returns the handle
// finishRequest must pass back.
func (s *Server) registerRequest(
	id uint64, cancel context.CancelFunc,
) *inflightRequest {
	request := &inflightRequest{cancel: cancel}
	s.inflightMu.Lock()
	s.inflight[id] = request
	s.inflightMu.Unlock()
	return request
}

// finishRequest unregisters the handler's own registration (never a
// newer request that reused the id) and releases its context.
func (s *Server) finishRequest(id uint64, request *inflightRequest) {
	request.cancel()
	s.inflightMu.Lock()
	if s.inflight[id] == request {
		delete(s.inflight, id)
	}
	s.inflightMu.Unlock()
}

// waitHandlers waits for the request handlers Serve started, bounded by
// grace so a wedged handler cannot delay process exit forever.
func (s *Server) waitHandlers(grace time.Duration) {
	done := make(chan struct{})
	go func() {
		s.handlers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(grace):
		telemetry.Warn(context.Background(),
			"execd: request handlers still running during teardown")
	}
}

// dispatch runs one request and sends the response.
func (s *Server) dispatch(reqCtx context.Context, frame *Frame, req *Request) {
	resp := handleSafely(func() *Response { return s.handle(reqCtx, req) })
	switch {
	case errors.Is(reqCtx.Err(), context.DeadlineExceeded):
		resp = errorResponse(CodeDeadlineExceeded, "request deadline exceeded")
	case errors.Is(reqCtx.Err(), context.Canceled):
		resp = errorResponse(CodeCanceled, "request canceled")
	}
	s.respond(frame.GetId(), resp)
}

func (s *Server) handleNotification(notification *Notification) {
	cancel := notification.GetCancel()
	if cancel == nil {
		return
	}
	s.inflightMu.Lock()
	request := s.inflight[cancel.GetRequestId()]
	s.inflightMu.Unlock()
	if request != nil {
		request.cancel()
	}
}

func (s *Server) handle(ctx context.Context, req *Request) *Response {
	switch {
	case req.GetHello() != nil:
		return s.handleHello(req.GetHello())
	case req.GetBind() != nil:
		return s.handleBind(ctx, req.GetBind())
	case req.GetUnbind() != nil:
		s.teardown()
		return &Response{Body: &Response_Ack{Ack: &Ack{Status: "ok"}}}
	case req.GetPing() != nil:
		return &Response{Body: &Response_PingOk{PingOk: &PingOk{
			UptimeMs: time.Since(s.startedAt).Milliseconds(),
			Version:  Build,
		}}}
	case req.GetDiagnostics() != nil:
		return s.handleDiagnostics()
	}
	st := s.bound()
	if st == nil {
		return errorResponse(CodeNotBound, "no workspace is bound")
	}
	switch {
	case req.GetStart() != nil:
		ok, errResp := s.start(ctx, st, req.GetStart())
		if errResp != nil {
			return errResp
		}
		return &Response{Body: &Response_StartOk{StartOk: ok}}
	case req.GetRead() != nil:
		ok, errResp := s.read(ctx, st, req.GetRead())
		if errResp != nil {
			return errResp
		}
		return &Response{Body: &Response_ReadOk{ReadOk: ok}}
	case req.GetWrite() != nil:
		status, errResp := s.write(ctx, st, req.GetWrite())
		if errResp != nil {
			return errResp
		}
		return &Response{Body: &Response_Ack{Ack: &Ack{Status: status}}}
	case req.GetCloseInput() != nil:
		if errResp := s.closeInput(ctx, st, req.GetCloseInput()); errResp != nil {
			return errResp
		}
		return &Response{Body: &Response_Ack{Ack: &Ack{Status: "ok"}}}
	case req.GetSignal() != nil:
		if errResp := s.signal(ctx, st, req.GetSignal()); errResp != nil {
			return errResp
		}
		return &Response{Body: &Response_Ack{Ack: &Ack{Status: "ok"}}}
	case req.GetResize() != nil:
		if errResp := s.resize(ctx, st, req.GetResize()); errResp != nil {
			return errResp
		}
		return &Response{Body: &Response_Ack{Ack: &Ack{Status: "ok"}}}
	case req.GetTerminate() != nil:
		ok, errResp := s.terminate(ctx, st, req.GetTerminate())
		if errResp != nil {
			return errResp
		}
		return &Response{Body: &Response_TerminateOk{TerminateOk: ok}}
	case req.GetRelease() != nil:
		ok, errResp := s.release(st, req.GetRelease())
		if errResp != nil {
			return errResp
		}
		return &Response{Body: &Response_ReleaseOk{ReleaseOk: ok}}
	case req.GetWait() != nil:
		ok, errResp := s.wait(ctx, st, req.GetWait())
		if errResp != nil {
			return errResp
		}
		return &Response{Body: &Response_WaitOk{WaitOk: ok}}
	default:
		return errorResponse(CodeMethodNotFound, "unknown request")
	}
}

func (s *Server) handleHello(hello *Hello) *Response {
	if hello.GetProtocolVersion() != ProtocolVersion {
		return errorResponse(CodeVersionMismatch,
			"child protocol %d, host protocol %d",
			ProtocolVersion, hello.GetProtocolVersion())
	}
	return &Response{Body: &Response_HelloOk{HelloOk: &HelloOk{
		ProtocolVersion: ProtocolVersion,
		Build:           Build,
		Capabilities:    []string{"exec", "session", "signal", "bind"},
		SessionId:       xid.New().String(),
	}}}
}

func (s *Server) handleDiagnostics() *Response {
	frames, bytes := 0, int64(0)
	s.mu.Lock()
	writer := s.writer
	s.mu.Unlock()
	if writer != nil {
		frames, bytes = writer.depth()
	}
	bound := false
	workdir := ""
	processes := 0
	if st := s.bound(); st != nil {
		bound = true
		workdir = st.workdir
		processes = st.sess.count()
	}
	return &Response{Body: &Response_DiagnosticsOk{DiagnosticsOk: &DiagnosticsOk{
		Version:              Build,
		ProtocolVersion:      ProtocolVersion,
		UptimeMs:             time.Since(s.startedAt).Milliseconds(),
		Bound:                bound,
		Workdir:              workdir,
		Processes:            int32(processes),
		Goroutines:           int32(runtime.NumGoroutine()),
		QueuedFrames:         int32(frames),
		QueuedBytes:          bytes,
		DroppedNotifications: s.stats.droppedNotifications.Load(),
		ReleasedProcesses:    s.stats.releasedProcesses.Load(),
		ReapedProcesses:      s.stats.reapedProcesses.Load(),
	}}}
}

// bound returns the current workspace state, or nil when unbound.
func (s *Server) bound() *boundState {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.state
}

func (s *Server) handleBind(ctx context.Context, bind *Bind) *Response {
	if bind.GetWorkdir() == "" {
		return errorResponse(CodeInvalid, "bind: workdir is required")
	}
	if s.newRunners == nil {
		return errorResponse(CodeInternal, "bind: no runner factory configured")
	}
	s.stateMu.Lock()
	if s.state != nil {
		s.stateMu.Unlock()
		return errorResponse(CodeAlreadyBound, "a workspace is already bound")
	}
	s.stateMu.Unlock()

	runners, err := s.newRunners(ctx, bind.GetWorkdir(), bind.GetPolicy())
	if err != nil {
		return errorResponse(CodeInternal, "bind: %v", err)
	}
	sess := &session{
		id:        xid.New().String(),
		processes: make(map[string]*processEntry),
		starting:  make(map[string]struct{}),
	}
	reapCtx, reapCancel := context.WithCancel(context.WithoutCancel(ctx))
	state := &boundState{
		workdir:    bind.GetWorkdir(),
		sess:       sess,
		runners:    runners,
		reapCancel: reapCancel,
	}
	s.stateMu.Lock()
	if s.state != nil {
		s.stateMu.Unlock()
		reapCancel()
		closeLog(ctx, "execd: close confined runner after bind race failed",
			runners.Confined)
		closeLog(ctx, "execd: close unconfined runner after bind race failed",
			runners.Unconfined)
		return errorResponse(CodeAlreadyBound, "a workspace is already bound")
	}
	s.state = state
	s.stateMu.Unlock()
	go s.reapLoop(reapCtx, sess)
	return &Response{Body: &Response_BindOk{BindOk: &BindOk{
		Capabilities: runnerCapabilities(runners),
	}}}
}

// runnerCapabilities renders the bound backend's session features as
// wire capability names. The Hello response advertises the static
// surface only; this is the one that tracks the platform backend.
func runnerCapabilities(runners RunnerSet) []string {
	caps := []string{"exec", "session", "bind"}
	if runners.Confined == nil {
		return caps
	}
	features := runners.Confined.Capabilities().Features
	if features.TTY {
		caps = append(caps, "pty")
	}
	if features.Signal {
		caps = append(caps, "signal")
	}
	// "events" stays out until the parent can consume it: core defines
	// the feature as "push event streams (Watch)" and the remote runner
	// has no Watch yet, so advertising it would make the surface lie.
	return caps
}

// teardown stops every process and releases the bound runners. It is
// safe to call when unbound.
func (s *Server) teardown() {
	s.stateMu.Lock()
	state := s.state
	s.state = nil
	s.stateMu.Unlock()
	if state == nil {
		return
	}
	if state.reapCancel != nil {
		state.reapCancel()
	}
	if state.sess != nil {
		s.closeAll(state.sess)
	}
	if state.runners.Confined != nil {
		telemetry.WarnErr(context.Background(),
			"execd: close confined runner failed", state.runners.Confined.Close())
	}
	if state.runners.Unconfined != nil {
		telemetry.WarnErr(context.Background(),
			"execd: close unconfined runner failed", state.runners.Unconfined.Close())
	}
}

func (s *Server) respond(id uint64, resp *Response) {
	s.writeFrame(&Frame{
		Id:   id,
		Body: &Frame_Response{Response: resp},
	}, false)
}

func (s *Server) notify(notification *Notification) {
	s.writeFrame(&Frame{
		Body: &Frame_Notification{Notification: notification},
	}, true)
}

func (s *Server) writeFrame(frame *Frame, bestEffort bool) {
	s.mu.Lock()
	writer := s.writer
	if writer == nil {
		// No Serve loop owns a writer (tests, embedded hosts): write
		// inline so the caller observes the result synchronously.
		defer s.mu.Unlock()
		raw, err := encodeFrame(frame)
		if err != nil {
			return
		}
		if _, err := s.out.Write(raw); err != nil && !connectionClosed(err) {
			telemetry.WarnErr(context.Background(), "execd: write frame failed", err)
		}
		return
	}
	s.mu.Unlock()

	if bestEffort {
		if !writer.offer(frame) {
			if s.stats.droppedNotifications.Add(1) == 1 {
				telemetry.Warn(context.Background(),
					"execd: dropping notifications under backpressure",
					otellog.Int64("execd.dropped_total",
						writer.dropped.Load()))
			}
		}
		return
	}
	if err := writer.enqueue(frame); err != nil &&
		!errors.Is(err, errFrameWriterStopped) {
		telemetry.WarnErr(context.Background(), "execd: enqueue response failed", err)
	}
}

// handleSafely turns a handler panic into an error response instead of
// killing the connection.
func handleSafely(fn func() *Response) (resp *Response) {
	defer func() {
		if r := recover(); r != nil {
			resp = errorResponse(CodeInternal, "handler panic: %v", r)
		}
	}()
	return fn()
}

// closeAll stops every process in the session. Each entry's actor is
// interrupted and waited for (bounded), and the processes are SIGKILLed
// through their session Close, so a wedged command cannot delay
// teardown.
func (s *Server) closeAll(sess *session) {
	sess.mu.Lock()
	entries := make([]*processEntry, 0, len(sess.processes))
	for _, entry := range sess.processes {
		entries = append(entries, entry)
	}
	sess.processes = make(map[string]*processEntry)
	sess.mu.Unlock()
	for _, entry := range entries {
		s.stopEntry(entry)
	}
}

func (sess *session) count() int {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return len(sess.processes)
}
