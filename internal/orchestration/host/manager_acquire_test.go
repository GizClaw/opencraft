package host

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
	m.closeHost = func(h *Host) {
		h.mu.Lock()
		if !h.closed {
			h.closed = true
			if h.closeDone != nil {
				close(h.closeDone)
			}
		}
		h.mu.Unlock()
		m.hostClosed(h.workDir, h)
	}
	m.assembleHost = func(
		_ context.Context,
		workDir string,
		_ interact.Backend,
		_ func(string) interact.Backend,
	) (*Host, error) {
		builds.Add(1)
		<-release
		if fail != nil {
			return nil, fail
		}
		return fakeManagerHost(m, workDir, 0), nil
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
				context.Background(), workDir, interact.Auto{}, nil)
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
	ref := m.hosts[workDir]
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
				context.Background(), workDir, interact.Auto{}, nil)
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
		workDir string,
		_ interact.Backend,
		_ func(string) interact.Backend,
	) (*Host, error) {
		builds.Add(1)
		return fakeManagerHost(m, workDir, 0), nil
	}
	h, err := m.Acquire(context.Background(), workDir, interact.Auto{}, nil)
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
