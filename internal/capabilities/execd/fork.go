package execd

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"
)

// stopGrace is how long stop waits for the execd child to shut down
// gracefully (and terminate its sessions) before SIGKILL.
const stopGrace = 3 * time.Second

// signalledExit reports whether err is the exit status of a process
// that was killed by a signal, which is what stop asks for when it
// terminates a child. exec.ExitError.ExitCode documents -1 for that
// case, so the check stays portable.
func signalledExit(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == -1
}

// childStderrLineLimit bounds one forwarded child log line, so a child
// that never emits a newline cannot grow host memory without bound.
const childStderrLineLimit = 16 * 1024

// forwardChildStderr copies the child's stderr into the host log, one
// record per line, tagged with the child's pid and socket. The child is
// forked before the desktop installs its log pipeline and cannot reach
// the application log on its own, so this is where its warnings become
// visible.
func forwardChildStderr(
	ctx context.Context, pid int, sock string, stderr io.Reader,
) {
	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 0, 4096), childStderrLineLimit)
	for sc.Scan() {
		telemetry.Warn(ctx, "execd stderr",
			otellog.Int("execd.pid", pid),
			otellog.String("execd.socket", sock),
			otellog.String("execd.line", sc.Text()))
	}
	// The read end is closed once the drain is over (see
	// forwardChildStderrAsync), which can race the last read; that end of
	// the stream is expected, not a failure.
	if err := sc.Err(); err != nil && !errors.Is(err, os.ErrClosed) {
		telemetry.WarnErr(ctx, "execd: drain child stderr failed", err)
	}
}

// forwardChildStderrAsync drains a child's stderr in the background and
// returns a channel closed once it has. The read end is closed with the
// drain, so the channel also means the descriptor is gone.
func forwardChildStderrAsync(
	ctx context.Context, pid int, sock string, stderr *os.File,
) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if err := stderr.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
				telemetry.WarnErr(ctx, "execd: close child stderr reader failed", err)
			}
		}()
		forwardChildStderr(ctx, pid, sock, stderr)
	}()
	return done
}

// Launch forks the current executable in execd mode and dials its
// unix socket. policyJSON is the parent's serialized sandbox policy
// (writable paths + environment policy); it is forwarded to the child
// through a 0600 temp file (-sandbox-policy-file) rather than a
// command-line argument, so injected env values never show up in `ps`.
// The returned stop function terminates the child and removes the
// socket. The returned socket path is the unix socket the child
// listens on (useful for cleanup verification and status).
func Launch(
	ctx context.Context,
	workDir, policyJSON string,
) (*Client, string, func(), error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, "", nil, fmt.Errorf("execd executable: %w", err)
	}
	return LaunchExe(ctx, workDir, executable, policyJSON)
}

