// Package execd defines the JSON-RPC protocol between opencraft and
// the execd process. The server wraps flowcraft's ProcessManager,
// ProcessSignaler, and ProcessEventSource primitives.
package execd

import (
	"encoding/json"
	"time"

	"github.com/GizClaw/flowcraft/sdk/sandbox"
)

// Method names.
const (
	MethodInitialize        = "initialize"
	MethodInitialized       = "initialized"
	MethodProcessStart      = "process/start"
	MethodProcessRead       = "process/read"
	MethodProcessWrite      = "process/write"
	MethodProcessSignal     = "process/signal"
	MethodProcessResize     = "process/resize"
	MethodProcessTerminate  = "process/terminate"
	MethodProcessOutput     = "process/output"
	MethodProcessExited     = "process/exited"
	MethodProcessClosed     = "process/closed"
	MethodProcessLag        = "process/lag"
	MethodEnvironmentInfo   = "environment/info"
	MethodEnvironmentStatus = "environment/status"
)

// RPCRequest is the JSON-RPC 2.0 request envelope.
type RPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Standard JSON-RPC error codes.
const (
	ErrParse    = -32700
	ErrInvalid  = -32600
	ErrMethod   = -32601
	ErrInternal = -32603
)

// Output streams.
const (
	StreamStdout = "stdout"
	StreamStderr = "stderr"
	StreamTTY    = "tty"
)

// InitializeParams is the handshake request.
type InitializeParams struct {
	ClientName      string  `json:"clientName"`
	ResumeSessionID *string `json:"resumeSessionId,omitempty"`
}

// InitializeResponse is the handshake response.
type InitializeResponse struct {
	SessionID string `json:"sessionId"`
}

// ExecParams starts a managed process. Policy is applied once at start.
type ExecParams struct {
	ProcessID string               `json:"processId"`
	Argv      []string             `json:"argv"`
	Cwd       string               `json:"cwd"`
	Env       map[string]string    `json:"env,omitempty"`
	TTY       bool                 `json:"tty"`
	PipeStdin bool                 `json:"pipeStdin,omitempty"`
	Timeout   time.Duration        `json:"timeout,omitempty"`
	Rows      int                  `json:"rows,omitempty"`
	Cols      int                  `json:"cols,omitempty"`
	Sandbox   *sandbox.ExecOptions `json:"sandbox,omitempty"`
}

// ExecResponse confirms a started process.
type ExecResponse struct {
	ProcessID string `json:"processId"`
}

// ReadParams pulls buffered output from an afterSeq cursor.
type ReadParams struct {
	ProcessID string `json:"processId"`
	AfterSeq  *int64 `json:"afterSeq,omitempty"`
	MaxBytes  *int   `json:"maxBytes,omitempty"`
	WaitMs    *int   `json:"waitMs,omitempty"`
}

// OutputChunk is one contiguous run of bytes from one stream.
type OutputChunk struct {
	Seq    int64  `json:"seq"`
	Stream string `json:"stream"`
	Data   []byte `json:"data"` // base64 on the wire
}

// ReadResponse is the result of process/read.
type ReadResponse struct {
	Chunks        []OutputChunk `json:"chunks"`
	NextSeq       int64         `json:"nextSeq"`
	EOF           bool          `json:"eof"`
	Exited        bool          `json:"exited"`
	ExitCode      *int32        `json:"exitCode,omitempty"`
	Closed        bool          `json:"closed"`
	Failure       *string       `json:"failure,omitempty"`
	SandboxDenied bool          `json:"sandboxDenied"`
}

// WriteParams writes to a process stdin. WriteID makes retries idempotent.
type WriteParams struct {
	ProcessID string `json:"processId"`
	Chunk     []byte `json:"chunk"` // base64 on the wire
	WriteID   string `json:"writeId"`
}

// WriteStatus values.
const (
	WriteAccepted    = "accepted"
	WriteUnknownProc = "unknown_process"
	WriteStdinClosed = "stdin_closed"
	WriteStarting    = "starting"
)

// WriteResponse is the result of process/write.
type WriteResponse struct {
	Status string `json:"status"`
}

// SignalParams sends a soft signal (P1: interrupt only).
type SignalParams struct {
	ProcessID string `json:"processId"`
	Signal    string `json:"signal"`
}

// ResizeParams resizes a pty session.
type ResizeParams struct {
	ProcessID string `json:"processId"`
	Rows      int    `json:"rows"`
	Cols      int    `json:"cols"`
}

// TerminateParams stops a process (SIGTERM -> grace -> SIGKILL).
type TerminateParams struct {
	ProcessID string `json:"processId"`
}

// TerminateResponse reports whether the process is still running.
type TerminateResponse struct {
	Running bool `json:"running"`
}

// OutputNotification is the process/output push event.
type OutputNotification struct {
	ProcessID string `json:"processId"`
	Seq       int64  `json:"seq"`
	Stream    string `json:"stream"`
	Data      []byte `json:"data"` // base64 on the wire
}

// ExitedNotification is the process/exited push event.
type ExitedNotification struct {
	ProcessID     string `json:"processId"`
	Seq           int64  `json:"seq"`
	ExitCode      int32  `json:"exitCode"`
	Reason        string `json:"reason"`
	SandboxDenied bool   `json:"sandboxDenied"`
}

// ClosedNotification is the process/closed push event.
type ClosedNotification struct {
	ProcessID string `json:"processId"`
	Seq       int64  `json:"seq"`
}

// LagNotification tells the client to pull with process/read(afterSeq).
type LagNotification struct {
	ProcessID string `json:"processId"`
	Seq       int64  `json:"seq"`
}

// Exit reason wire values.
const (
	ReasonExited     = "exited"
	ReasonSignaled   = "signaled"
	ReasonTerminated = "terminated"
	ReasonUnknown    = "unknown"
)

// EnvironmentInfoResponse reports static capabilities.
type EnvironmentInfoResponse struct {
	Shell        string   `json:"shell"`
	Cwd          string   `json:"cwd"`
	TmpDir       string   `json:"tmpdir"`
	Capabilities []string `json:"capabilities"`
}

// EnvironmentStatusResponse is the readiness probe.
type EnvironmentStatusResponse struct {
	Ready bool `json:"ready"`
}
