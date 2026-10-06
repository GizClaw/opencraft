package host

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/testing/logcapture"
)

// fakeManagerHost builds a Host that can sit in a Manager pool without a
// real runtime behind it. It does not install it: an assembly stub has
// to be able to return a Host the pool is going to publish itself. Run
// lifecycle and teardown state are real; only the engine pieces stay
// nil.
func fakeManagerHost(m *Manager, t Target, runs int) *Host {
	h := &Host{
		target:    t,
		manager:   m,
		runs:      make(map[RunID]*runDetail),
		closeDone: make(chan struct{}),
	}
	// Only a workspace target has a work dir these tests care about;
	// the pool itself never reads it.
	if t.Kind == TargetWorkspace {
		h.workDir = t.ID
	}
	h.runsCond = sync.NewCond(&h.mu)
	for i := 0; i < runs; i++ {
		h.runs[RunID(string(rune('a'+i)))] = &runDetail{}
	}
	return h
}

// pooledFakeHost is fakeManagerHost plus the pool entry, installed the
// way assembleShared installs one: keyed by the target, with the entry
// carrying that target.
func pooledFakeHost(m *Manager, t Target, runs int) *Host {
	h := fakeManagerHost(m, t, runs)
	m.hosts[h.target.Key()] = &hostRef{host: h, target: h.target, refs: 1}
	return h
}

// finishTeardown completes the teardown contract by hand — the closed
// flag, closeDone, and the pool's own notification — for tests that
// cannot run a real Host's doClose.
func finishTeardown(m *Manager, h *Host) {
	h.mu.Lock()
	if !h.closed {
		h.closed = true
		if h.closeDone != nil {
			close(h.closeDone)
		}
	}
	h.mu.Unlock()
	m.hostClosed(h.target, h)
}

// recordingClose installs a closeHost that synchronously finishes the
// teardown contract (closed flag + closeDone + manager notification)
// and reports every closed Host on closed.
func recordingClose(m *Manager, closed chan *Host) {
	m.closeHost = func(h *Host) {
		closed <- h
		finishTeardown(m, h)
	}
}

func TestInvalidateDefersBusyHost(t *testing.T) {
	m := NewManagerAt(t.TempDir(), t.TempDir())
	closed := make(chan *Host, 1)
	recordingClose(m, closed)
	h := pooledFakeHost(m, WorkspaceTarget("/workspace/a"), 1)

	m.Invalidate(context.Background(), h.target)

	ref := m.hosts[h.target.Key()]
	if ref == nil {
		t.Fatal("busy host was removed from the pool while still running")
	}
	if !ref.stale {
		t.Fatal("busy host was not marked stale")
	}
	if !h.IsStale() {
		t.Fatal("busy host did not record its retirement flag")
	}
	select {
	case <-closed:
		t.Fatal("busy host was closed while still running")
	default:
	}
}

func TestInvalidateClosesIdleHost(t *testing.T) {
	m := NewManagerAt(t.TempDir(), t.TempDir())
	closed := make(chan *Host, 1)
	recordingClose(m, closed)
	h := pooledFakeHost(m, WorkspaceTarget("/workspace/b"), 0)

	m.Invalidate(context.Background(), h.target)

	if m.hosts[h.target.Key()] != nil {
		t.Fatal("idle host stayed in the pool")
	}
	if got := <-closed; got != h {
		t.Fatalf("closed host = %p, want %p", got, h)
	}
	if !h.closed {
		t.Fatal("idle host was not closed")
	}
	if m.retiring[h.target.Key()] != nil {
		t.Fatal("retiring entry was not cleaned up after close")
	}
}

func TestStaleHostRetiresWhenLastRunEnds(t *testing.T) {
	m := NewManagerAt(t.TempDir(), t.TempDir())
	closed := make(chan *Host, 1)
	recordingClose(m, closed)
	h := pooledFakeHost(m, WorkspaceTarget("/workspace/c"), 1)

	m.Invalidate(context.Background(), h.target)
	if m.hosts[h.target.Key()] == nil {
		t.Fatal("busy host left the pool at invalidate time")
	}
	if !h.IsStale() {
		t.Fatal("busy host did not record its retirement flag")
	}

	var runID RunID
	for id := range h.runs {
		runID = id
		break
	}
	h.dropRun(runID)

	if m.hosts[h.target.Key()] != nil {
		t.Fatal("stale host stayed in the pool after its last run ended")
	}
	if got := <-closed; got != h {
		t.Fatalf("closed host = %p, want %p", got, h)
	}
	if !h.closed {
		t.Fatal("stale host was not closed after becoming idle")
	}
	if !h.IsStale() {
		t.Fatal("retired host lost its retirement flag")
	}
}

