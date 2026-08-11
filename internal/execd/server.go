package execd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/sdk/errdefs"
	"github.com/GizClaw/flowcraft/sdk/sandbox"

	"github.com/rs/xid"
)

// Server serves the exec JSON-RPC protocol over an io.Reader/io.Writer
// pair (stdio or a unix-socket connection). One session per Serve call:
// processes started on a session belong to it and are terminated when
// the connection ends.
type Server struct {
	backend sandbox.ProcessManager
	in      io.Reader
	out     io.Writer
	mu      sync.Mutex
}

// New creates a Server over the given backend and transport.
func New(backend sandbox.ProcessManager, in io.Reader, out io.Writer) *Server {
	return &Server{backend: backend, in: in, out: out}
}

type processEntry struct {
	proc     sandbox.Process
	watcher  sandbox.ProcessWatcher
	writeIDs map[string]bool
	mu       sync.Mutex
	exit     *sandbox.ProcessExit
}

func (e *processEntry) noteWrite(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.writeIDs[id] {
		return false
	}
	e.writeIDs[id] = true
	return true
}

type session struct {
	id        string
	processes map[string]*processEntry
	mu        sync.Mutex
}

// Serve processes requests until EOF or ctx cancellation.
func (s *Server) Serve(ctx context.Context) error {
	sess := &session{
		id:        xid.New().String(),
		processes: make(map[string]*processEntry),
	}
	defer sess.closeAll()

	dec := json.NewDecoder(s.in)
	initialized := false
	for {
		var req RPCRequest
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if req.ID == nil {
			continue // notification
		}
		if !initialized && req.Method != MethodInitialize {
			s.respond(Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &RPCError{Code: ErrInvalid, Message: "not initialized"},
			})
			continue
		}
		if req.Method == MethodInitialize && initialized {
			s.respond(Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &RPCError{Code: ErrInvalid, Message: "already initialized"},
			})
			continue
		}
		resp := s.handle(ctx, sess, req)
		s.respond(resp)
		if req.Method == MethodInitialize && resp.Error == nil {
			initialized = true
		}
	}
}

func (s *Server) handle(
	ctx context.Context,
	sess *session,
	req RPCRequest,
) Response {
	var result any
	var rpcErr *RPCError
	switch req.Method {
	case MethodInitialize:
		result = InitializeResponse{SessionID: sess.id}
	case MethodInitialized:
		result = map[string]bool{"ok": true}
	case MethodProcessStart:
		var p ExecParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			rpcErr = invalid("process/start params: " + err.Error())
			break
		}
		result, rpcErr = s.start(ctx, sess, p)
	case MethodProcessRead:
		var p ReadParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			rpcErr = invalid("process/read params: " + err.Error())
			break
		}
		result, rpcErr = s.read(ctx, sess, p)
	case MethodProcessWrite:
		var p WriteParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			rpcErr = invalid("process/write params: " + err.Error())
			break
		}
		result, rpcErr = s.write(ctx, sess, p)
	case MethodProcessSignal:
		var p SignalParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			rpcErr = invalid("process/signal params: " + err.Error())
			break
		}
		result, rpcErr = s.signal(ctx, sess, p)
	case MethodProcessResize:
		var p ResizeParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			rpcErr = invalid("process/resize params: " + err.Error())
			break
		}
		result, rpcErr = s.resize(ctx, sess, p)
	case MethodProcessTerminate:
		var p TerminateParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			rpcErr = invalid("process/terminate params: " + err.Error())
			break
		}
		result, rpcErr = s.terminate(ctx, sess, p)
	case MethodEnvironmentInfo:
		result = EnvironmentInfoResponse{
			Shell:  "/bin/sh",
			Cwd:    "",
			TmpDir: os.TempDir(),
			Capabilities: []string{
				string(CapExec), string(CapSession),
				string(CapPTY), string(CapSignal),
			},
		}
	case MethodEnvironmentStatus:
		result = EnvironmentStatusResponse{Ready: true}
	default:
		rpcErr = &RPCError{Code: ErrMethod, Message: "method not found"}
	}

	resp := Response{JSONRPC: "2.0", ID: req.ID}
	if rpcErr != nil {
		resp.Error = rpcErr
		return resp
	}
	raw, err := json.Marshal(result)
	if err != nil {
		resp.Error = &RPCError{Code: ErrInternal, Message: err.Error()}
		return resp
	}
	resp.Result = raw
	return resp
}

