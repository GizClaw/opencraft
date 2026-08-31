package execd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
// and removes the socket. The returned socket path is the unix socket
// the child listens on (useful for cleanup verification and status).
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
		return nil, sock, nil, fmt.Errorf("execd launch: %w", err)
	}
	var dialed *Client
	stop := func() {
		// Close the client first: the child's Serve loop returns on
		// EOF and runs its in-process session cleanup. On unix this is
		// belt-and-braces alongside SIGTERM; on Windows it is the
		// graceful shutdown trigger (SIGTERM is not deliverable there).
		if dialed != nil {
			_ = dialed.Close()
		}
		// SIGTERM on unix, no-op on Windows (EOF close above).
		_ = terminateExecd(cmd)
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

// execdSocketPath returns a fresh, unguessable unix socket path for the
// execd child. The path is private to the user (the user cache dir, mode
// 0700) and carries a random component, so other users on a shared box
// cannot pre-create or guess it (the old /tmp/<pid>.sock scheme was
// predictable and exposed a symlink race before the 0600 chmod in the
// server applied). Falls back to the temp dir if the cache dir is
// unavailable or unwritable (constrained sandboxes, CI); the random
// component keeps even that fallback safe.
func execdSocketPath() (string, error) {
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