// LaunchExe forks the given executable in execd mode and dials
// its unix socket. Launch uses os.Executable; LaunchExe exists for
// tests and embedded hosts that know a different binary path.
// policyJSON is optional ("" applies an empty environment policy).
func LaunchExe(
	ctx context.Context,
	workDir, executable, policyJSON string,
) (*Client, string, func(), error) {
	sock, err := execdSocketPath()
	if err != nil {
		return nil, "", nil, fmt.Errorf("execd socket path: %w", err)
	}
	// One sweep per process, before this launch adds its own socket.
	sweepStaleSocketsOnce(ctx)
	if err := os.Remove(sock); err != nil && !os.IsNotExist(err) {
		telemetry.WarnErr(ctx, "execd: remove stale socket failed", err)
	}

	var policyFile string
	if policyJSON != "" {
		f, err := os.CreateTemp("", "opencraft-policy-*.json")
		if err != nil {
			return nil, sock, nil, fmt.Errorf("execd policy file: %w", err)
		}
		policyFile = f.Name()
		if err := f.Chmod(0o600); err != nil {
			telemetry.WarnErr(ctx, "execd: close policy file after chmod failure",
				f.Close())
			telemetry.WarnErr(ctx, "execd: remove policy file after chmod failure",
				os.Remove(policyFile))
			return nil, sock, nil, fmt.Errorf("execd policy file mode: %w", err)
		}
		if _, err := f.WriteString(policyJSON); err != nil {
			telemetry.WarnErr(ctx, "execd: close policy file after write failure",
				f.Close())
			telemetry.WarnErr(ctx, "execd: remove policy file after write failure",
				os.Remove(policyFile))
			return nil, sock, nil, fmt.Errorf("execd policy write: %w", err)
		}
		if err := f.Close(); err != nil {
			telemetry.WarnErr(ctx, "execd: remove policy file after close failure",
				os.Remove(policyFile))
			return nil, sock, nil, fmt.Errorf("execd policy close: %w", err)
		}
	}

	// The child watches its parent: if this process dies without
	// running stop (SIGKILL, crash), the child self-terminates and
	// removes the socket instead of leaking.
	args := []string{
		"execd", "-listen", sock, "-workdir", workDir,
		"-parent-pid", strconv.Itoa(os.Getpid()),
	}
	if policyFile != "" {
		args = append(args, "-sandbox-policy-file", policyFile)
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	// The child's stderr carries its own diagnostics (it has a log
	// pipeline that writes warnings there, see execd_main.go), and the
	// forwarder below copies them into the host log instead of dropping
	// them on the application's stderr.
	//
	// The pipe is explicit rather than cmd.StderrPipe(): Wait closes
	// StderrPipe's parent end the moment the child is reaped, which can
	// drop the lines the child wrote last, before the forwarder has read
	// them. With a read end of our own the forwarder drains everything up
	// to EOF, and stop can wait for it below.
	stderr, stderrWrite, err := os.Pipe()
	if err != nil {
		if policyFile != "" {
			telemetry.WarnErr(ctx, "execd: remove policy file after stderr pipe failure",
				os.Remove(policyFile))
		}
		return nil, sock, nil, fmt.Errorf("execd stderr pipe: %w", err)
	}
	cmd.Stderr = stderrWrite
	if err := cmd.Start(); err != nil {
		// Start closes the parent's copy of the write end; the read end
		// is ours to release.
		telemetry.WarnErr(ctx, "execd: close stderr reader after start failure",
			stderr.Close())
		if err := stderrWrite.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			telemetry.WarnErr(ctx, "execd: close stderr writer after start failure",
				err)
		}
		if policyFile != "" {
			telemetry.WarnErr(ctx, "execd: remove policy file after start failure",
				os.Remove(policyFile))
		}
		return nil, sock, nil, fmt.Errorf("execd launch: %w", err)
	}
	// The child holds its own descriptor for the write end, so the
	// parent's copy has to go the way StderrPipe's would: while any
	// writer is open the read end never reaches EOF, and the drain would
	// only end when stop force-closes the reader after its grace period —
	// dropping everything the child wrote in the meantime, emitting a
	// last line after stop returned, and leaking one descriptor per
	// child.
	if err := stderrWrite.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		telemetry.WarnErr(ctx,
			"execd: close parent stderr writer after start failed", err)
	}
	stderrForwarded := forwardChildStderrAsync(ctx, cmd.Process.Pid, sock, stderr)
	var dialed *Client
	stop := func() {
		// Close the client first: the child's Serve loop returns on
		// EOF and runs its in-process session cleanup. On unix this is
		// belt-and-braces alongside SIGTERM; on Windows it is the
		// graceful shutdown trigger (SIGTERM is not deliverable there).
		if dialed != nil {
			telemetry.WarnErr(ctx, "execd: close client during stop failed",
				dialed.Close())
			// The read loop ends with the connection. Waiting for it here
			// keeps the socket removal below after the client's last
			// record instead of racing it.
			waitCtx, cancelWait := context.WithTimeout(
				context.WithoutCancel(ctx), stopGrace)
			telemetry.WarnErr(ctx, "execd: wait client read loop during stop failed",
				dialed.WaitReadLoop(waitCtx))
			cancelWait()
		}
		// SIGTERM on unix, no-op on Windows (EOF close above).
		telemetry.WarnErr(ctx, "execd: terminate child during stop failed",
			terminateExecd(cmd))
		waited := make(chan struct{})
		go func() {
			// Terminating the child is the point of stop: a status of
			// "killed by the signal we sent" is the expected outcome, so
			// only an unexpected failure is worth a warning.
			if err := cmd.Wait(); err != nil && !signalledExit(err) {
				telemetry.WarnErr(ctx, "execd: wait child during stop failed", err)
			}
			close(waited)
		}()
		select {
		case <-waited:
		case <-time.After(stopGrace):
			telemetry.WarnErr(ctx, "execd: kill child during stop failed",
				cmd.Process.Kill())
			<-waited
		}
		// The forwarder ends at EOF, which the child's exit produces, so
		// waiting here keeps the warnings a child writes while shutting
		// down inside this stop instead of in whatever log window runs
		// next (the next session, or the next test's capture). A
		// grandchild that inherited the write end can hold EOF back, so
		// the wait is bounded and closing the read end ends the drain.
		select {
		case <-stderrForwarded:
		case <-time.After(stopGrace):
			telemetry.Warn(ctx,
				"execd: child stderr still draining during stop")
			if err := stderr.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
				telemetry.WarnErr(ctx,
					"execd: close child stderr reader during stop failed", err)
			}
		}
		if err := os.Remove(sock); err != nil && !os.IsNotExist(err) {
			telemetry.WarnErr(ctx, "execd: remove socket during stop failed", err)
		}
		if policyFile != "" {
			if err := os.Remove(policyFile); err != nil && !os.IsNotExist(err) {
				telemetry.WarnErr(ctx,
					"execd: remove policy file during stop failed", err)
			}
		}
	}

	// Allow generous startup time: the child compiles/links and builds
	// its sandbox backend, which can take a while under concurrent test
	// builds.
	deadline := time.Now().Add(15 * time.Second)
	for {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			client, err := Dial(ctx, conn)
			if err != nil {
				telemetry.WarnErr(ctx, "execd: close connection after dial failure",
					conn.Close())
				stop()
				return nil, sock, stop, fmt.Errorf("execd handshake: %w", err)
			}
			dialed = client
			return client, sock, stop, nil
		}
		if time.Now().After(deadline) {
			stop()
			return nil, sock, stop, fmt.Errorf("execd: socket not ready: %w", err)
		}
		select {
		case <-ctx.Done():
			stop()
			return nil, sock, stop, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// socketDir resolves the private directory holding execd sockets,
// creating it when needed.
func socketDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	base := filepath.Join(dir, "opencraft")
	if err := ensurePrivateDir(base); err != nil {
		// os.UserCacheDir can return a path whose parent is not
		// writable (e.g. ~/Library/Caches inside a seatbelt sandbox):
		// fall back to the system temp dir before giving up.
		base = filepath.Join(os.TempDir(), "opencraft")
		if err := ensurePrivateDir(base); err != nil {
			return "", err
		}
	}
	return base, nil
}

// execdSocketPath returns a fresh, unguessable unix socket path for the
// execd child. The path is private to the user (the user cache dir, mode
// 0700) and carries a random component, so other users on a shared box
// cannot pre-create or guess it (the old /tmp/<pid>.sock scheme was
// predictable and exposed a symlink race before the 0600 chmod in the
// server applied). Falls back to the temp dir if the cache dir is
// unavailable or unwritable (constrained sandboxes, CI); the random
// component keeps even that fallback safe.
func execdSocketPath() (string, error) {
	base, err := socketDir()
	if err != nil {
		return "", err
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return filepath.Join(base, "execd-"+hex.EncodeToString(b[:])+".sock"), nil
}

// ensurePrivateDir creates dir as 0700 and tightens the mode in case a
// looser directory pre-existed.
func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}
