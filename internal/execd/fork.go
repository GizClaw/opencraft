package execd

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Launch forks the current executable in execd mode and dials its
// unix socket. The returned stop function terminates the child and
// removes the socket.
func Launch(ctx context.Context, workDir string) (*Client, func(), error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, nil, fmt.Errorf("execd executable: %w", err)
	}
	return LaunchExe(ctx, workDir, executable)
}

// LaunchExe forks the given executable in execd mode and dials
// its unix socket. Launch uses os.Executable; LaunchExe exists for
// tests and embedded hosts that know a different binary path.
func LaunchExe(
	ctx context.Context,
	workDir, executable string,
) (*Client, func(), error) {
	sock := filepath.Join(os.TempDir(),
		fmt.Sprintf("opencraft-execd-%d.sock", os.Getpid()))
	_ = os.Remove(sock)

	cmd := exec.CommandContext(ctx, executable,
		"execd", "-listen", sock, "-workdir", workDir)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("execd launch: %w", err)
	}
	stop := func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = os.Remove(sock)
	}

	deadline := time.Now().Add(5 * time.Second)
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
