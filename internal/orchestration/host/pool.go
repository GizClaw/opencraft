// Pool lifecycle: which workspace gets which Host, how a stale Host is
// retired, and how the pool drains on shutdown.

package host

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/orchestration/interact"

	otellog "go.opentelemetry.io/otel/log"
)

// InvalidateAll drops every pooled Host. Idle hosts close immediately;
// hosts with active runs finish on the old runtime and close after the
// last run ends.
func (m *Manager) InvalidateAll(ctx context.Context) {
	m.mu.Lock()
	dirs := make([]string, 0, len(m.hosts))
	for wd := range m.hosts {
		dirs = append(dirs, wd)
	}
	m.mu.Unlock()
	for _, wd := range dirs {
		m.Invalidate(ctx, wd)
	}
}

// Acquire returns (creating if needed) the shared Host for workDir.
// fallback is used for runs without a resolver hit. Concurrent callers
// for one workspace share a single assembly: whoever gets there first
// builds, the rest wait and reuse the result. Every Host the pool hands
// out has the host configurator applied first.
func (m *Manager) Acquire(
	ctx context.Context,
	workDir string,
	fallback interact.Backend,
	resolver func(runID string) interact.Backend,
) (*Host, error) {
	h, err := m.acquire(ctx, workDir, fallback, resolver)
	if err != nil {
		return nil, err
	}
	m.configureHost(h)
	return h, nil
}

// acquire is Acquire's pool half: it resolves or assembles the Host and
// leaves the configurator to the caller.
func (m *Manager) acquire(
	ctx context.Context,
	workDir string,
	fallback interact.Backend,
	resolver func(runID string) interact.Backend,
) (*Host, error) {
	workDir = filepath.Clean(workDir)
	for {
		m.mu.Lock()
		if ref := m.hosts[workDir]; ref != nil {
			ref.refs++
			h := ref.host
			m.mu.Unlock()
			return h, nil
		}
		if h := m.retiring[workDir]; h != nil {
			m.mu.Unlock()
			if err := h.waitClosed(ctx); err != nil {
				return nil, err
			}
			continue
		}
		call := m.assembling[workDir]
		if call == nil {
			call = &assemblyCall{done: make(chan struct{})}
			if m.assembling == nil {
				m.assembling = make(map[string]*assemblyCall)
			}
			m.assembling[workDir] = call
			m.mu.Unlock()
			return m.assembleShared(
				ctx, workDir, fallback, resolver, call)
		}
		m.mu.Unlock()
		select {
		case <-call.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if call.err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// A leader cancelled by its own caller is not this caller's
			// failure, and ours is still live: the next pass assembles.
			// Any other error is shared rather than retried once per
			// waiter.
			if !errors.Is(call.err, context.Canceled) &&
				!errors.Is(call.err, context.DeadlineExceeded) {
				return nil, call.err
			}
		}
	}
}

// Current returns the Host that serves workDir right now: the pooled
// Host when one is installed — stale ones included, because a stale
// Host keeps serving its live runs on the old assembly — or the Host
// that is retiring out of the pool while those runs finish. Nil when
// the workspace has no Host at all: never assembled, or fully torn
// down.
//
// This is the pool's answer to "which generation serves this
// workspace", and it is deliberately per workspace: no process-wide
// current Host means a background acquire cannot make a read for the
// window's workspace land on another workspace's store.
func (m *Manager) Current(workDir string) *Host {
	if strings.TrimSpace(workDir) == "" {
		return nil
	}
	workDir = filepath.Clean(workDir)
	m.mu.Lock()
	defer m.mu.Unlock()
	if ref := m.hosts[workDir]; ref != nil {
		return ref.host
	}
	return m.retiring[workDir]
}

// Ensure returns a Host for workDir that can serve new work: the pooled
// Host when it is live (a stale one still serving its last runs
// counts), and otherwise a fresh assembly, once any retiring Host for
// the workspace has finished teardown. It never assembles a second Host
// while one is still draining, and never hands out another workspace's.
// A Host it returns is wired with the host configurator, whether it
// came out of the pool or from an assembly this call started.
//
// Programmatic callers make up this pool's supply side, so the assembly
// runs on the backend they all use (interact.Auto); a caller that needs
// its own fallback backend for the runs on that Host goes through
// Acquire instead.
func (m *Manager) Ensure(ctx context.Context, workDir string) (*Host, error) {
	if strings.TrimSpace(workDir) == "" {
		return nil, ErrNoWorkspace
	}
	if h := m.Current(workDir); h != nil && !h.IsClosing() {
		// The pooled branch hands out a Host the assembler may not
		// have reached yet (it configures after publishing), so the
		// configurator is applied here too: no hand-out path leaves a
		// Host that can serve runs unwired.
		m.configureHost(h)
		return h, nil
	}
	return m.Acquire(ctx, workDir, interact.Auto{}, nil)
}

