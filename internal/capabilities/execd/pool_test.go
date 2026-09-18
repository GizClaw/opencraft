package execd

import (
	"context"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/sandbox"
)

// testChild builds one in-process child pair for the pool launcher.
func testChild(t *testing.T) (*Client, func(), error) {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	srv := New(localFactory(t), serverConn, serverConn)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Serve(ctx) }()
	client, err := Dial(ctx, clientConn)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	stop := func() {
		_ = client.Close()
		cancel()
	}
	return client, stop, nil
}

func TestPoolLeasesAndReusesChild(t *testing.T) {
	settings := DefaultPoolSettings()
	settings.Prewarm = 0
	pool := NewPool(settings)
	defer pool.Close()
	var launches atomic.Int64
	pool.SetLauncher(func(context.Context) (*Client, func(), error) {
		launches.Add(1)
		return testChild(t)
	})

	ctx := context.Background()
	workdir := t.TempDir()
	runner, err := pool.Lease(ctx, workdir, &SandboxPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := sandbox.Exec(ctx, runner, "/bin/sh",
		[]string{"-c", "echo pool-ok"}, sandbox.ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit = %d", result.ExitCode)
	}
	if err := runner.Close(); err != nil {
		t.Fatal(err)
	}
	if idle, active := pool.Stats(); idle != 1 || active != 0 {
		t.Fatalf("stats = idle %d active %d, want 1/0", idle, active)
	}

	second, err := pool.Lease(ctx, workdir, &SandboxPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	if launches.Load() != 1 {
		t.Fatalf("launches = %d, want the released child to be reused",
			launches.Load())
	}
}

// TestPoolOverflowGetsDedicatedChild pins the capacity semantics: the
// cap bounds children the pool reuses, and a workspace beyond it still
// gets a child (dedicated, not pooled) instead of a failed assembly.
func TestPoolOverflowGetsDedicatedChild(t *testing.T) {
	settings := DefaultPoolSettings()
	settings.Prewarm = 0
	settings.MaxActive = 1
	pool := NewPool(settings)
	defer pool.Close()
	var launches atomic.Int64
	pool.SetLauncher(func(context.Context) (*Client, func(), error) {
		launches.Add(1)
		return testChild(t)
	})
	ctx := context.Background()
	first, err := pool.Lease(ctx, t.TempDir(), &SandboxPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := pool.Lease(ctx, t.TempDir(), &SandboxPolicy{})
	if err != nil {
		t.Fatalf("lease beyond maxActive: %v", err)
	}
	if launches.Load() != 2 {
		t.Fatalf("launches = %d, want the overflow lease to fork its own child",
			launches.Load())
	}
	if _, active := pool.Stats(); active != 1 {
		t.Fatalf("active = %d, want only the pooled lease counted", active)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if idle, _ := pool.Stats(); idle != 0 {
		t.Fatalf("idle = %d, want the dedicated child to leave the pool alone", idle)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPoolReapsIdleBeyondPrewarm(t *testing.T) {
	settings := DefaultPoolSettings()
	settings.Prewarm = 0
	settings.IdleTTL = 50 * time.Millisecond
	pool := NewPool(settings)
	defer pool.Close()
	var stops atomic.Int64
	pool.SetLauncher(func(context.Context) (*Client, func(), error) {
		client, stop, err := testChild(t)
		if err != nil {
			return nil, nil, err
		}
		return client, func() { stops.Add(1); stop() }, nil
	})
	ctx := context.Background()
	runner, err := pool.Lease(ctx, t.TempDir(), &SandboxPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Close(); err != nil {
		t.Fatal(err)
	}
	pool.mu.Lock()
	if len(pool.idle) != 1 {
		pool.mu.Unlock()
		t.Fatalf("idle = %d, want 1", len(pool.idle))
	}
	pool.idle[0].since = time.Now().Add(-time.Hour)
	pool.mu.Unlock()
	pool.reapIdle()
	if idle, _ := pool.Stats(); idle != 0 {
		t.Fatalf("idle = %d after reap, want 0", idle)
	}
	if stops.Load() == 0 {
		t.Fatal("reaped child was not stopped")
	}
}

// TestPoolPrewarmsAfterFirstLease pins the lazy pre-warm: nothing is
// forked before something wants a child, and from the first lease on
// the pool keeps Prewarm children ready.
func TestPoolPrewarmsAfterFirstLease(t *testing.T) {
	settings := DefaultPoolSettings()
	settings.Prewarm = 1
	pool := NewPool(settings)
	defer pool.Close()
	var launches atomic.Int64
	pool.SetLauncher(func(context.Context) (*Client, func(), error) {
		launches.Add(1)
		return testChild(t)
	})
	time.Sleep(200 * time.Millisecond)
	if idle, _ := pool.Stats(); idle != 0 {
		t.Fatalf("idle = %d before the first lease, want 0", idle)
	}
	runner, err := pool.Lease(context.Background(), t.TempDir(), &SandboxPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if idle, _ := pool.Stats(); idle >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pool never topped itself back up to Prewarm")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if launches.Load() != 2 {
		t.Fatalf("launches = %d, want the lease plus one pre-warmed child",
			launches.Load())
	}
	if err := runner.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestPoolSetSettingsTrimsIdleChildren pins that lowering MaxIdle takes
// effect on the children that are already idle.
func TestPoolSetSettingsTrimsIdleChildren(t *testing.T) {
	settings := DefaultPoolSettings()
	settings.Prewarm = 0
	settings.MaxIdle = 4
	pool := NewPool(settings)
	defer pool.Close()
	pool.SetLauncher(func(context.Context) (*Client, func(), error) {
		return testChild(t)
	})
	ctx := context.Background()
	runners := make([]*RemoteRunner, 0, 3)
	for i := 0; i < 3; i++ {
		runner, err := pool.Lease(ctx, t.TempDir(), &SandboxPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		runners = append(runners, runner)
	}
	for _, runner := range runners {
		if err := runner.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if idle, _ := pool.Stats(); idle != 3 {
		t.Fatalf("idle = %d, want 3", idle)
	}
	next := pool.Settings()
	next.MaxIdle = 1
	pool.SetSettings(next)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if idle, _ := pool.Stats(); idle <= 1 {
			return
		}
		if time.Now().After(deadline) {
			idle, _ := pool.Stats()
			t.Fatalf("idle = %d after lowering MaxIdle, want <= 1", idle)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestPoolPrewarmedChildSurvivesLaunchContext pins the regression that
// killed every pre-warmed child: the pool cancels its background launch
// context as soon as the fork returns, and the child must outlive it.
func TestPoolPrewarmedChildSurvivesLaunchContext(t *testing.T) {
	bin := buildOpencraft(t)
	pool := NewPool(DefaultPoolSettings()) // Prewarm 1
	defer pool.Close()
	pool.SetLauncher(func(ctx context.Context) (*Client, func(), error) {
		return LaunchExe(ctx, bin)
	})
	ctx := context.Background()
	first, err := pool.Lease(ctx, t.TempDir(), &SandboxPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if idle, _ := pool.Stats(); idle >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pool never pre-warmed a child")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The pre-warm helper (and its launch context) is long gone by now.
	time.Sleep(500 * time.Millisecond)
	second, err := pool.Lease(ctx, t.TempDir(), &SandboxPolicy{})
	if err != nil {
		t.Fatalf("lease of the pre-warmed child: %v", err)
	}
	defer func() { _ = second.Close() }()
	result, err := sandbox.Exec(ctx, second, "/bin/sh",
		[]string{"-c", "echo pooled-ok"}, sandbox.ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Stdout, "pooled-ok") {
		t.Fatalf("stdout = %q", result.Stdout)
	}
}

// TestRemoteRunnerCloseStopsRelaunchedChild pins that a child the
// watchdog relaunched is reclaimed when a pooled runner closes instead
// of leaking until process exit.
func TestRemoteRunnerCloseStopsRelaunchedChild(t *testing.T) {
	ctx := context.Background()
	leased, stopLeased, err := testChild(t)
	if err != nil {
		t.Fatal(err)
	}
	var released atomic.Int64
	runner, err := NewRemoteRunner(
		ctx, leased, stopLeased, t.TempDir(), &SandboxPolicy{},
		withWatchdog(20*time.Millisecond, 100*time.Millisecond, 1,
			[]time.Duration{10 * time.Millisecond}),
		withRelease(func() { released.Add(1) }),
	)
	if err != nil {
		t.Fatal(err)
	}
	relaunched, stopRelaunched, err := testChild(t)
	if err != nil {
		t.Fatal(err)
	}
	var stopped atomic.Int64
	runner.SetRelauncher(func() (*Client, func(), error) {
		return relaunched, func() { stopped.Add(1); stopRelaunched() }, nil
	})
	_ = leased.Close()
	deadline := time.Now().Add(5 * time.Second)
	for runner.Stats().Restarts == 0 {
		if time.Now().After(deadline) {
			t.Fatal("watchdog never restarted the child")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := runner.Close(); err != nil {
		t.Fatal(err)
	}
	if released.Load() == 0 {
		t.Fatal("pool release hook was not called")
	}
	if stopped.Load() == 0 {
		t.Fatal("relaunched child was not stopped on Close")
	}
}

// TestPoolReplacesADeadIdleChild pins the health check: an idle child
// whose transport died must not turn the next lease into a failure, the
// way it did when acquire handed out whatever sat in the idle list.
func TestPoolReplacesADeadIdleChild(t *testing.T) {
	settings := DefaultPoolSettings()
	settings.Prewarm = 0
	pool := NewPool(settings)
	defer pool.Close()
	var (
		mu      sync.Mutex
		clients []*Client
	)
	pool.SetLauncher(func(context.Context) (*Client, func(), error) {
		client, stop, err := testChild(t)
		if err == nil {
			mu.Lock()
			clients = append(clients, client)
			mu.Unlock()
		}
		return client, stop, err
	})
	ctx := context.Background()
	first, err := pool.Lease(ctx, t.TempDir(), &SandboxPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	dead := clients[0]
	mu.Unlock()
	_ = dead.Close() // the idle child's transport dies unnoticed

	second, err := pool.Lease(ctx, t.TempDir(), &SandboxPolicy{})
	if err != nil {
		t.Fatalf("lease after the idle child died: %v", err)
	}
	defer func() { _ = second.Close() }()
	mu.Lock()
	launches := len(clients)
	mu.Unlock()
	if launches != 2 {
		t.Fatalf("launches = %d, want the dead child replaced", launches)
	}
}