func (s *Server) start(
	ctx context.Context,
	sess *session,
	p ExecParams,
) (any, *RPCError) {
	if p.ProcessID == "" || len(p.Argv) == 0 {
		return nil, invalid("process/start: processId and argv are required")
	}
	sess.mu.Lock()
	if _, exists := sess.processes[p.ProcessID]; exists {
		sess.mu.Unlock()
		return nil, &RPCError{Code: ErrInvalid, Message: "duplicate processId"}
	}
	sess.mu.Unlock()

	spec := sandbox.ProcessSpec{
		ID:   p.ProcessID,
		Argv: p.Argv,
		TTY:  p.TTY,
		Rows: p.Rows,
		Cols: p.Cols,
	}
	if p.Sandbox != nil {
		spec.Opts = *p.Sandbox
	}
	if p.Cwd != "" {
		spec.Opts.WorkDir = strings.TrimPrefix(p.Cwd, "file://")
	}
	if p.Timeout > 0 {
		spec.Opts.Timeout = p.Timeout
	}
	if p.Sandbox == nil && len(p.Env) > 0 {
		spec.Opts.Env = sandbox.EnvPolicy{Inject: p.Env}
	}

	proc, err := s.backend.Start(ctx, spec)
	if err != nil {
		return nil, internal(err)
	}
	entry := &processEntry{
		proc:     proc,
		writeIDs: make(map[string]bool),
	}
	sess.mu.Lock()
	sess.processes[p.ProcessID] = entry
	sess.mu.Unlock()

	if source, ok := sandbox.ProcessEventSourceOf(proc); ok {
		if watcher, err := source.Watch(ctx); err == nil {
			entry.watcher = watcher
			go s.pushEvents(p.ProcessID, entry, watcher)
		}
	}
	return ExecResponse{ProcessID: p.ProcessID}, nil
}

func (s *Server) read(
	ctx context.Context,
	sess *session,
	p ReadParams,
) (any, *RPCError) {
	entry, ok := sess.get(p.ProcessID)
	if !ok {
		return nil, &RPCError{Code: ErrInvalid, Message: "unknown process"}
	}
	afterSeq := int64(0)
	if p.AfterSeq != nil {
		afterSeq = *p.AfterSeq
	}
	maxBytes := 4096
	if p.MaxBytes != nil {
		maxBytes = *p.MaxBytes
	}
	out, err := entry.proc.Read(ctx, afterSeq, maxBytes)
	if err != nil {
		if errors.Is(err, sandbox.ErrSequenceGap) {
			return nil, &RPCError{Code: ErrInvalid, Message: "sequence gap; restart from cursor 0"}
		}
		return nil, internal(err)
	}
	chunks := make([]OutputChunk, 0, len(out.Chunks))
	for _, ch := range out.Chunks {
		chunks = append(chunks, OutputChunk{
			Seq: ch.Seq, Stream: ch.Stream.String(), Data: ch.Data,
		})
	}
	resp := ReadResponse{
		Chunks:  chunks,
		NextSeq: out.NextSeq,
		EOF:     out.EOF,
		Exited:  out.EOF,
	}
	entry.mu.Lock()
	if entry.exit != nil {
		code := int32(entry.exit.Code)
		resp.ExitCode = &code
	}
	entry.mu.Unlock()
	return resp, nil
}

func (s *Server) write(
	ctx context.Context,
	sess *session,
	p WriteParams,
) (any, *RPCError) {
	entry, ok := sess.get(p.ProcessID)
	if !ok {
		return WriteResponse{Status: WriteUnknownProc}, nil
	}
	if !entry.noteWrite(p.WriteID) {
		return WriteResponse{Status: WriteAccepted}, nil // idempotent
	}
	if err := entry.proc.Write(ctx, p.Chunk); err != nil {
		if errors.Is(err, sandbox.ErrProcessClosed) {
			return WriteResponse{Status: WriteStdinClosed}, nil
		}
		return nil, internal(err)
	}
	return WriteResponse{Status: WriteAccepted}, nil
}

func (s *Server) signal(
	ctx context.Context,
	sess *session,
	p SignalParams,
) (any, *RPCError) {
	if p.Signal != string(SignalInterrupt) {
		return nil, invalid("signal must be \"interrupt\"")
	}
	entry, ok := sess.get(p.ProcessID)
	if !ok {
		return nil, &RPCError{Code: ErrInvalid, Message: "unknown process"}
	}
	signaler, ok := sandbox.ProcessSignalerOf(entry.proc)
	if !ok {
		return nil, &RPCError{
			Code:    ErrInternal,
			Message: "backend does not support signals",
		}
	}
	if err := signaler.Signal(ctx, sandbox.ProcessSignalInterrupt); err != nil {
		return nil, internal(err)
	}
	return map[string]bool{"ok": true}, nil
}

