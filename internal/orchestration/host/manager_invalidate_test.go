package host

import (
	"context"
	"sync"
	"testing"
)

// fakeManagerHost builds a Host that can sit in a Manager pool without
// a real runtime behind it. Run lifecycle and teardown state are real;
// only the engine pieces stay nil.
func fakeManagerHost(m *Manager, workDir string, runs int) *Host {
	h := &Host{
		workDir:   workDir,
		manager:   m,
		runs:      make(map[RunID]*runDetail),
		closeDone: make(chan struct{}),
	}
	h.runsCond = sync.NewCond(&h.mu)
	for i := 0; i < runs; i++ {
		h.runs[RunID(string(rune('a'+i)))] = &runDetail{}
	}
	return h
}

// recordingClose installs a closeHost that synchronously finishes the
// teardown contract (closed flag + closeDone + manager notification)
// and reports every closed Host on closed.
func recordingClose(m *Manager, closed chan *Host) {
	m.closeHost = func(h *Host) {
		closed <- h
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
}

func TestInvalidateDefersBusyHost(t *testing.T) {
	m := NewManagerAt(t.TempDir(), t.TempDir())
	closed := make(chan *Host, 1)
	recordingClose(m, closed)
	h := fakeManagerHost(m, "/workspace/a", 1)
	m.hosts[h.workDir] = &hostRef{host: h, refs: 1}

	m.Invalidate(h.workDir)

	ref := m.hosts[h.workDir]
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
	h := fakeManagerHost(m, "/workspace/b", 0)
	m.hosts[h.workDir] = &hostRef{host: h, refs: 1}

	m.Invalidate(h.workDir)

	if m.hosts[h.workDir] != nil {
		t.Fatal("idle host stayed in the pool")
	}
	if got := <-closed; got != h {
		t.Fatalf("closed host = %p, want %p", got, h)
	}
	if !h.closed {
		t.Fatal("idle host was not closed")
	}
	if m.retiring[h.workDir] != nil {
		t.Fatal("retiring entry was not cleaned up after close")
	}
}

func TestStaleHostRetiresWhenLastRunEnds(t *testing.T) {
	m := NewManagerAt(t.TempDir(), t.TempDir())
	closed := make(chan *Host, 1)
	recordingClose(m, closed)
	h := fakeManagerHost(m, "/workspace/c", 1)
	m.hosts[h.workDir] = &hostRef{host: h, refs: 1}

	m.Invalidate(h.workDir)
	if m.hosts[h.workDir] == nil {
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

	if m.hosts[h.workDir] != nil {
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
	h := fakeManagerHost(m, "/workspace/d", 1)
	m.hosts[h.workDir] = &hostRef{host: h, refs: 1}

	var runID RunID
	for id := range h.runs {
		runID = id
		break
	}
	h.dropRun(runID)

	if m.hosts[h.workDir] == nil {
		t.Fatal("non-stale host left the pool when it became idle")
	}
	select {
	case <-closed:
		t.Fatal("non-stale host was closed when it became idle")
	default:
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
