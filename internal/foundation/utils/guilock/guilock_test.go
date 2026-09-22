package guilock

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/foundation/utils/wslock"
)

const (
	helperEnv      = "GUILOCK_TEST_HELPER"
	helperRootEnv  = "GUILOCK_TEST_ROOT"
	helperIDEnv    = "GUILOCK_TEST_ID"
	helperServeEnv = "GUILOCK_TEST_SERVE"
)

// testIdentity keeps each test's endpoint out of its neighbour's way:
// the fallback endpoint names live in one temporary directory.
func testIdentity(t *testing.T) string {
	t.Helper()
	return "guilock-test-" + t.Name()
}

// TestHelperProcess is not a test: it is the child half of the
// cross-process cases. The parent re-executes this test binary with
// GUILOCK_TEST_HELPER=1; the child takes the lock, reports it on stdout
// (and serves raises when asked) and blocks until the parent kills it -
// which is the moment the kernel must hand the state root over.
func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		return
	}
	lock, err := Acquire(context.Background(),
		os.Getenv(helperRootEnv), os.Getenv(helperIDEnv))
	if err != nil {
		fmt.Fprintln(os.Stderr, "helper acquire:", err)
		os.Exit(2)
	}
	fmt.Println("locked")
	if os.Getenv(helperServeEnv) == "1" {
		lock.Serve(context.Background(), func(attempt Launch) {
			fmt.Printf("raised %d %s\n", attempt.PID, strings.Join(attempt.Args, "|"))
		})
		fmt.Println("serving")
	}
	// Sleep instead of blocking on an empty select: the runtime's
	// deadlock detector would abort the holder before the parent's
	// assertions run.
	for {
		time.Sleep(time.Hour)
	}
}

