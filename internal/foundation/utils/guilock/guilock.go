// Package guilock implements the GUI single-instance lock: at most one
// desktop process per state root, on every platform, with a diagnostic
// the user can act on.
//
// The lock is a non-blocking exclusive flock on <state root>/gui.lock
// (the same kernel primitives wslock uses for a workspace, so the
// kernel releases it when the holder dies for any reason). The holder
// records its pid, kind, start time and version in the file, which is
// what lets a rejected launch name the owner instead of vanishing
// silently.
//
// A rejected launch is not a dead end: before exiting it asks the
// holder to bring its window to the front over a per-root endpoint
// (<state root>/gui.sock; a named pipe on Windows, where no socket path
// exists). The double-click contract of a desktop app - opening the app
// again shows the running app - therefore rests on this protocol
// instead of on the shell's.
//
// The Wails SingleInstance options stay enabled next to this lock, but
// only as a second, opportunistic gate: they speak different protocols
// ($TMPDIR flock plus a distributed notification on macOS, a D-Bus name
// on Linux, a named mutex on Windows) that older builds of the same
// root lineage still use, so keeping them catches a mix of generations.
// The guarantee itself lives here, because it must not depend on
// $TMPDIR semantics, on a session bus (whose absence used to abort
// startup on Linux) or on a per-platform mutex check.
//
// Semantics: held by a live process -> HeldError, IsHeld true, the
// caller rejects the launch. The lock file cannot be taken or written
// (a filesystem oddity) -> a plain error, and the caller fails open with
// a warning, because a lock that cannot be taken must not keep a user
// out of their own app. Only IsHeld is a reason to refuse.
package guilock

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/foundation/utils/wslock"
)

// LockName is the lock file inside the state root.
const LockName = "gui.lock"

// Kind is recorded as the holder kind in the lock file.
const Kind = "gui"

// raiseTimeout bounds every step of the raise round trip: dial, write,
// handler-less readers and the reply. A rejected launch is on its way
// out; it must not wait on a holder that cannot answer.
const raiseTimeout = 2 * time.Second

// raiseReadLimit caps the raise request size. It carries a command line,
// nothing more.
const raiseReadLimit = 64 << 10

// raiseAck is the reply of a holder that ran its handler.
const raiseAck = "ok"

// Info describes the process holding the state root. It is wslock's
// holder record: diagnostics only, the lock state is the authority.
type Info = wslock.Info

// Launch describes one rejected launch: the arguments and working
// directory the holder logs (and nothing it acts on).
type Launch struct {
	// PID is the rejected process.
	PID int `json:"pid,omitempty"`
	// Args is its command line without argv[0] (like the shell's
	// second-instance data).
	Args []string `json:"args,omitempty"`
	// WorkingDir is where it was started.
	WorkingDir string `json:"working_dir,omitempty"`
}

// Lock is the held GUI lock of one state root. It stays valid for the
// lifetime of the process (the caller must not release it on a workspace
// switch: the lock is per state root, not per workspace) and doubles as
// the raise endpoint's server side.
type Lock struct {
	handle   *wslock.Handle
	root     string
	identity string
}

// Acquire takes the GUI lock of one state root without blocking.
//
//   - (lock, nil): this process owns the state root. A lock whose Owned
//     reports false means another handle of this same process already
//     holds it (tests, embedded hosts); the caller may proceed, and
//     Release is a no-op on that handle.
//   - (nil, err with IsHeld(err)): another live process owns the state
//     root. Ask it to come to the front (RequestRaise) and reject the
//     launch.
//   - (nil, err): the lock could not be taken or recorded. Fail open.
//
// identity names the state root in endpoint names that cannot carry a
// path (Windows pipe names, the socket fallback for over-long roots);
// callers pass the resolved single-instance id (config.Launch), which is
// canonical for the root. An empty identity falls back to the cleaned
// root path.
func Acquire(ctx context.Context, stateRoot, identity string) (*Lock, error) {
	if stateRoot == "" {
		return nil, errors.New("guilock: state root is empty")
	}
	handle, err := wslock.Acquire(ctx, filepath.Join(stateRoot, LockName), Kind)
	if err != nil {
		return nil, err
	}
	return &Lock{
		handle:   handle,
		root:     stateRoot,
		identity: identityOf(stateRoot, identity),
	}, nil
}

// IsHeld reports whether err means "another live process owns the state
// root" and returns the holder it recorded.
func IsHeld(err error) (Info, bool) {
	return wslock.IsHeld(err)
}

// Info names the holder as recorded in the lock file.
func (l *Lock) Info() Info {
	if l == nil {
		return Info{}
	}
	return l.handle.Info()
}

// Owned reports whether this handle owns the kernel lock; false means
// this process already held the root through another handle.
func (l *Lock) Owned() bool {
	return l != nil && l.handle.Owned()
}

// Release drops the lock. Processes normally never get here - the kernel
// releases it on exit - but a caller that returns from its main function
// should hand the root over instead of leaving a stale record behind.
func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	return l.handle.Release()
}

