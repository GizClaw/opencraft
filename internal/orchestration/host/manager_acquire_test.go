package host

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/orchestration/engine"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
)

// blockingAssembler installs an assembly stub that blocks until
// release is closed and counts how many builds actually ran.
func blockingAssembler(
	t *testing.T,
	m *Manager,
	release chan struct{},
	builds *atomic.Int32,
	fail error,
) {
	t.Helper()
	m.closeHost = func(h *Host) { finishTeardown(m, h) }
	m.assembleHost = func(
		_ context.Context,
		t Target,
		_ interact.Backend,
		_ func(string) interact.Backend,
	) (*Host, error) {
		builds.Add(1)
		<-release
		if fail != nil {
			return nil, fail
		}
		// The Host a builder returns carries the target it was asked
		// for — assembleShared refuses one that does not.
		return fakeManagerHost(m, t, 0), nil
	}
}

// misTargetedHost builds a Host a real teardown can run on without a
// runtime behind it: the engine controller and broker a close touches
// are present as zero values, everything else is a fake. It is how a
// test observes what happens to a Host the pool refuses to publish.
func misTargetedHost(m *Manager, t Target) *Host {
	h := &Host{
		target:    t,
		manager:   m,
		runs:      make(map[RunID]*runDetail),
		closeDone: make(chan struct{}),
		broker:    &interact.Broker{},
		ctrl:      &engine.Controller{},
	}
	h.runsCond = sync.NewCond(&h.mu)
	return h
}

// TestAssembleRejectsHostForAnotherTarget pins the identity check every
// builder's answer goes through: a Host for the wrong target must never
// be published under the requested target's key. The pool would hand it
// out as if it served the request, and its own Close would look up a
// pool entry that was never written — so the mismatch fails the lookup
// instead, naming both targets, and the mis-targeted Host is torn down
// rather than leaked.
func TestAssembleRejectsHostForAnotherTarget(t *testing.T) {
	m := NewManagerAt(t.TempDir(), t.TempDir())
	asked := WorkspaceTarget("/workspace/asked")
	answered := WorkspaceTarget("/workspace/elsewhere")
	stray := misTargetedHost(m, answered)
	m.assembleHost = func(
		context.Context, Target, interact.Backend, func(string) interact.Backend,
	) (*Host, error) {
		return stray, nil
	}

	_, err := m.Ensure(context.Background(), asked)
	if err == nil {
		t.Fatal("a host built for another target was accepted as this one's")
	}
	for _, want := range []string{answered.String(), asked.String()} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal = %q, want it to name both targets (%q)", err, want)
		}
	}
	if !stray.closed {
		t.Fatal("the mis-targeted host was not torn down")
	}
	if m.hosts[asked.Key()] != nil || m.hosts[answered.Key()] != nil {
		t.Fatalf("the mis-targeted host reached the pool: %v", m.hosts)
	}
	if m.Current(asked) != nil || m.Current(answered) != nil {
		t.Fatal("the mis-targeted host was published as a target's host")
	}
}

// TestAssembleRejectsEmptyResult pins the other half of the same check:
// a builder that reports no error and hands back no Host is not "here
// is your Host". Publishing it would leave the target looking served to
// every reader while Acquire returns a nil Host to a caller about to
// start a run on it.
func TestAssembleRejectsEmptyResult(t *testing.T) {
	m := NewManagerAt(t.TempDir(), t.TempDir())
	target := WorkspaceTarget("/workspace/empty")
	m.assembleHost = func(
		context.Context, Target, interact.Backend, func(string) interact.Backend,
	) (*Host, error) {
		return nil, nil
	}

	h, err := m.Ensure(context.Background(), target)
	if err == nil {
		t.Fatalf("an empty assembly was accepted as %s's: %p", target, h)
	}
	if !strings.Contains(err.Error(), target.String()) {
		t.Fatalf("refusal = %q, want it to name %s", err, target)
	}
	if ref := m.hosts[target.Key()]; ref != nil {
		t.Fatalf("the empty assembly left pool entry %+v", ref)
	}
	if got := m.Current(target); got != nil {
		t.Fatalf("Current = %p, want the target unserved", got)
	}
}

// TestAcquireSharesOneAssembly pins the single-flight contract behind a
// rebuild storm: a burst of Acquire calls for one workspace runs one
// assembly, and every caller receives the same Host with its own
// reference. Before this, each waker built a runtime and all but the
// first were closed again.
func TestAcquireSharesOneAssembly(t *testing.T) {
	m := NewManagerAt(t.TempDir(), t.TempDir())
	release := make(chan struct{})
	var builds atomic.Int32
	blockingAssembler(t, m, release, &builds, nil)

	const callers = 5
	workDir := "/workspace/single-flight"
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		hosts   []*Host
		errs    []error
		started = make(chan struct{})
	)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-started
			h, err := m.Acquire(
				context.Background(), WorkspaceTarget(workDir), interact.Auto{}, nil)
			mu.Lock()
			defer mu.Unlock()
			hosts = append(hosts, h)
			errs = append(errs, err)
		}()
	}
	close(started)
	// Give every caller time to reach the in-flight assembly before the
	// leader is allowed to finish, so they all wait on the same call.
	deadline := time.Now().Add(5 * time.Second)
	for builds.Load() == 0 || m.pendingAssemblies() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no assembly started")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := builds.Load(); got != 1 {
		t.Fatalf("assemblies = %d, want 1 (concurrent Acquire must share)", got)
	}
	mu.Lock()
	defer mu.Unlock()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	if len(hosts) != callers {
		t.Fatalf("hosts returned = %d, want %d", len(hosts), callers)
	}
	for i, h := range hosts {
		if h != hosts[0] {
			t.Fatalf("caller %d got host %p, want %p", i, h, hosts[0])
		}
	}
	ref := m.hosts[WorkspaceTarget(workDir).Key()]
	if ref == nil {
		t.Fatal("assembled host never reached the pool")
	}
	if ref.refs != callers {
		t.Fatalf("pooled refs = %d, want %d", ref.refs, callers)
	}
}

