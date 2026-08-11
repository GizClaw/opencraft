package execd

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/sdk/sandbox"
)

// Capability names an Environment declares. Consumers check
// Capabilities before using a feature; environments never silently
// no-op a missing capability.
type Capability string

const (
	// CapExec runs one-shot commands (Environment.Exec).
	CapExec Capability = "exec"
	// CapSession starts long-running sessions (Environment.Start).
	CapSession Capability = "session"
	// CapPTY supports pseudo-terminals (TTY sessions and Resize).
	CapPTY Capability = "pty"
	// CapSignal delivers soft signals (Process.Signal).
	CapSignal Capability = "signal"
	// CapFiles exposes a filesystem (reserved for the fs/* protocol).
	CapFiles Capability = "files"
)

// Stream identifies one output stream.
type Stream string

const (
	Stdout Stream = "stdout"
	Stderr Stream = "stderr"
	TTY    Stream = "tty"
)

// Signal is a soft signal delivered to a session process.
type Signal string

const (
	SignalInterrupt Signal = "interrupt"
)

// ExitReason classifies how a process ended.
type ExitReason string

const (
	ExitReasonExited     ExitReason = "exited"
	ExitReasonSignaled   ExitReason = "signaled"
	ExitReasonTerminated ExitReason = "terminated"
	ExitReasonUnknown    ExitReason = "unknown"
)

// Request is a one-shot command execution.
type Request struct {
	Argv      []string
	WorkDir   string
	Env       sandbox.EnvPolicy
	Net       sandbox.NetPolicy
	Resources sandbox.ResourceLimits
	Timeout   time.Duration
	Stdin     []byte
}

// Result is the outcome of Request.
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// Spec starts a long-running session.
type Spec struct {
	ID    string
	Argv  []string
	TTY   bool
	Rows  int
	Cols  int
	Input Request
}

// Chunk is one contiguous run of output.
type Chunk struct {
	Seq    int64
	Stream Stream
	Data   []byte
}

// Output is one Read result.
type Output struct {
	NextSeq int64
	Chunks  []Chunk
	EOF     bool
}

// Exit is the final process outcome.
type Exit struct {
	Code   int
	Reason ExitReason
}

// Process is a long-running session handle. Optional capabilities are
// discovered by type assertion after checking the environment's
// Capabilities: [Signaler] for CapSignal, [Resizer] for CapPTY.
type Process interface {
	ID() string
	Read(ctx context.Context, afterSeq int64, maxBytes int) (Output, error)
	Write(ctx context.Context, data []byte) error
	Terminate(ctx context.Context) error
	Wait(ctx context.Context) (Exit, error)
	Close() error
}

// Signaler is implemented by processes that accept soft signals
// (environment declares CapSignal).
type Signaler interface {
	Signal(ctx context.Context, sig Signal) error
}

// Resizer is implemented by processes that accept pty resizes
// (environment declares CapPTY).
type Resizer interface {
	Resize(ctx context.Context, rows, cols int) error
}

// Environment is an execution backend: local sandbox or remote
// execd. Features are opt-in via Capabilities.
type Environment interface {
	ID() string
	Capabilities() []Capability
	// Exec runs a one-shot command. Requires CapExec.
	Exec(ctx context.Context, req Request) (Result, error)
	// Start launches a session. Requires CapSession.
	Start(ctx context.Context, spec Spec) (Process, error)
}

// EnvironmentManager is a registry of named environments. "local" is
// reserved for the in-process backend.
type EnvironmentManager struct {
	mu           sync.RWMutex
	environments map[string]Environment
}

// NewEnvironmentManager creates an empty manager.
func NewEnvironmentManager() *EnvironmentManager {
	return &EnvironmentManager{environments: make(map[string]Environment)}
}

// Register adds an environment. "local" is reserved.
func (m *EnvironmentManager) Register(env Environment) error {
	if env == nil {
		return fmt.Errorf("execd: nil environment")
	}
	id := env.ID()
	if id == "" || id == "local" {
		return fmt.Errorf("execd: environment id %q is reserved", id)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.environments[id]; exists {
		return fmt.Errorf("execd: environment %q already registered", id)
	}
	m.environments[id] = env
	return nil
}

// Get returns a named environment.
func (m *EnvironmentManager) Get(id string) (Environment, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	env, ok := m.environments[id]
	return env, ok
}

// Names returns sorted environment names.
func (m *EnvironmentManager) Names() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.environments))
	for id := range m.environments {
		names = append(names, id)
	}
	sort.Strings(names)
	return names
}

// Has reports whether env declares capability c.
func Has(env Environment, c Capability) bool {
	return slices.Contains(env.Capabilities(), c)
}

// toSandboxExecOptions converts a Request to flowcraft's sandbox
// options. Local and remote implementations share the policy surface.
func toSandboxExecOptions(req Request) sandbox.ExecOptions {
	return sandbox.ExecOptions{
		WorkDir:   req.WorkDir,
		Env:       req.Env,
		Net:       req.Net,
		Resources: req.Resources,
		Timeout:   req.Timeout,
	}
}