func TestHostIdleIgnoresNonStaleHost(t *testing.T) {
	m := NewManagerAt(t.TempDir(), t.TempDir())
	closed := make(chan *Host, 1)
	recordingClose(m, closed)
	h := pooledFakeHost(m, WorkspaceTarget("/workspace/d"), 1)

	var runID RunID
	for id := range h.runs {
		runID = id
		break
	}
	h.dropRun(runID)

	if m.hosts[h.target.Key()] == nil {
		t.Fatal("non-stale host left the pool when it became idle")
	}
	select {
	case <-closed:
		t.Fatal("non-stale host was closed when it became idle")
	default:
	}
}

// TestCurrentKeepsReportingTheHostThatIsRetiring pins the read every
// drain path is built on and no other test in this file can observe:
// the fixtures that record teardown finish it synchronously, so the
// window in which a Host has left the pool's supply side and is not yet
// gone is invisible to them. Current answers with that Host — which is
// what Ensure hands out while the old generation drains and what
// ScheduleReplacement reads to tell "something is draining" from
// "nothing is coming".
func TestCurrentKeepsReportingTheHostThatIsRetiring(t *testing.T) {
	m := NewManagerAt(t.TempDir(), t.TempDir())
	// Teardown is requested and deliberately left unfinished.
	m.closeHost = func(*Host) {}
	h := pooledFakeHost(m, WorkspaceTarget("/workspace/e"), 1)

	m.Invalidate(context.Background(), h.target)
	if got := m.Current(h.target); got != h {
		t.Fatalf("Current = %p, want the stale-but-live host %p", got, h)
	}

	// The last run ends: out of the pool, into the retiring set, and
	// still the target's Host.
	var runID RunID
	for id := range h.runs {
		runID = id
		break
	}
	h.dropRun(runID)
	if m.hosts[h.target.Key()] != nil {
		t.Fatal("a stale host stayed in the pool after its last run ended")
	}
	if m.retiring[h.target.Key()] != h {
		t.Fatal("the host that left the pool is not recorded as retiring")
	}
	if got := m.Current(h.target); got != h {
		t.Fatalf("Current = %p, want the retiring host %p", got, h)
	}
	if h.IsClosing() {
		t.Fatal("a host that is merely retiring reports itself as closing")
	}

	// Work asked for during the teardown reuses that generation rather
	// than assembling a second runtime for the same target.
	var builds int
	m.assembleHost = func(
		context.Context, Target, interact.Backend, func(string) interact.Backend,
	) (*Host, error) {
		builds++
		return nil, errors.New("assembled while the old host was retiring")
	}
	// Bounded on purpose: the healthy path answers from the pool at
	// once, while an Ensure that decided to wait out the drain instead
	// would fail here rather than hang the package.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	got, err := m.Ensure(ctx, h.target)
	if err != nil {
		t.Fatalf("Ensure during teardown: %v", err)
	}
	if got != h || builds != 0 {
		t.Fatalf("Ensure = %p after %d assemblies, want the retiring host %p",
			got, builds, h)
	}

	// Teardown finishes: only now is the target unserved.
	finishTeardown(m, h)
	if m.retiring[h.target.Key()] != nil {
		t.Fatal("the retiring entry survived teardown")
	}
	if got := m.Current(h.target); got != nil {
		t.Fatalf("Current after teardown = %p, want no Host", got)
	}
}

