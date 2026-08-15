package execd

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// stopGrace is how long stop waits for the execd child to shut down
// gracefully (and terminate its sessions) before SIGKILL.
const stopGrace = 3 * time.Second

// Launch forks the current executable in execd mode and dials its
// unix socket. policyJSON is the parent's serialized sandbox policy
// (writable paths + environment policy); it is forwarded to the child
// as -sandbox-policy so the child's default environment matches the
// deploy document. The returned stop function terminates the child
// and removes the socket.
func Launch(
	ctx context.Context,
	workDir, policyJSON string,
) (*Client, func(), error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, nil, fmt.Errorf("execd executable: %w", err)
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
) (*Client, func(), error) {
	sock := filepath.Join(os.TempDir(),
		fmt.Sprintf("opencraft-execd-%d.sock", os.Getpid()))
	_ = os.Remove(sock)

	// The child watches its parent: if this process dies without
	// running stop (SIGKILL, crash), the child self-terminates and
	// removes the socket instead of leaking.
	args := []string{
		"execd", "-listen", sock, "-workdir", workDir,
		"-parent-pid", strconv.Itoa(os.Getpid()),
	}
	if policyJSON != "" {
		args = append(args, "-sandbox-policy", policyJSON)
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("execd launch: %w", err)
	}
	stop := func() {
		// SIGTERM first so the child runs its own cleanup
		// (terminating sandbox sessions), then SIGKILL as a bound
		// fallback.
		_ = cmd.Process.Signal(syscall.SIGTERM)
		waited := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			close(waited)
		}()
		select {
		case <-waited:
		case <-time.After(stopGrace):
			_ = cmd.Process.Kill()
			<-waited
		}
		_ = os.Remove(sock)
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
				_ = conn.Close()
				stop()
				return nil, nil, fmt.Errorf("execd handshake: %w", err)
			}
			return client, stop, nil
		}
		if time.Now().After(deadline) {
			stop()
			return nil, nil, fmt.Errorf("execd: socket not ready: %w", err)
		}
		select {
		case <-ctx.Done():
			stop()
			return nil, nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