func (s *Server) resize(
	ctx context.Context,
	sess *session,
	p ResizeParams,
) (any, *RPCError) {
	entry, ok := sess.get(p.ProcessID)
	if !ok {
		return nil, &RPCError{Code: ErrInvalid, Message: "unknown process"}
	}
	if err := entry.proc.Resize(ctx, p.Rows, p.Cols); err != nil {
		return nil, internal(err)
	}
	return map[string]bool{"ok": true}, nil
}

func (s *Server) terminate(
	ctx context.Context,
	sess *session,
	p TerminateParams,
) (any, *RPCError) {
	entry, ok := sess.get(p.ProcessID)
	if !ok {
		return nil, &RPCError{Code: ErrInvalid, Message: "unknown process"}
	}
	if err := entry.proc.Terminate(ctx); err != nil {
		return nil, internal(err)
	}
	// The exit event arrives asynchronously through the watcher; give it
	// a short window before reporting Running.
	deadline := time.Now().Add(time.Second)
	running := true
	for {
		entry.mu.Lock()
		running = entry.exit == nil
		entry.mu.Unlock()
		if !running || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return TerminateResponse{Running: running}, nil
}

// pushEvents forwards watcher events as notifications.
func (s *Server) pushEvents(
	processID string,
	entry *processEntry,
	watcher sandbox.ProcessWatcher,
) {
	defer watcher.Close()
	for ev := range watcher.Events() {
		switch ev.Type {
		case sandbox.ProcessEventOutput:
			s.notify(MethodProcessOutput, OutputNotification{
				ProcessID: processID,
				Seq:       ev.Seq,
				Stream:    ev.Stream.String(),
				Data:      ev.Data,
			})
		case sandbox.ProcessEventExited:
			reason := ReasonExited
			if ev.Exit != nil {
				entry.mu.Lock()
				entry.exit = ev.Exit
				entry.mu.Unlock()
				reason = exitReasonWire(ev.Exit.Reason)
			}
			code := int32(0)
			if ev.Exit != nil {
				code = int32(ev.Exit.Code)
			}
			s.notify(MethodProcessExited, ExitedNotification{
				ProcessID: processID,
				Seq:       ev.Seq,
				ExitCode:  code,
				Reason:    reason,
			})
			// The session stays readable via process/read (tail window),
			// but live push ends: emit closed right after exited.
			s.notify(MethodProcessClosed, ClosedNotification{
				ProcessID: processID, Seq: ev.Seq,
			})
			return
		case sandbox.ProcessEventClosed:
			s.notify(MethodProcessClosed, ClosedNotification{
				ProcessID: processID, Seq: ev.Seq,
			})
			return
		case sandbox.ProcessEventLag:
			s.notify(MethodProcessLag, LagNotification{
				ProcessID: processID, Seq: ev.Seq,
			})
			return
		}
	}
}

func (s *Server) respond(resp Response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = json.NewEncoder(s.out).Encode(resp)
}

func (s *Server) notify(method string, params any) {
	raw, err := json.Marshal(params)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = json.NewEncoder(s.out).Encode(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  json.RawMessage(raw),
	})
}

func (sess *session) get(id string) (*processEntry, bool) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	e, ok := sess.processes[id]
	return e, ok
}

func (sess *session) closeAll() {
	sess.mu.Lock()
	entries := make([]*processEntry, 0, len(sess.processes))
	for _, e := range sess.processes {
		entries = append(entries, e)
	}
	sess.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, e := range entries {
		if e.watcher != nil {
			_ = e.watcher.Close()
		}
		_ = e.proc.Terminate(ctx)
		_ = e.proc.Close()
	}
}

func exitReasonWire(r sandbox.ProcessExitReason) string {
	switch r {
	case sandbox.ProcessExited:
		return ReasonExited
	case sandbox.ProcessSignaled:
		return ReasonSignaled
	case sandbox.ProcessTerminated:
		return ReasonTerminated
	default:
		return ReasonUnknown
	}
}

func invalid(msg string) *RPCError {
	return &RPCError{Code: ErrInvalid, Message: msg}
}

func internal(err error) *RPCError {
	if err == nil {
		return nil
	}
	if errdefs.IsNotAvailable(err) {
		return &RPCError{Code: ErrInternal, Message: err.Error()}
	}
	return &RPCError{Code: ErrInternal, Message: fmt.Sprintf("%v", err)}
}
