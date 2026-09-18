package execd

// This file owns the parent side of the exec child lifecycle: fork the
// same binary in execd mode, attach to the private channel, run the
// Hello handshake, and stop the child with EOF-then-kill semantics.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"
)

const (
	// stopGrace is how long stop waits for the child to exit after its
	// channel closes (EOF) before it kills the process.
	stopGrace = 3 * time.Second
	// handshakeTimeout bounds child startup + Hello.
	handshakeTimeout = 15 * time.Second
	// stderrTailBytes is how much recent child stderr is attached to a
	// startup failure.
	stderrTailBytes = 8 << 10
	// childStderrLineLimit bounds one forwarded child log line.
	childStderrLineLimit = 16 * 1024
)

// Launch forks the current executable in execd mode and returns a
// connected client plus a stop function. The child starts unbound; the
// caller binds a workspace with Client.Bind.
func Launch(ctx context.Context) (*Client, func(), error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, nil, fmt.Errorf("execd executable: %w", err)
	}
	return LaunchExe(ctx, executable)
}

// LaunchExe is Launch with an explicit executable path, for tests and
// embedded hosts.
func LaunchExe(ctx context.Context, executable string) (*Client, func(), error) {
	// Reap children an earlier host left behind before adding new ones.
	// The channel-EOF path covers a clean death; this covers a wedged
	// child whose parent was killed before it could clean up.
	sweepOrphansOnce(ctx)
	plan, err := prepareChannel()
	if err != nil {
		return nil, nil, err
	}
	nonce := randomChannelSuffix()
	args := append([]string{"execd"}, plan.args...)
	args = append(args, "-execd-nonce", nonce)
	// The child deliberately does not inherit the launch context:
	// exec.CommandContext kills the process the moment ctx is done, and
	// every caller passes a context that ends long before the child
	// should (the pool's prewarm helper returns and cancels, the
	// assembly RPC finishes, a retry window lapses). The child's
	// lifetime is owned by stop below: EOF on the channel, then a plain
	// kill. The launch context only bounds the handshake.
	cmd := exec.Command(executable, args...)
	configureChildProcess(cmd)
	if len(plan.extraFiles) > 0 {
		cmd.ExtraFiles = plan.extraFiles
	}

	// The child's stderr carries its own diagnostics (it installs a
	// stderr-only log sink). The pipe is explicit rather than
	// cmd.StderrPipe(): Wait closes StderrPipe's parent end the moment
	// the child is reaped, which can drop the last lines.
	stderr, stderrWrite, err := os.Pipe()
	if err != nil {
		if plan.close != nil {
			plan.close()
		}
		return nil, nil, fmt.Errorf("execd stderr pipe: %w", err)
	}
	cmd.Stderr = stderrWrite
	if err := cmd.Start(); err != nil {
		closeLog(ctx, "execd: close stderr reader after start failure", stderr)
		closeLog(ctx, "execd: close stderr writer after start failure", stderrWrite)
		if plan.close != nil {
			plan.close()
		}
		return nil, nil, fmt.Errorf("execd launch: %w", err)
	}
	// The parent must not hold the child's inherited descriptors:
	// keeping the socketpair end open would hide the child's EOF, and
	// keeping the stderr write end open would delay the drain's EOF.
	for _, file := range plan.extraFiles {
		closeLog(ctx, "execd: close inherited channel end failed", file)
	}
	closeLog(ctx, "execd: close parent stderr writer failed", stderrWrite)
	unrecordChild := recordChild(ctx, cmd.Process.Pid, nonce)

	tail := newStderrTail(stderrTailBytes)
	stderrForwarded := forwardChildStderrAsync(ctx, cmd.Process.Pid, stderr, tail)

	// The handshake honours the caller's context (plus the package
	// timeout): a cancelled launch must fail fast and kill the child it
	// just forked, which is what lets Pool.Close abort an in-flight
	// pre-warm. The child's lifetime is still detached from ctx - stop()
	// owns it once the handshake succeeds.
	handshakeCtx, cancelHandshake := context.WithTimeout(ctx, handshakeTimeout)
	conn, err := plan.attach(handshakeCtx)
	var client *Client
	if err == nil {
		client, err = Dial(handshakeCtx, conn)
	}
	cancelHandshake()
	if err != nil {
		telemetry.WarnErr(ctx, "execd: child handshake failed", err)
		unrecordChild()
		telemetry.WarnErr(ctx, "execd: kill child after handshake failure failed",
			cmd.Process.Kill())
		waitForExit(cmd, stopGrace)
		drainStderr(stderr, stderrForwarded, stopGrace)
		if plan.close != nil {
			plan.close()
		}
		return nil, nil, fmt.Errorf("execd: child start failed: %w%s",
			err, stderrTailSuffix(tail))
	}

	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			// Closing the channel is the graceful shutdown: the child's
			// read loop sees EOF, kills every process group it owns, and
			// exits. SIGTERM is not needed (and is not deliverable on
			// Windows), so the escalation is a plain kill.
			telemetry.WarnErr(ctx, "execd: close child client failed", client.Close())
			unrecordChild()
			waitCtx, cancelWait := context.WithTimeout(
				context.WithoutCancel(ctx), stopGrace)
			telemetry.WarnErr(ctx, "execd: wait client read loop failed",
				client.WaitReadLoop(waitCtx))
			cancelWait()
			if !waitForExit(cmd, stopGrace) {
				telemetry.WarnErr(ctx, "execd: kill child failed", cmd.Process.Kill())
			}
			drainStderr(stderr, stderrForwarded, stopGrace)
			if plan.close != nil {
				plan.close()
			}
		})
	}
	return client, stop, nil
}

