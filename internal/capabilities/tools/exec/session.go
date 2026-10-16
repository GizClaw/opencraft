package exec

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/sandbox"
	"github.com/GizClaw/flowcraft/core/tool"

	"github.com/GizClaw/opencraft/internal/capabilities/tools/toolargs"
)

// SessionName is the canonical exec_session tool name.
const SessionName = "exec_session"

// maxSessions bounds the number of concurrently live sessions. Sessions
// are removed on wait/close, but a model that forgets to close would
// otherwise grow the map without bound (each entry pins the process
// handle and its output buffer).
const maxSessions = 64

// maxReadBytes caps one session read so a single call cannot request
// an unbounded output window from the sandbox backend.
const maxReadBytes = 1 << 20 // 1 MiB

// SessionTool manages named sessions on one environment. It is safe
// for concurrent use.
type SessionTool struct {
	runner sandbox.Runner

	mu       sync.Mutex
	sessions map[string]sessionEntry
	// epochs hands out the identity of each slot. An id can be started
	// again while an earlier session's wait is still blocked, so "is
	// this slot still mine" is answered by the epoch the slot was
	// claimed with: the session value is not a stable identity to
	// compare (interfaces holding uncomparable values panic on ==), and
	// the id alone is not unique over time.
	epochs uint64
}

// sessionEntry is one named session slot. It exists in two states: a
// claim (session nil, the launch in flight) and a live session. The
// claim is what makes "one session per id" and the session cap atomic:
// both are decided under the lock, so two concurrent starts for one id,
// or one id too many, cannot both get past them while the launch runs.
type sessionEntry struct {
	session sandbox.Session
	epoch   uint64
}

// NewSession creates the exec_session tool.
func NewSession(runner sandbox.Runner) (*SessionTool, error) {
	if runner == nil {
		return nil, errdefs.Validationf(
			"exec_session: runner is required")
	}
	return &SessionTool{
		runner:   runner,
		sessions: make(map[string]sessionEntry),
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
// Execute implements tool.Tool. The tool result is a single text part;
// the tool has no multimodal output.
func (t *SessionTool) Execute(ctx context.Context, arguments string) (message.Content, error) {
	out, err := t.execute(ctx, arguments)
	if err != nil {
		return message.Content{}, err
	}
	return message.NewTextContent(out), nil
}

// execute renders the tool's text result.
func (t *SessionTool) execute(ctx context.Context, arguments string) (string, error) {
	var a args
	if err := toolargs.Decode(SessionName, arguments, nil, &a); err != nil {
		return "", err
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
	// Claim the slot before launching. The launch is slow — it forks a
	// process and may set up a pty — so checking first and registering
	// afterwards let two concurrent starts for one id both pass the
	// check, overwrite each other's handle, and leave one process
	// running with nothing pointing at it. Claiming first, then filling
	// the slot in, keeps the check and the claim in one critical
	// section; the quota counts claims for the same reason.
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
	t.epochs++
	epoch := t.epochs
	t.sessions[a.ProcessID] = sessionEntry{epoch: epoch}
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
		t.releaseClaim(a.ProcessID, epoch)
		return nil, err
	}
	t.mu.Lock()
	entry, ok := t.sessions[a.ProcessID]
	if !ok || entry.epoch != epoch {
		// The claim was closed while the launch was in flight: the
		// process belongs to nobody now, so it is stopped here rather
		// than registered under an id the caller already released.
		t.mu.Unlock()
		if closeErr := proc.Close(); closeErr != nil {
			return nil, closeErr
		}
		return nil, errdefs.Conflictf(
			"exec_session: process %q was closed while it was starting",
			a.ProcessID)
	}
	entry.session = proc
	t.sessions[a.ProcessID] = entry
	t.mu.Unlock()
	return map[string]any{"process_id": a.ProcessID, "started": true}, nil
}

// releaseClaim drops a claim whose launch failed, unless the claim was
// already replaced (a close followed by a fresh start of the same id).
func (t *SessionTool) releaseClaim(id string, epoch uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if entry, ok := t.sessions[id]; ok && entry.epoch == epoch {
		delete(t.sessions, id)
	}
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
	if maxBytes <= 0 || maxBytes > maxReadBytes {
		maxBytes = maxReadBytes
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
	proc, epoch, err := t.lookup(a.ProcessID)
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
	// explicit wait.) The slot is dropped only while it is still the one
	// this wait was called on: an id can be started again while the wait
	// blocks, and taking the new session's slot away would leave a live
	// process the caller can no longer reach.
	t.mu.Lock()
	if entry, ok := t.sessions[a.ProcessID]; ok && entry.epoch == epoch {
		delete(t.sessions, a.ProcessID)
	}
	t.mu.Unlock()
	return map[string]any{
		"process_id": a.ProcessID,
		"exit_code":  exit.Code,
		"reason":     exit.Reason.String(),
	}, nil
}

func (t *SessionTool) close(ctx context.Context, a args) (any, error) {
	t.mu.Lock()
	entry, ok := t.sessions[a.ProcessID]
	if ok {
		delete(t.sessions, a.ProcessID)
	}
	t.mu.Unlock()
	if !ok {
		return nil, errdefs.NotFoundf(
			"exec_session: unknown process %q", a.ProcessID)
	}
	if entry.session == nil {
		// A launch still in flight: the claim is gone, so the start
		// path stops the process it was given and reports the close.
		return map[string]bool{"closed": true}, nil
	}
	if err := entry.session.Close(); err != nil {
		return nil, err
	}
	return map[string]bool{"closed": true}, nil
}

// lookup returns the live session behind id together with the epoch of
// its slot, which is what the caller compares against before dropping
// the slot later.
func (t *SessionTool) lookup(id string) (sandbox.Session, uint64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry, ok := t.sessions[id]
	if !ok {
		return nil, 0, errdefs.NotFoundf(
			"exec_session: unknown process %q", id)
	}
	if entry.session == nil {
		return nil, 0, errdefs.Conflictf(
			"exec_session: process %q is still starting", id)
	}
	return entry.session, entry.epoch, nil
}

func (t *SessionTool) get(id string) (sandbox.Session, error) {
	proc, _, err := t.lookup(id)
	return proc, err
}

var _ tool.Tool = (*SessionTool)(nil)