// TestAcquireWaitsOutTheDrain pins the other entry point's half of the
// same rule: Ensure answers from the retiring generation, but Acquire —
// which exists to hand out a Host for new work — never assembles a
// second runtime for a target whose previous one is still draining. It
// parks on the teardown and assembles once, after it.
func TestAcquireWaitsOutTheDrain(t *testing.T) {
	m := NewManagerAt(t.TempDir(), t.TempDir())
	// Teardown is requested and deliberately left unfinished.
	m.closeHost = func(*Host) {}
	target := WorkspaceTarget("/workspace/draining")
	busy := pooledFakeHost(m, target, 1)

	// Invalidate finds the Host busy, so it stays in the pool, marked
	// stale; the last run ending is what moves it out and starts the
	// teardown.
	m.Invalidate(context.Background(), target)
	var runID RunID
	for id := range busy.runs {
		runID = id
		break
	}
	busy.dropRun(runID)
	if m.retiring[target.Key()] != busy {
		t.Fatalf("the drained host is not the retiring generation: %v", m.retiring)
	}

	var builds int
	m.assembleHost = func(
		_ context.Context, t Target, _ interact.Backend, _ func(string) interact.Backend,
	) (*Host, error) {
		builds++
		return fakeManagerHost(m, t, 0), nil
	}

	// Acquire is called while that teardown is still in flight. Bounded
	// on purpose: a pool that waited forever fails here instead of
	// hanging the package.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	type result struct {
		host *Host
		err  error
	}
	done := make(chan result, 1)
	go func() {
		h, err := m.Acquire(ctx, target, interact.Auto{}, nil)
		done <- result{host: h, err: err}
	}()
	// A grace period is the honest way to observe a block from the
	// outside: nothing may be assembled while the old generation is
	// still finishing.
	time.Sleep(50 * time.Millisecond)
	if builds != 0 {
		t.Fatalf("the assembler ran %d times while the target was draining", builds)
	}

	// Teardown finishes: only now is the replacement assembled, and the
	// caller gets it — not the Host that is leaving.
	finishTeardown(m, busy)
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("Acquire after the drain: %v", got.err)
		}
		if got.host == nil || got.host == busy {
			t.Fatalf("Acquire = %p, want a fresh host", got.host)
		}
		if got.host.Target() != target {
			t.Fatalf("the replacement serves %s, want %s", got.host.Target(), target)
		}
	case <-time.After(waitForDeadline):
		t.Fatal("Acquire never returned after the teardown finished")
	}
	if builds != 1 {
		t.Fatalf("assemblies = %d, want exactly one replacement", builds)
	}
}

func TestWaitClosedHonorsContextCancel(t *testing.T) {
	h := &Host{closeDone: make(chan struct{})}
	h.runsCond = sync.NewCond(&h.mu)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.WaitClosed(ctx); err == nil {
		t.Fatal("WaitClosed returned nil on a canceled context")
	}
}

// TestInvalidateLogsAttribution pins the diagnostic contract item 2.0
// exists for: every invalidation names the caller that asked for it,
// the workspace it tore down, and whether it was deferred behind live
// runs. A rebuild storm has to be answerable from the log alone.
func TestInvalidateLogsAttribution(t *testing.T) {
	recorder := logcapture.Install(t)
	m := NewManagerAt(t.TempDir(), t.TempDir())
	closed := make(chan *Host, 1)
	recordingClose(m, closed)
	h := pooledFakeHost(m, WorkspaceTarget("/workspace/logged"), 1)

	ctx := WithAssemblyReason(context.Background(), ReasonSettingsSave)
	m.Invalidate(ctx, h.target)

	var found bool
	for _, record := range recorder.Records() {
		if record.Body().AsString() != "host: runtime invalidated" {
			continue
		}
		found = true
		if got := logcapture.Attribute(record, "reason"); got != "settings_save" {
			t.Fatalf("reason = %q, want settings_save", got)
		}
		// The line reports a target, not a workspace: an application's
		// Host is invalidated by the same code, and its id is not a
		// directory.
		if got := logcapture.Attribute(record, "target"); got != h.target.String() {
			t.Fatalf("target = %q, want %q", got, h.target)
		}
		if got := logcapture.Attribute(record, "in_turn"); got != "true" {
			t.Fatalf("in_turn = %q, want true", got)
		}
		if got := logcapture.Attribute(record, "deferred"); got != "true" {
			t.Fatalf("deferred = %q, want true", got)
		}
	}
	if !found {
		t.Fatalf("no invalidation log record emitted; bodies: %v",
			recorder.Bodies())
	}
}