// waitForExit waits up to grace for the child to exit and reports
// whether it did.
func waitForExit(cmd *exec.Cmd, grace time.Duration) bool {
	waited := make(chan struct{})
	go func() {
		if err := cmd.Wait(); err != nil {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				telemetry.WarnErr(context.Background(),
					"execd: wait child failed", err)
			}
		}
		close(waited)
	}()
	select {
	case <-waited:
		return true
	case <-time.After(grace):
		return false
	}
}

func drainStderr(
	stderr *os.File,
	forwarded <-chan struct{},
	grace time.Duration,
) {
	select {
	case <-forwarded:
	case <-time.After(grace):
		telemetry.Warn(context.Background(),
			"execd: child stderr still draining during stop")
		closeLog(context.Background(), "execd: close child stderr reader failed",
			stderr)
	}
}

// stderrTail keeps the most recent child stderr bytes so a startup
// failure can say what the child complained about instead of only
// "connection closed".
type stderrTail struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func newStderrTail(max int) *stderrTail {
	return &stderrTail{max: max}
}

// add appends text, trimming to the most recent max bytes.
func (t *stderrTail) add(text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, text...)
	if len(t.buf) > t.max {
		t.buf = append(t.buf[:0:0], t.buf[len(t.buf)-t.max:]...)
	}
}

func (t *stderrTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

func stderrTailSuffix(tail *stderrTail) string {
	if tail == nil {
		return ""
	}
	text := tail.String()
	if text == "" {
		return ""
	}
	return "\nchild stderr:\n" + text
}

// forwardChildStderr copies the child's stderr into the host log, one
// record per line, and mirrors it into tail.
func forwardChildStderr(
	ctx context.Context, pid int, stderr io.Reader, tail *stderrTail,
) {
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 0, 4096), childStderrLineLimit)
	for scanner.Scan() {
		line := scanner.Text()
		if tail != nil {
			tail.add(line + "\n")
		}
		telemetry.Warn(ctx, "execd stderr",
			otellog.Int("execd.pid", pid),
			otellog.String("execd.line", line))
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, os.ErrClosed) {
		telemetry.WarnErr(ctx, "execd: drain child stderr failed", err)
	}
}

func forwardChildStderrAsync(
	ctx context.Context, pid int, stderr *os.File, tail *stderrTail,
) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if err := stderr.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
				telemetry.WarnErr(ctx, "execd: close child stderr reader failed", err)
			}
		}()
		forwardChildStderr(ctx, pid, stderr, tail)
	}()
	return done
}