// TestAcquireRetriesAfterFailedAssembly pins the other half of the
// contract: a failed build is shared with the callers that were already
// waiting (so one broken configuration is reported once), and the next
// Acquire retries instead of inheriting the failure forever.
func TestAcquireRetriesAfterFailedAssembly(t *testing.T) {
	m := NewManagerAt(t.TempDir(), t.TempDir())
	release := make(chan struct{})
	close(release)
	var builds atomic.Int32
	buildErr := errors.New("assembly refused")
	blockingAssembler(t, m, release, &builds, buildErr)

	workDir := "/workspace/retry"
	// Two callers share the first failure.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = m.Acquire(
				context.Background(), WorkspaceTarget(workDir), interact.Auto{}, nil)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if !errors.Is(err, buildErr) {
			t.Fatalf("caller %d error = %v, want %v", i, err, buildErr)
		}
	}
	if first := builds.Load(); first == 0 {
		t.Fatal("assembly stub never ran")
	}
	if m.pendingAssemblies() != 0 {
		t.Fatal("failed assembly left an in-flight entry behind")
	}

	// The failure is not sticky: the next caller assembles again.
	builds.Store(0)
	m.assembleHost = func(
		_ context.Context,
		t Target,
		_ interact.Backend,
		_ func(string) interact.Backend,
	) (*Host, error) {
		builds.Add(1)
		return fakeManagerHost(m, t, 0), nil
	}
	h, err := m.Acquire(context.Background(), WorkspaceTarget(workDir), interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("retry after failure: %v", err)
	}
	if h == nil {
		t.Fatal("retry after failure returned no host")
	}
	if got := builds.Load(); got != 1 {
		t.Fatalf("retry assemblies = %d, want 1", got)
	}
}

// pendingAssemblies reports how many workspaces have an in-flight
// assembly, for tests only.
func (m *Manager) pendingAssemblies() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.assembling)
}

// TestHostConfiguratorAppliesOncePerPooledHost pins the contract the
// adapter's UI wiring depends on: the callback runs when a Host enters
// the pool, once per Host rather than once per Acquire, and the marker
// rides the pool entry — so a Host the pool has forgotten stops being
// referenced by the bookkeeping that configured it (see
// TestRebuiltHostIsCollectable in the desktop core package).
func TestHostConfiguratorAppliesOncePerPooledHost(t *testing.T) {
	m := NewManagerAt(t.TempDir(), t.TempDir())
	release := make(chan struct{})
	close(release)
	var builds atomic.Int32
	blockingAssembler(t, m, release, &builds, nil)

	var (
		mu         sync.Mutex
		configured []*Host
	)
	m.SetHostConfigurator(func(h *Host) {
		mu.Lock()
		defer mu.Unlock()
		configured = append(configured, h)
	})

	workDir := "/workspace/configure-once"
	ctx := context.Background()
	first, err := m.Acquire(ctx, WorkspaceTarget(workDir), interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	second, err := m.Acquire(ctx, WorkspaceTarget(workDir), interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if second != first {
		t.Fatalf("second acquire returned %p, want the pooled %p",
			second, first)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(configured) != 1 {
		t.Fatalf("configurator ran %d times for one pooled Host, want 1",
			len(configured))
	}
	if configured[0] != first {
		t.Fatalf("configurator ran on %p, want the pooled Host %p",
			configured[0], first)
	}
}

// TestEnsureWiresAHostThePoolAlreadyHeld pins the other half of the
// configurator contract: no hand-out path returns a Host that can serve
// runs unconfigured. Ensure's pooled branch is the one that could: it
// returns a Host the assembler has not reached yet (Acquire configures
// after publishing), and one that was pooled before the configurator
// was installed is reachable the same way. Handing either out unwired
// means the adapter never sees that Host's artifacts or session
// updates, for the whole life of a run it starts — a run that outlives
// the assembly change by design.
func TestEnsureWiresAHostThePoolAlreadyHeld(t *testing.T) {
	m := NewManagerAt(t.TempDir(), t.TempDir())
	release := make(chan struct{})
	close(release)
	var builds atomic.Int32
	blockingAssembler(t, m, release, &builds, nil)

	workDir := "/workspace/configure-on-ensure"
	ctx := context.Background()
	pooled, err := m.Acquire(ctx, WorkspaceTarget(workDir), interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	var (
		mu         sync.Mutex
		configured []*Host
	)
	m.SetHostConfigurator(func(h *Host) {
		mu.Lock()
		defer mu.Unlock()
		configured = append(configured, h)
	})

	h, err := m.Ensure(ctx, WorkspaceTarget(workDir))
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if h != pooled {
		t.Fatalf("ensure returned %p, want the pooled %p", h, pooled)
	}
	mu.Lock()
	ran := len(configured)
	var wired *Host
	if ran > 0 {
		wired = configured[0]
	}
	mu.Unlock()
	if ran != 1 || wired != pooled {
		t.Fatalf("configurator ran %d times (first %p), want once on %p",
			ran, wired, pooled)
	}

	if _, err := m.Ensure(ctx, WorkspaceTarget(workDir)); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(configured) != 1 {
		t.Fatalf("configurator ran %d times for one pooled Host, want 1",
			len(configured))
	}
}
