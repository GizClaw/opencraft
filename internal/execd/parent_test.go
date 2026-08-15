package execd

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestExecdExitsWhenParentDies verifies the parent-death hardening: an
// execd child forked with -parent-pid must self-terminate and remove
// its socket when the parent is SIGKILLed, so a crashed host never
// leaves an orphan daemon behind.
func TestExecdExitsWhenParentDies(t *testing.T) {
	bin := buildOpencraft(t)
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(os.TempDir(),
		fmt.Sprintf("opencraft-execd-parent-%d.sock", os.Getpid()))
	_ = os.Remove(sock)
	defer func() { _ = os.Remove(sock) }()

	// Re-exec this test binary as the parent proxy: it forks execd
	// with its own PID as -parent-pid, announces readiness, then
	// blocks until the test SIGKILLs it.
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	parent := exec.Command(exe, "-test.run=TestExecdParentHelper")
	parent.Env = append(os.Environ(),
		"OPENCRAFT_EXECD_PARENT=1",
		"OPENCRAFT_EXECD_BIN="+bin,
		"OPENCRAFT_EXECD_ROOT="+root,
		"OPENCRAFT_EXECD_SOCK="+sock,
	)
	parent.Stderr = os.Stderr
	out, err := parent.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = parent.Process.Kill()
		_ = parent.Wait()
	}()

	ready := make(chan struct{})
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := out.Read(buf)
			if err != nil {
				return
			}
			if bytes.Contains(buf[:n], []byte("READY")) {
				close(ready)
				return
			}
		}
	}()
	select {
	case <-ready:
	case <-time.After(20 * time.Second):
		t.Fatal("parent proxy did not report execd ready")
	}
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("socket should exist while execd is alive: %v", err)
	}

	// SIGKILL the parent: no defers run on its side, so cleanup must
	// come from the execd child watching it.
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = parent.Wait()

	deadline := time.Now().Add(10 * time.Second)
	var lastStatErr error
	for time.Now().Before(deadline) {
		_, statErr := os.Stat(sock)
		lastStatErr = statErr
		sockGone := os.IsNotExist(statErr)
		_, pgrepErr := exec.Command("pgrep", "-f", sock).CombinedOutput()
		childGone := pgrepErr != nil
		if sockGone && childGone {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	pgOut, _ := exec.Command("pgrep", "-f", sock).CombinedOutput()
	t.Fatalf("execd child did not self-clean after parent death: "+
		"socket stat err=%v pgrep out=%q", lastStatErr, pgOut)
}

// TestExecdParentHelper is re-exec'd by TestExecdExitsWhenParentDies.
// It forks execd with itself as the parent and blocks until killed.
// Without the OPENCRAFT_EXECD_PARENT marker it is a no-op.
func TestExecdParentHelper(t *testing.T) {
	if os.Getenv("OPENCRAFT_EXECD_PARENT") != "1" {
		return
	}
	bin := os.Getenv("OPENCRAFT_EXECD_BIN")
	root := os.Getenv("OPENCRAFT_EXECD_ROOT")
	sock := os.Getenv("OPENCRAFT_EXECD_SOCK")

	cmd := exec.Command(bin, "execd", "-listen", sock,
		"-workdir", root, "-parent-pid", strconv.Itoa(os.Getpid()))
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "helper spawn execd:", err)
		os.Exit(1)
	}

	// Wait until the execd child is serving so the parent test does
	// not kill us before the watchdog is installed.
	deadline := time.Now().Add(15 * time.Second)
	for {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			fmt.Fprintln(os.Stderr, "helper: execd socket never became ready")
			os.Exit(1)
		}
		time.Sleep(50 * time.Millisecond)
	}
	fmt.Println("READY")
	for {
		time.Sleep(time.Hour)
	}
}
