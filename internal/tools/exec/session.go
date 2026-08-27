package exec

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/sandbox"
	"github.com/GizClaw/flowcraft/core/tool"
)

// SessionName is the canonical exec_session tool name.
const SessionName = "exec_session"

// maxSessions bounds the number of concurrently live sessions. Sessions
// are removed on wait/close, but a model that forgets to close would
// otherwise grow the map without bound (each entry pins the process
// handle and its output buffer).
const maxSessions = 64

// SessionTool manages named sessions on one environment. It is safe
// for concurrent use.
type SessionTool struct {
	runner sandbox.Runner

	mu       sync.Mutex
	sessions map[string]sandbox.Session
}

// NewSession creates the exec_session tool.
func NewSession(runner sandbox.Runner) (*SessionTool, error) {
	if runner == nil {
		return nil, errdefs.Validationf(
			"exec_session: runner is required")
	}
	return &SessionTool{
		runner:   runner,
		sessions: make(map[string]sandbox.Session),
	}, nil
}

// MustNewSession panics on invalid construction; use in static wiring.
func MustNewSession(runner sandbox.Runner) *SessionTool {
	t, err := NewSession(runner)
	if err != nil {
		panic(err)
	}
	return t
}

// Definition implements tool.Tool.
func (t *SessionTool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		SessionName,
		"Manage a long-running shell session in the execution "+
			"environment. Actions: start (launch a session with argv), "+
			"read (pull output after an after_seq cursor), write (send "+
			"stdin bytes), signal (interrupt), resize (pty window), "+
			"terminate (stop the process), wait (block until exit), "+
			"close (release the session).",
		message.ToolProperty("action", "string",
			"start|read|write|signal|resize|terminate|wait|close (required)."),
		message.ToolProperty("process_id", "string",
			"Session identifier (required)."),
		message.ToolArrayProperty("argv",
			"Command and arguments (action=start).",
			message.Items("string")),
		message.ToolProperty("tty", "boolean",
			"Request a pseudo-terminal (action=start)."),
		message.ToolProperty("rows", "integer", "Pty rows (action=start/resize)."),
		message.ToolProperty("cols", "integer", "Pty cols (action=start/resize)."),
		message.ToolProperty("workdir", "string",
			"Working directory (action=start)."),
		message.ToolProperty("after_seq", "integer",
			"Output cursor (action=read)."),
		message.ToolProperty("max_bytes", "integer",
			"Maximum bytes to return (action=read, default 4096)."),
		message.ToolProperty("data", "string",
			"Bytes to write to stdin (action=write)."),
	).Required("action", "process_id").DisallowAdditionalProperties().Build()
}

// Metadata implements tool.ToolMetadata.
func (t *SessionTool) Metadata() tool.ToolMeta {
	return tool.ToolMeta{MutatesState: true}
}

type args struct {
	Action    string   `json:"action"`
	ProcessID string   `json:"process_id"`
	Argv      []string `json:"argv"`
	TTY       bool     `json:"tty"`
	Rows      int      `json:"rows"`
	Cols      int      `json:"cols"`
	Workdir   string   `json:"workdir"`
	AfterSeq  *int64   `json:"after_seq"`
	MaxBytes  *int     `json:"max_bytes"`
	Data      string   `json:"data"`
}

// Execute implements tool.Tool.
func (t *SessionTool) Execute(ctx context.Context, arguments string) (string, error) {
	var a args
	if err := json.Unmarshal([]byte(arguments), &a); err != nil {
		return "", errdefs.Validationf("exec_session: parse arguments: %v", err)
	}
	if a.Action == "" || a.ProcessID == "" {
		return "", errdefs.Validationf(
			"exec_session: action and process_id are required")
	}

	var result any
	var err error
	switch a.Action {
	case "start":
		result, err = t.start(ctx, a)
	case "read":
		result, err = t.read(ctx, a)
	case "write":
		result, err = t.write(ctx, a)
	case "signal":
		result, err = t.signal(ctx, a)
	case "resize":
		result, err = t.resize(ctx, a)
	case "terminate":
		result, err = t.terminate(ctx, a)
	case "wait":
		result, err = t.wait(ctx, a)
	case "close":
		result, err = t.close(ctx, a)
	default:
		return "", errdefs.Validationf(
			"exec_session: unknown action %q", a.Action)
	}
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return "", errdefs.Internalf("exec_session: encode result: %v", err)
	}
	return string(payload), nil
}