// Invalidate marks one workspace's Host as stale (the rebuild path).
// An idle Host closes immediately; a Host with active runs stays
// pooled and keeps serving new turns on the old runtime until the last
// run ends, then retires itself through hostIdle. This defers
// engine-input swaps to idle so a second Host (and a second flowcraft
// Session for the same conversation) is never assembled while the old
// runtime still has live runs.
func (m *Manager) Invalidate(ctx context.Context, workDir string) {
	workDir = filepath.Clean(workDir)
	m.mu.Lock()
	ref := m.hosts[workDir]
	if ref == nil {
		m.mu.Unlock()
		return
	}
	ref.stale = true
	h := ref.host
	h.markStale()
	active := h.hasActiveRuns()
	var closeNow bool
	if !active {
		delete(m.hosts, workDir)
		closeNow = m.trackRetiringLocked(workDir, h)
	}
	m.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	// The reason names what asked for the rebuild, not what is being
	// torn down: a storm of invalidations all pointing at one caller is
	// the signal this line exists for.
	telemetry.Info(ctx, "host: runtime invalidated",
		otellog.String("reason", string(AssemblyReasonFrom(ctx))),
		otellog.String("workspace", workDir),
		otellog.Bool("in_turn", active),
		otellog.Bool("deferred", !closeNow),
		otellog.String("host_ptr", fmt.Sprintf("%p", h)))
	if closeNow {
		m.closeHost(h)
	}
}

// hostIdle is called by a Host when its last active run ends. A stale
// Host retires (and closes) here instead of at Invalidate time, which
// keeps the pool free of duplicate Hosts across reload boundaries.
func (m *Manager) hostIdle(h *Host) {
	if m == nil || h == nil {
		return
	}
	m.mu.Lock()
	var closeNow bool
	for workDir, ref := range m.hosts {
		if ref.host != h || !ref.stale {
			continue
		}
		delete(m.hosts, workDir)
		closeNow = m.trackRetiringLocked(workDir, h)
		break
	}
	m.mu.Unlock()
	if closeNow {
		m.closeHost(h)
	}
}

// hostClosed forgets a fully torn-down Host so Acquire can assemble a
// replacement. Host.doClose reports itself through this hook.
func (m *Manager) hostClosed(workDir string, h *Host) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.retiring != nil && m.retiring[workDir] == h {
		delete(m.retiring, workDir)
	}
	m.mu.Unlock()
}

// trackRetiringLocked records a Host that is leaving the pool. The
// caller must hold m.mu and must have removed the host from m.hosts.
// It returns true when closeHost should be invoked after unlocking.
func (m *Manager) trackRetiringLocked(workDir string, h *Host) bool {
	if m.retiring == nil {
		m.retiring = make(map[string]*Host)
	}
	if m.retiring[workDir] != nil {
		return false
	}
	m.retiring[workDir] = h
	return true
}

// CloseAll invalidates every pooled Host. Active runs finish on their
// old runtime in the background, so callers that must stop promptly
// should CancelAll first.
func (m *Manager) CloseAll() {
	m.InvalidateAll(context.Background())
}

// CancelAll cancels every live run on every pooled Host.
func (m *Manager) CancelAll() {
	m.mu.Lock()
	hosts := make([]*Host, 0, len(m.hosts))
	for _, ref := range m.hosts {
		hosts = append(hosts, ref.host)
	}
	m.mu.Unlock()
	for _, h := range hosts {
		h.CancelAll()
	}
}

// anyActiveRuns reports whether any pooled or draining Host has a live
// run. An assembly that overlaps a run is one the user paid for inside
// a turn.
func (m *Manager) anyActiveRuns() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ref := range m.hosts {
		if ref.host.hasActiveRuns() {
			return true
		}
	}
	for _, h := range m.retiring {
		if h.hasActiveRuns() {
			return true
		}
	}
	return false
}