func TestAcquireRecordsHolderAndReleases(t *testing.T) {
	root := t.TempDir()
	lock, err := Acquire(context.Background(), root, testIdentity(t))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if !lock.Owned() {
		t.Fatal("first handle is not the owner")
	}
	if info := lock.Info(); info.PID != os.Getpid() || info.Kind != Kind {
		t.Fatalf("holder info = %+v, want this process and kind %q", info, Kind)
	}
	// The lock file sits in the state root, spelled out: the location is
	// the contract the diagnostics point at.
	lockPath := filepath.Join(root, "gui.lock")
	recorded, ok := wslock.ReadInfo(lockPath)
	if !ok || recorded.PID != os.Getpid() {
		t.Fatalf("recorded holder = %+v (ok=%v), want this process", recorded, ok)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	again, err := Acquire(context.Background(), root, testIdentity(t))
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
	root := t.TempDir()
	first, err := Acquire(context.Background(), root, testIdentity(t))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	second, err := Acquire(context.Background(), root, testIdentity(t))
	if err != nil {
		t.Fatalf("second acquire in the same process: %v", err)
	}
	if second.Owned() {
		t.Fatal("second in-process handle claims ownership")
	}
	if err := second.Release(); err != nil {
		t.Fatalf("shared release: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("owner release: %v", err)
	}
}

func TestSecondLaunchFromOtherProcessIsHeld(t *testing.T) {
	root := t.TempDir()
	h := startHolder(t, root, testIdentity(t), false)
	t.Cleanup(h.stop)

	lock, err := Acquire(context.Background(), root, testIdentity(t))
	if err == nil || lock != nil {
		t.Fatalf("acquire succeeded (%v) while a live holder owns the root", err)
	}
	info, held := IsHeld(err)
	if !held {
		t.Fatalf("error did not report a live holder: %v", err)
	}
	if info.PID != h.cmd.Process.Pid || info.Kind != Kind {
		t.Fatalf("holder info = %+v, want the helper pid %d", info, h.cmd.Process.Pid)
	}

	// The kernel hands the root over once the holder is gone.
	h.stop()
	recovered, err := Acquire(context.Background(), root, testIdentity(t))
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

func TestAcquireFailsOpenOnUnusableRoot(t *testing.T) {
	// A regular file can never be a state root; the error must not read
	// as "someone else holds it", or the caller would refuse a launch
	// over a filesystem oddity.
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	lock, err := Acquire(context.Background(), file, testIdentity(t))
	if err == nil || lock != nil {
		t.Fatalf("acquiring under a file returned (%v, %v)", lock, err)
	}
	if _, held := IsHeld(err); held {
		t.Fatalf("filesystem error reported as held: %v", err)
	}
}

func TestRootsAreIndependent(t *testing.T) {
	first, err := Acquire(context.Background(), t.TempDir(), "identity-a")
	if err != nil {
		t.Fatalf("acquire first root: %v", err)
	}
	second, err := Acquire(context.Background(), t.TempDir(), "identity-b")
	if err != nil {
		t.Fatalf("acquire second root: %v", err)
	}
	if !first.Owned() || !second.Owned() {
		t.Fatal("two roots did not both acquire")
	}
	_ = first.Release()
	_ = second.Release()
}

// TestRaiseStaysInsideOneRoot pins the shape of `wails3 task dev` next to
// the installed app: two profiles, two state roots, two holders. Both
// roots are held at once, and a raise lands on the holder of the root it
// names - asking the dev instance to come forward must never touch the
// installed app's window.
func TestRaiseStaysInsideOneRoot(t *testing.T) {
	prodRoot, devRoot := t.TempDir(), t.TempDir()
	prod, err := Acquire(context.Background(), prodRoot, "identity-prod")
	if err != nil {
		t.Fatalf("acquire installed app root: %v", err)
	}
	defer func() { _ = prod.Release() }()
	dev, err := Acquire(context.Background(), devRoot, "identity-dev")
	if err != nil {
		t.Fatalf("acquire dev root: %v", err)
	}
	defer func() { _ = dev.Release() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prodRaises := make(chan Launch, 1)
	devRaises := make(chan Launch, 1)
	prod.Serve(ctx, func(attempt Launch) { prodRaises <- attempt })
	dev.Serve(ctx, func(attempt Launch) { devRaises <- attempt })

	if err := RequestRaise(context.Background(), devRoot, "identity-dev",
		Launch{PID: 7, Args: []string{"--profile", "dev"}}); err != nil {
		t.Fatalf("raise dev root: %v", err)
	}
	select {
	case got := <-devRaises:
		if got.PID != 7 {
			t.Fatalf("dev holder saw pid %d, want 7", got.PID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the dev holder was not raised")
	}
	select {
	case got := <-prodRaises:
		t.Fatalf("the installed app's holder saw a raise meant for the dev root: %+v", got)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestServeAnswersRaise(t *testing.T) {
	root := t.TempDir()
	lock, err := Acquire(context.Background(), root, testIdentity(t))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer func() { _ = lock.Release() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attempts := make(chan Launch, 1)
	lock.Serve(ctx, func(attempt Launch) { attempts <- attempt })

	want := Launch{PID: 4242, Args: []string{"--profile", "dev"}, WorkingDir: "/tmp/scratch"}
	if err := RequestRaise(context.Background(), root, testIdentity(t), want); err != nil {
		t.Fatalf("request raise: %v", err)
	}
	select {
	case got := <-attempts:
		if got.PID != want.PID ||
			strings.Join(got.Args, " ") != strings.Join(want.Args, " ") ||
			got.WorkingDir != want.WorkingDir {
			t.Fatalf("raise payload = %+v, want %+v", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the raise handler was not called")
	}
}

func TestServeOnSharedHandleIsNoOp(t *testing.T) {
	// One root has one endpoint; only the handle that owns the kernel
	// lock may serve it, or a second in-process handle would steal the
	// socket from the real holder.
	root := t.TempDir()
	owner, err := Acquire(context.Background(), root, testIdentity(t))
	if err != nil {
		t.Fatalf("acquire owner: %v", err)
	}
	defer func() { _ = owner.Release() }()
	shared, err := Acquire(context.Background(), root, testIdentity(t))
	if err != nil {
		t.Fatalf("acquire shared: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan Launch, 1)
	shared.Serve(ctx, func(attempt Launch) { served <- attempt })
	if err := RequestRaise(context.Background(), root, testIdentity(t), Launch{PID: 7}); err == nil {
		t.Fatal("a raise reached a handle that does not own the root")
	}
	select {
	case attempt := <-served:
		t.Fatalf("shared handle served %+v", attempt)
	default:
	}
}

func TestServeReplacesStaleEndpoint(t *testing.T) {
	root := t.TempDir()
	endpoint := endpointName(root, testIdentity(t))
	// A holder that dies without cleanup leaves the socket path behind.
	if err := os.WriteFile(endpoint, []byte("stale"), 0o600); err != nil {
		t.Fatalf("write stale endpoint: %v", err)
	}
	lock, err := Acquire(context.Background(), root, testIdentity(t))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer func() { _ = lock.Release() }()
	ctx, cancel := context.WithCancel(context.Background())
	attempts := make(chan Launch, 1)
	lock.Serve(ctx, func(attempt Launch) { attempts <- attempt })
	if err := RequestRaise(context.Background(), root, testIdentity(t), Launch{PID: 11}); err != nil {
		t.Fatalf("request raise after a stale endpoint: %v", err)
	}
	select {
	case <-attempts:
	case <-time.After(5 * time.Second):
		t.Fatal("the raise handler was not called")
	}
	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(endpoint); errors.Is(err, os.ErrNotExist) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("endpoint %s was not cleaned up", endpoint)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestLongRootFallsBackToTempEndpoint(t *testing.T) {
	// A state root deep enough that <root>/gui.sock cannot fit a unix
	// socket address still keeps the lock and moves only the raise
	// endpoint.
	root := filepath.Join(t.TempDir(), strings.Repeat("d", 120))
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("make deep root: %v", err)
	}
	endpoint := endpointName(root, testIdentity(t))
	if strings.HasPrefix(endpoint, root) {
		t.Fatalf("endpoint %s still lives under the over-long root", endpoint)
	}
	lock, err := Acquire(context.Background(), root, testIdentity(t))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer func() { _ = lock.Release() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attempts := make(chan Launch, 1)
	lock.Serve(ctx, func(attempt Launch) { attempts <- attempt })
	if err := RequestRaise(context.Background(), root, testIdentity(t), Launch{PID: 12}); err != nil {
		t.Fatalf("request raise over the fallback endpoint: %v", err)
	}
	select {
	case <-attempts:
	case <-time.After(5 * time.Second):
		t.Fatal("the raise handler was not called")
	}
}

func TestRequestRaiseWithoutListenerFails(t *testing.T) {
	root := t.TempDir()
	lock, err := Acquire(context.Background(), root, testIdentity(t))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer func() { _ = lock.Release() }()
	// The holder owns the root but has not started serving yet: the
	// rejected launch must report a raise that did not happen instead of
	// waiting on it.
	start := time.Now()
	err = RequestRaise(context.Background(), root, testIdentity(t), Launch{PID: os.Getpid()})
	if err == nil {
		t.Fatal("raise succeeded without a listener")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("raise without a listener blocked for %s", elapsed)
	}
}

func TestRaiseReachesAnotherProcess(t *testing.T) {
	root := t.TempDir()
	h := startHolder(t, root, testIdentity(t), true)
	t.Cleanup(h.stop)
	err := RequestRaise(context.Background(), root, testIdentity(t),
		Launch{PID: 4242, Args: []string{"--profile", "dev"}, WorkingDir: "/tmp/x"})
	if err != nil {
		t.Fatalf("raise: %v", err)
	}
	if line := h.readLine(t); line != "raised 4242 --profile|dev" {
		t.Fatalf("holder said %q, want %q", line, "raised 4242 --profile|dev")
	}
}

// holder is a re-executed test binary holding (and optionally serving)
// one state root.
type holder struct {
	cmd    *exec.Cmd
	lines  *bufio.Reader
	closed bool
}

// startHolder runs the helper until it reports the lock, so the parent's
// assertions never race the child's startup.
func startHolder(t *testing.T, root, identity string, serve bool) *holder {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess")
	env := append(os.Environ(),
		helperEnv+"=1",
		helperRootEnv+"="+root,
		helperIDEnv+"="+identity,
	)
	if serve {
		env = append(env, helperServeEnv+"=1")
	}
	cmd.Env = env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start holder: %v", err)
	}
	h := &holder{cmd: cmd, lines: bufio.NewReader(stdout)}
	if line := h.readLine(t); line != "locked" {
		h.stop()
		t.Fatalf("holder said %q, want locked", line)
	}
	if serve && h.readLine(t) != "serving" {
		h.stop()
		t.Fatal("holder did not start serving")
	}
	return h
}

// readLine returns the next line the holder printed, failing the test
// instead of hanging on a child that never gets there.
func (h *holder) readLine(t *testing.T) string {
	t.Helper()
	type result struct {
		line string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		line, err := h.lines.ReadString('\n')
		done <- result{line: line, err: err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			h.stop()
			t.Fatalf("read holder output: %v", r.err)
		}
		return strings.TrimSpace(r.line)
	case <-time.After(30 * time.Second):
		h.stop()
		t.Fatal("timed out waiting for the holder")
		return ""
	}
}

func (h *holder) stop() {
	if h == nil || h.closed {
		return
	}
	h.closed = true
	_ = h.cmd.Process.Kill()
	_ = h.cmd.Wait()
}
