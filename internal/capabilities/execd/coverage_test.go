package execd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/sandbox"
	"github.com/GizClaw/flowcraft/core/sandbox/local"
)

// localRunnerFor returns an in-process backend for tests that only need
// a runner to wrap.
func localRunnerFor(t *testing.T) sandbox.Runner {
	t.Helper()
	return local.New(t.TempDir())
}

// recordingRunner records the sessions a backend was asked to start, so a
// test can tell which side of the confined/unconfined split served a
// request.
type recordingRunner struct {
	sandbox.Runner
	mu     sync.Mutex
	starts []sandbox.SessionSpec
}

func (r *recordingRunner) Start(
	ctx context.Context, spec sandbox.SessionSpec,
) (sandbox.Session, error) {
	r.mu.Lock()
	r.starts = append(r.starts, spec)
	r.mu.Unlock()
	return r.Runner.Start(ctx, spec)
}

func (r *recordingRunner) started() []sandbox.SessionSpec {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]sandbox.SessionSpec(nil), r.starts...)
}

// modeKey marks a context whose command must run unconfined.
type modeKey struct{}

// TestUnconfinedRoutingPicksTheUnconfinedBackend pins the YOLO path:
// Start.Unconfined is resolved per request through SetModeFunc, the
// unconfined backend serves that request, and the child runs it with an
// empty env policy (the escalation contract).
func TestUnconfinedRoutingPicksTheUnconfinedBackend(t *testing.T) {
	confined := &recordingRunner{Runner: localRunnerFor(t)}
	unconfined := &recordingRunner{Runner: localRunnerFor(t)}
	client, _ := testPairWithFactory(t, func(
		_ context.Context, _ string, _ *SandboxPolicy,
	) (RunnerSet, error) {
		return RunnerSet{Confined: confined, Unconfined: unconfined}, nil
	})
	runner, err := NewRemoteRunner(
		context.Background(), client, nil, t.TempDir(), &SandboxPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runner.Close() })
	runner.SetModeFunc(func(ctx context.Context) bool {
		marked, _ := ctx.Value(modeKey{}).(bool)
		return marked
	})

	if _, err := runner.Start(context.Background(), sandbox.SessionSpec{
		ID: "plain", Argv: []string{"/bin/sh", "-c", "true"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Start(
		context.WithValue(context.Background(), modeKey{}, true),
		sandbox.SessionSpec{
			ID: "yolo", Argv: []string{"/bin/sh", "-c", "true"},
		},
	); err != nil {
		t.Fatal(err)
	}

	if got := confined.started(); len(got) != 1 || got[0].ID != "plain" {
		t.Fatalf("confined starts = %+v, want only the plain request", got)
	}
	got := unconfined.started()
	if len(got) != 1 || got[0].ID != "yolo" {
		t.Fatalf("unconfined starts = %+v, want only the marked request", got)
	}
	if got[0].Opts.Env.Allow != nil || got[0].Opts.Env.Inject != nil {
		t.Fatalf("unconfined env = %+v, want the escalated request to run "+
			"without a policy env", got[0].Opts.Env)
	}
}

// TestChildExitsWhenParentChannelCloses pins the lifetime claim: the
// child is kept alive by the channel alone, so a parent that dies
// without stopping it (the SIGKILL case the journal covers) still takes
// the child down.
func TestChildExitsWhenParentChannelCloses(t *testing.T) {
	SetJournalRoot(t.TempDir())
	bin := buildOpencraft(t)
	client, stop, err := LaunchExe(context.Background(), bin)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	pid := journalChildPID(t)
	// Abrupt close, no stop(): this is what the child sees when its
	// parent is killed.
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !childTerminated(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("child pid %d outlived its parent channel", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// childTerminated reports whether pid is gone or a zombie. The test
// deliberately delays cmd.Wait (the deferred stop), so the exited child
// would otherwise still answer kill(pid, 0).
func childTerminated(pid int) bool {
	if !processAlive(pid) {
		return true
	}
	out, err := exec.Command(
		"ps", "-o", "state=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(string(out)), "Z")
}

// journalChildPID reads the one journal entry a fresh launch wrote.
func journalChildPID(t *testing.T) int {
	t.Helper()
	dir, err := childrenDir()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		pidPart, _, ok := strings.Cut(entry.Name(), "-")
		if !ok {
			continue
		}
		pid, err := strconv.Atoi(pidPart)
		if err != nil {
			continue
		}
		return pid
	}
	t.Fatal("no journal entry for the launched child")
	return 0
}

// TestBindPassesTheSandboxPolicyThrough pins that the workspace policy
// (writable roots, env allow/inject) survives the wire and reaches the
// backend that runs the command.
func TestBindPassesTheSandboxPolicyThrough(t *testing.T) {
	var (
		mu       sync.Mutex
		recorded *SandboxPolicy
	)
	inner := localFactory(t)
	client, _ := testPairWithFactory(t, func(
		ctx context.Context, workdir string, policy *SandboxPolicy,
	) (RunnerSet, error) {
		mu.Lock()
		recorded = policy
		mu.Unlock()
		return inner(ctx, workdir, policy)
	})
	ctx := context.Background()
	policy := &SandboxPolicy{
		WritablePaths: []string{t.TempDir()},
		EnvAllow:      []string{"PATH"},
		EnvInject:     map[string]string{"EXECD_TEST_ENV": "injected"},
		EnvAllowSet:   true,
	}
	if _, err := client.Bind(ctx, t.TempDir(), policy); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := recorded
	mu.Unlock()
	if got == nil || got.GetWritablePaths()[0] != policy.WritablePaths[0] ||
		got.GetEnvAllow()[0] != "PATH" || !got.GetEnvAllowSet() ||
		got.GetEnvInject()["EXECD_TEST_ENV"] != "injected" {
		t.Fatalf("policy on the wire = %+v, want %+v", got, policy)
	}

	// The injected env has to reach the command the backend starts.
	if _, err := client.Start(ctx, &Start{
		ProcessId: "env",
		Argv:      []string{"/bin/sh", "-c", "echo $EXECD_TEST_ENV"},
	}); err != nil {
		t.Fatal(err)
	}
	output := readUntilEOF(t, client, "env")
	if !strings.Contains(output, "injected") {
		t.Fatalf("command output = %q, want the bound env policy", output)
	}
}

// readUntilEOF drains one process and returns everything it wrote.
func readUntilEOF(t *testing.T, client *Client, id string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var output strings.Builder
	var after int64
	for {
		read, err := client.Read(ctx, &Read{
			ProcessId: id, AfterSeq: after, MaxBytes: 64 << 10,
		})
		if err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		for _, chunk := range read.GetChunks() {
			output.Write(chunk.GetData())
		}
		after = read.GetNextSeq()
		if read.GetEof() {
			return output.String()
		}
	}
}

// TestSessionSignalInterrupts pins the signal primitive the exec tools
// rely on: interrupt is delivered and the process ends on its own.
//
// The command runs without a shell on purpose. "/bin/sh -c ..." installs
// its SIGINT handler before it forks the command, so an interrupt that
// lands in that window — microseconds idle, milliseconds under load — is
// caught by the shell and never reaches the forked command. The shell
// then waits out a child that is still asleep and the interrupt looks
// lost, which is what made this test flake on loaded CI. A process that
// exists before Signal cannot miss it: the group has exactly one member,
// and a signal that arrives while the child is still pre-exec stays
// pending until it execs and then kills it.
func TestSessionSignalInterrupts(t *testing.T) {
	runner := testRunner(t)
	ctx := context.Background()
	session, err := runner.Start(ctx, sandbox.SessionSpec{
		ID:   "signal",
		Argv: []string{"/bin/sleep", "30"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Signal(ctx, sandbox.SessionSignalInterrupt); err != nil {
		t.Fatalf("signal: %v", err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := session.Wait(waitCtx); err != nil {
		t.Fatalf("wait after signal: %v (the process outlived it: %s)",
			err, processSnapshot(session.PID()))
	}
}

// processSnapshot reports how the interrupted process looked when the
// wait gave up: its group and state, and on Linux the signal mask bits
// that decide whether an interrupt can ever land.
//
// A timeout alone cannot say which way a SIGINT to the process group
// fails to end a `/bin/sleep`. The signal may have gone to a group the
// process is not in (`pgid` next to `pid`), the process may have exec'd
// with SIGINT ignored or blocked (`sigIgn`/`sigBlk`, bit 0x2), a SIGINT
// may be waiting unread (`sigPnd`), or the process may already be a
// zombie nobody reaped (`stat` Z, which points at the exit path instead
// of the signal). CI is the only place this reproduces so far, so the
// failure carries its own evidence instead of another rerun.
func processSnapshot(pid int) string {
	parts := []string{fmt.Sprintf("pid=%d", pid)}
	out, err := exec.Command("ps", "-o", "ppid=,pgid=,stat=,command=", "-p",
		strconv.Itoa(pid)).Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		parts = append(parts, "ps: process gone")
	} else {
		parts = append(parts, "ps: "+strings.Join(strings.Fields(string(out)), " "))
	}
	if data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status")); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			switch fields[0] {
			case "SigPnd:", "ShdPnd:", "SigBlk:", "SigIgn:", "SigCgt:":
				parts = append(parts, fields[0]+" "+fields[1])
			}
		}
	}
	return strings.Join(parts, " ")
}

// TestSessionResizeOnTTY pins the resize RPC on a real pty session.
func TestSessionResizeOnTTY(t *testing.T) {
	runner := testRunner(t)
	ctx := context.Background()
	session, err := runner.Start(ctx, sandbox.SessionSpec{
		ID:   "pty",
		Argv: []string{"/bin/sh", "-c", "sleep 30"},
		TTY:  true,
		Rows: 24,
		Cols: 80,
	})
	if err != nil {
		if errdefs.IsNotAvailable(err) {
			t.Skipf("pty sessions unavailable: %v", err)
		}
		t.Fatal(err)
	}
	if err := session.Resize(ctx, 100, 40); err != nil {
		t.Fatalf("resize: %v", err)
	}
}

// TestReadRespectsMaxBytes pins the per-response cap the client relies on
// to bound one ReadOk frame.
func TestReadRespectsMaxBytes(t *testing.T) {
	client, _ := testPair(t)
	ctx := context.Background()
	if _, err := client.Bind(ctx, t.TempDir(), &SandboxPolicy{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Start(ctx, &Start{
		ProcessId: "large",
		Argv:      []string{"/bin/sh", "-c", "printf 0123456789"},
	}); err != nil {
		t.Fatal(err)
	}
	first, err := client.Read(ctx, &Read{ProcessId: "large", MaxBytes: 4})
	if err != nil {
		t.Fatal(err)
	}
	size := 0
	for _, chunk := range first.GetChunks() {
		size += len(chunk.GetData())
	}
	if size > 4 {
		t.Fatalf("first read returned %d bytes, want <= 4", size)
	}
	if first.GetEof() {
		t.Fatal("first read reported EOF, want a partial read")
	}
	if got := readUntilEOF(t, client, "large"); got != "0123456789" {
		t.Fatalf("drained output = %q", got)
	}
}

// TestEventNotificationsReachTheClient pins the notification surface the
// protocol keeps for a future streaming consumer: output, exit and close
// arrive in order with growing cursors.
func TestEventNotificationsReachTheClient(t *testing.T) {
	client, _ := testPair(t)
	ctx := context.Background()
	events := make(chan *Notification, 64)
	client.SetNotificationHandler(func(n *Notification) {
		select {
		case events <- n:
		default:
		}
	})
	if _, err := client.Bind(ctx, t.TempDir(), &SandboxPolicy{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Start(ctx, &Start{
		ProcessId: "notify",
		Argv:      []string{"/bin/sh", "-c", "echo notified"},
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(10 * time.Second)
	var output strings.Builder
	var sawExited, sawClosed bool
	for !sawClosed {
		select {
		case n := <-events:
			switch {
			case n.GetOutput() != nil:
				output.Write(n.GetOutput().GetData())
			case n.GetExited() != nil:
				sawExited = true
			case n.GetClosed() != nil:
				sawClosed = true
			}
		case <-deadline:
			t.Fatalf("notifications incomplete: output=%q exited=%v closed=%v",
				output.String(), sawExited, sawClosed)
		}
	}
	if !sawExited || !strings.Contains(output.String(), "notified") {
		t.Fatalf("output=%q exited=%v, want the command's events",
			output.String(), sawExited)
	}
}

// TestJournalEntriesCarryTheChildIdentity pins the extra fields the
// sweep relies on to tell a reused pid from a real orphan.
func TestJournalEntriesCarryTheChildIdentity(t *testing.T) {
	SetJournalRoot(t.TempDir())
	bin := buildOpencraft(t)
	_, stop, err := LaunchExe(context.Background(), bin)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	dir, err := childrenDir()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var record childRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
		if record.PID <= 0 || record.Nonce == "" ||
			record.ParentPID <= 0 || record.CreatedAt <= 0 {
			t.Fatalf("journal record = %+v, want pid/nonce/parent/createdAt",
				record)
		}
		return
	}
	t.Fatal("no journal entry written by the launch")
}