func (t *SessionTool) start(ctx context.Context, a args) (any, error) {
	t.mu.Lock()
	if _, exists := t.sessions[a.ProcessID]; exists {
		t.mu.Unlock()
		return nil, errdefs.Conflictf(
			"exec_session: process %q already exists", a.ProcessID)
	}
	if len(t.sessions) >= maxSessions {
		t.mu.Unlock()
		return nil, errdefs.Conflictf(
			"exec_session: too many live sessions (%d); close one before starting another",
			maxSessions)
	}
	t.mu.Unlock()
	proc, err := t.runner.Start(ctx, sandbox.SessionSpec{
		ID:   a.ProcessID,
		Argv: a.Argv,
		TTY:  a.TTY,
		Rows: a.Rows,
		Cols: a.Cols,
		Opts: sandbox.ExecOptions{WorkDir: a.Workdir},
	})
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.sessions[a.ProcessID] = proc
	t.mu.Unlock()
	return map[string]any{"process_id": a.ProcessID, "started": true}, nil
}

func (t *SessionTool) read(ctx context.Context, a args) (any, error) {
	proc, err := t.get(a.ProcessID)
	if err != nil {
		return nil, err
	}
	after := int64(0)
	if a.AfterSeq != nil {
		after = *a.AfterSeq
	}
	maxBytes := 4096
	if a.MaxBytes != nil {
		maxBytes = *a.MaxBytes
	}
	out, err := proc.Read(ctx, after, maxBytes)
	if err != nil {
		return nil, err
	}
	chunks := make([]map[string]any, 0, len(out.Chunks))
	for _, ch := range out.Chunks {
		chunks = append(chunks, map[string]any{
			"seq":    ch.Seq,
			"stream": ch.Stream.String(),
			"data":   string(ch.Data),
		})
	}
	return map[string]any{
		"process_id": a.ProcessID,
		"chunks":     chunks,
		"next_seq":   out.NextSeq,
		"eof":        out.EOF,
	}, nil
}

func (t *SessionTool) write(ctx context.Context, a args) (any, error) {
	proc, err := t.get(a.ProcessID)
	if err != nil {
		return nil, err
	}
	if err := proc.Write(ctx, []byte(a.Data)); err != nil {
		return nil, err
	}
	return map[string]bool{"written": true}, nil
}

func (t *SessionTool) signal(ctx context.Context, a args) (any, error) {
	proc, err := t.get(a.ProcessID)
	if err != nil {
		return nil, err
	}
	if err := proc.Signal(ctx, sandbox.SessionSignalInterrupt); err != nil {
		return nil, err
	}
	return map[string]bool{"signaled": true}, nil
}

func (t *SessionTool) resize(ctx context.Context, a args) (any, error) {
	proc, err := t.get(a.ProcessID)
	if err != nil {
		return nil, err
	}
	if err := proc.Resize(ctx, a.Rows, a.Cols); err != nil {
		return nil, err
	}
	return map[string]bool{"resized": true}, nil
}

func (t *SessionTool) terminate(ctx context.Context, a args) (any, error) {
	proc, err := t.get(a.ProcessID)
	if err != nil {
		return nil, err
	}
	if err := proc.Terminate(ctx); err != nil {
		return nil, err
	}
	return map[string]bool{"terminated": true}, nil
}

func (t *SessionTool) wait(ctx context.Context, a args) (any, error) {
	proc, err := t.get(a.ProcessID)
	if err != nil {
		return nil, err
	}
	exit, err := proc.Wait(ctx)
	if err != nil {
		return nil, err
	}
	// The session has ended: drop it so finished processes the model
	// never closes do not accumulate in the map. (Read's EOF flag is
	// not a reliable exit signal, so removal happens only here, on the
	// explicit wait.)
	t.mu.Lock()
	delete(t.sessions, a.ProcessID)
	t.mu.Unlock()
	return map[string]any{
		"process_id": a.ProcessID,
		"exit_code":  exit.Code,
		"reason":     exit.Reason.String(),
	}, nil
}

func (t *SessionTool) close(ctx context.Context, a args) (any, error) {
	t.mu.Lock()
	proc, ok := t.sessions[a.ProcessID]
	if ok {
		delete(t.sessions, a.ProcessID)
	}
	t.mu.Unlock()
	if !ok {
		return nil, errdefs.NotFoundf(
			"exec_session: unknown process %q", a.ProcessID)
	}
	if err := proc.Close(); err != nil {
		return nil, err
	}
	return map[string]bool{"closed": true}, nil
}

func (t *SessionTool) get(id string) (sandbox.Session, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	proc, ok := t.sessions[id]
	if !ok {
		return nil, errdefs.NotFoundf(
			"exec_session: unknown process %q", id)
	}
	return proc, nil
}

var _ tool.Tool = (*SessionTool)(nil)
