package wslock

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const (
	helperEnv     = "WSLOCK_TEST_HELPER"
	helperPathEnv = "WSLOCK_TEST_PATH"
	helperKindEnv = "WSLOCK_TEST_KIND"
)

// TestHelperProcess is not a test: it is the child half of the
// cross-process case. The parent re-executes this test binary with
// WSLOCK_TEST_HELPER=1, the child takes the lock, prints "locked" and
// blocks until the parent kills it — which is the moment the kernel must
// release the lock.
func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		return
	}
	handle, err := Acquire(context.Background(),
		os.Getenv(helperPathEnv), os.Getenv(helperKindEnv))
	if err != nil {
		fmt.Fprintln(os.Stderr, "helper acquire:", err)
		os.Exit(2)
	}
	_ = handle
	fmt.Println("locked")
	// Sleep instead of blocking on an empty select: the runtime's
	// deadlock detector would abort the holder before the parent's
	// assertions run.
	for {
		time.Sleep(time.Hour)
	}
}

func TestAcquireReleaseRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	handle, err := Acquire(context.Background(), path, "gui")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if !handle.Owned() {
		t.Fatal("first handle is not the owner")
	}
	if info := handle.Info(); info.PID != os.Getpid() || info.Kind != "gui" {
		t.Fatalf("holder info = %+v, want this process and kind gui", info)
	}
	recorded, ok := ReadInfo(path)
	if !ok || recorded.PID != os.Getpid() {
		t.Fatalf("recorded holder = %+v (ok=%v), want this process", recorded, ok)
	}
	if err := handle.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	// The file keeps the last holder's record; the lock itself is what
	// decides liveness, so the workspace must be acquirable again.
	again, err := Acquire(context.Background(), path, "gui")
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	if !again.Owned() {
		t.Fatal("re-acquire returned a shared handle")
	}
	if err := again.Release(); err != nil {
		t.Fatalf("second release: %v", err)
	}
}

func TestAcquireFromSameProcessIsShared(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	owner, err := Acquire(context.Background(), path, "gui")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer func() { _ = owner.Release() }()

	// Two managers over one state root (tests, embedded hosts) must not
	// deadlock each other: the second handle is shared and proceeds.
	shared, err := Acquire(context.Background(), path, "headless")
	if err != nil {
		t.Fatalf("second acquire in one process: %v", err)
	}
	if shared.Owned() {
		t.Fatal("second handle claims ownership")
	}
	if err := shared.Release(); err != nil {
		t.Fatalf("shared release: %v", err)
	}
}

func TestAcquireReportsLiveForeignHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	child, stop := startHolder(t, path, "headless")
	defer stop()

	handle, err := Acquire(context.Background(), path, "gui")
	if handle != nil || err == nil {
		t.Fatalf("acquire while a live process holds it = (%v, %v), want held",
			handle, err)
	}
	if !errors.Is(err, ErrHeld) {
		t.Fatalf("error = %v, want ErrHeld", err)
	}
	holder, ok := IsHeld(err)
	if !ok {
		t.Fatalf("IsHeld(%v) = false", err)
	}
	if holder.PID != child.Process.Pid || holder.Kind != "headless" {
		t.Fatalf("holder = %+v, want pid %d kind headless",
			holder, child.Process.Pid)
	}
	if info, ok := ReadInfo(path); !ok || info.PID != child.Process.Pid {
		t.Fatalf("ReadInfo = %+v (ok=%v), want the child's record", info, ok)
	}

	// Killing the holder is what the whole design leans on: the kernel
	// drops the lock, and the next process may take the workspace.
	stop()
	recovered, err := Acquire(context.Background(), path, "gui")
	if err != nil {
		t.Fatalf("acquire after the holder died: %v", err)
	}
	if !recovered.Owned() {
		t.Fatal("handle after the holder died is not the owner")
	}
	if err := recovered.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
}

func TestAcquireFailsOnUnusablePath(t *testing.T) {
	dir := t.TempDir()
	// A directory can never be a lock file; the error must not read as
	// "someone else holds it", or callers would skip recovery forever.
	if _, err := Acquire(context.Background(), dir, "gui"); err == nil {
		t.Fatal("acquiring a directory path succeeded")
	} else if _, held := IsHeld(err); held {
		t.Fatalf("directory error reported as held: %v", err)
	}
}

// startHolder re-executes this test binary as a lock holder and waits
// until it reports the lock is taken.
func startHolder(t *testing.T, path, kind string) (*exec.Cmd, func()) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess")
	cmd.Env = append(os.Environ(),
		helperEnv+"=1",
		helperPathEnv+"="+path,
		helperKindEnv+"="+kind,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start holder: %v", err)
	}
	ready := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err != nil {
			ready <- fmt.Errorf("holder exited before locking: %w", err)
			return
		}
		if line != "locked\n" {
			ready <- fmt.Errorf("holder said %q, want locked", line)
			return
		}
		ready <- nil
	}()
	select {
	case err := <-ready:
		if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal("timed out waiting for the lock holder")
	}
	stopped := false
	return cmd, func() {
		if stopped {
			return
		}
		stopped = true
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
}