// Serve answers raise requests from rejected launches until ctx is
// cancelled, calling handler for each one on the accept goroutine (a
// handler that blocks delays the next request, not the app). It binds
// synchronously: once Serve returns without a warning, RequestRaise
// finds the endpoint. A failure here costs the raise, never the lock.
//
// One root has one endpoint and only the handle that owns the kernel
// lock serves it: Serve on a shared in-process handle (or twice on one
// handle) is a no-op.
func (l *Lock) Serve(ctx context.Context, handler func(Launch)) {
	if l == nil || l.handle == nil || !l.handle.Owned() || handler == nil {
		return
	}
	endpoint := endpointName(l.root, l.identity)
	listener, err := listenEndpoint(endpoint)
	if err != nil {
		telemetry.Warn(ctx, "guilock: state root cannot receive second launches",
			otellog.String("state_root", l.root),
			otellog.String("endpoint", endpoint),
			otellog.String("error", err.Error()))
		return
	}
	go func() {
		defer closeQuiet(ctx, "guilock: raise endpoint close failed", listener)
		defer removeEndpoint(ctx, endpoint)
		done := make(chan struct{})
		defer close(done)
		go func() {
			select {
			case <-ctx.Done():
				closeQuiet(ctx, "guilock: raise endpoint close failed", listener)
			case <-done:
			}
		}()
		for {
			conn, err := listener.Accept()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				telemetry.Warn(ctx, "guilock: raise endpoint accept failed",
					otellog.String("endpoint", endpoint),
					otellog.String("error", err.Error()))
				// A persistent accept error must not become a hot
				// loop; a raise is worth retrying briefly.
				time.Sleep(100 * time.Millisecond)
				continue
			}
			handleRaise(ctx, conn, handler)
		}
	}()
}

// RequestRaise asks the process holding the state root to bring its
// window to the front. It is best-effort by design: a holder that is
// still starting (no endpoint yet) or runs without a listener answers
// nothing, and the rejected launch reports that instead of waiting.
func RequestRaise(ctx context.Context, stateRoot, identity string, attempt Launch) error {
	if stateRoot == "" {
		return errors.New("guilock: state root is empty")
	}
	if attempt.PID == 0 {
		attempt.PID = os.Getpid()
	}
	payload, err := json.Marshal(attempt)
	if err != nil {
		return err
	}
	conn, err := dialEndpoint(ctx, endpointName(stateRoot, identityOf(stateRoot, identity)))
	if err != nil {
		return err
	}
	defer closeQuiet(ctx, "guilock: raise connection close failed", conn)
	deadline := time.Now().Add(raiseTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("guilock: set raise deadline: %w", err)
	}
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("guilock: send raise request: %w", err)
	}
	reply, err := io.ReadAll(io.LimitReader(conn, int64(len(raiseAck)+8)))
	if err != nil {
		return fmt.Errorf("guilock: read raise reply: %w", err)
	}
	if strings.TrimSpace(string(reply)) != raiseAck {
		return fmt.Errorf("guilock: raise reply %q", strings.TrimSpace(string(reply)))
	}
	return nil
}

// handleRaise decodes one raise request, hands it to the handler and
// acks it. Every failure is the rejected launch's problem: it prints the
// plain line instead of "its window was raised".
func handleRaise(ctx context.Context, conn net.Conn, handler func(Launch)) {
	defer closeQuiet(ctx, "guilock: raise connection close failed", conn)
	// A failed deadline only means this connection is already dead; the
	// sender times out and reports the raise as "did not happen".
	if err := conn.SetDeadline(time.Now().Add(raiseTimeout)); err != nil {
		return
	}
	// One newline-terminated line, not "until EOF": the sender waits for
	// the ack on the same connection.
	payload, err := bufio.NewReader(io.LimitReader(conn, raiseReadLimit)).ReadBytes('\n')
	if err != nil && len(payload) == 0 {
		return
	}
	var attempt Launch
	if err := json.Unmarshal(payload, &attempt); err != nil {
		return
	}
	handler(attempt)
	if _, err := io.WriteString(conn, raiseAck); err != nil {
		return
	}
}

// closeQuiet closes the raise endpoint or one of its connections. The
// error cannot change the outcome - the caller is already tearing down,
// or has answered the raise - but a surprising one is recorded rather
// than dropped.
func closeQuiet(ctx context.Context, msg string, closer io.Closer) {
	err := closer.Close()
	if err == nil || errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrClosed) {
		return
	}
	telemetry.WarnErr(ctx, msg, err)
}

// identityOf falls back to the cleaned root when the caller had no
// resolved id; two spellings of one root then at least share the root
// part of their identity.
func identityOf(stateRoot, identity string) string {
	if identity != "" {
		return identity
	}
	return filepath.Clean(stateRoot)
}

// endpointToken reduces an identity to a short, path- and name-safe
// component for endpoint names that carry no directory structure.
func endpointToken(identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:6])
}
