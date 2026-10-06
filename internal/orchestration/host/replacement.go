package host

import (
	"context"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"
)

// ReplacementHooks are the adapter's half of the deferred rebuild. The
// pool decides when a replacement can be assembled — only after the
// retiring Host has finished teardown — and these hooks decide whether
// it still should be, and what to announce once it is. The alternative,
// a caller-side watcher per reload, is what this replaces: nothing
// outside the pool knows who is draining or has already retired.
type ReplacementHooks struct {
	// Wanted reports whether t still wants a runtime: the window has
	// not left it while the old assembly drained. Nil means every
	// target is wanted. An adapter that answers false keeps the pool
	// from assembling a runtime nobody is going to look at, at the
	// moment the user has just moved to another workspace.
	Wanted func(t Target) bool
	// Installed is notified after a replacement was assembled and
	// pooled, so the adapter can refresh whatever it renders from the
	// document. Nil means no notification. It runs on the pool's
	// watcher goroutine and must not block.
	Installed func(t Target)
}

// SetReplacementHooks installs the deferred-rebuild policy. The
// composition root installs it before the first reload.
func (m *Manager) SetReplacementHooks(h ReplacementHooks) {
	m.mu.Lock()
	m.replacementHooks = h
	m.mu.Unlock()
}

// ReplacementArmed reports whether a replacement is already scheduled
// for one target. A caller deciding whether the document it holds still
// needs a rebuild asks this to tell "the generation in use is stale and
// its replacement is on the way" from "it is stale and nothing is
// coming". An unnamed target is never armed, so it answers false — a
// target that names nothing has nothing draining.
func (m *Manager) ReplacementArmed(t Target) bool {
	if !t.Valid() {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.armed[t.Key()]
	return ok
}

// ScheduleReplacement arms the deferred replacement of one target's
// assembly and reports whether this call armed it — false means one was
// already armed, i.e. the drain in progress is already accounted for.
//
// It is how a reload handles a Host that is stale but still serving
// live runs: the running turn keeps the assembly it started on, and a
// fresh Host is assembled as soon as that one has finished teardown.
// One replacement per workspace on purpose. A settings save, a
// workspace switch and a plugin write can all invalidate the same
// workspace inside one drain; with a watcher per invalidation they all
// woke at once and assembled a runtime each — one installed, the rest
// built and thrown away. The single armed watcher always assembles from
// the document on disk at the time it runs, so a later invalidation has
// nothing left to ask for. An unnamed target is not armed: false, the
// same answer a second arm gets, because there is no drain to wait for.
func (m *Manager) ScheduleReplacement(ctx context.Context, t Target) bool {
	if !t.Valid() {
		return false
	}
	key := t.Key()
	m.mu.Lock()
	if _, ok := m.armed[key]; ok {
		m.mu.Unlock()
		return false
	}
	if m.armed == nil {
		m.armed = make(map[string]struct{})
	}
	m.armed[key] = struct{}{}
	m.mu.Unlock()
	go m.replaceAfterDrain(context.WithoutCancel(ctx), t)
	return true
}

// disarmReplacement releases the armed slot once the drain settled, so
// a later reload can arm a new replacement.
func (m *Manager) disarmReplacement(t Target) {
	m.mu.Lock()
	delete(m.armed, t.Key())
	m.mu.Unlock()
}

// replaceAfterDrain waits for the target's retiring Host to finish
// teardown, then assembles its replacement. A stale Host keeps serving
// new turns on its old assembly until its last run ends, so the
// replacement cannot be built any earlier without a second runtime
// serving the same target.
func (m *Manager) replaceAfterDrain(ctx context.Context, t Target) {
	defer m.disarmReplacement(t)
	if old := m.Current(t); old != nil {
		if !old.IsStale() && !old.IsClosing() {
			// A live Host serves the target: nothing was draining,
			// so there is no replacement to schedule.
			return
		}
		// WaitClosed only fails on a canceled context, and this wait
		// has no deadline of its own: the drain ends with the
		// target's last live run.
		if err := old.WaitClosed(ctx); err != nil {
			return
		}
	}
	// Current answering nil is not the "nothing was draining" case: it
	// means the target has no Host at all, because the retired one
	// finished teardown between the arm and this watcher. That is
	// exactly what the replacement is for — a target left unserved
	// shows the window an empty session list until the next turn or
	// rebuild.
	if !m.replacementWanted(t) {
		return
	}
	ctx = WithAssemblyReason(ctx, ReasonRetryAfterDrain)
	h, err := m.Ensure(ctx, t)
	if err != nil {
		telemetry.WarnErr(ctx, "host: deferred rebuild failed", err,
			otellog.String("target", t.String()))
		return
	}
	if h == nil {
		return
	}
	if installed := m.replacementInstalled(); installed != nil {
		installed(t)
	}
}

// replacementWanted consults the adapter's Wanted hook; a target no
// hook answers for is wanted.
func (m *Manager) replacementWanted(t Target) bool {
	m.mu.Lock()
	fn := m.replacementHooks.Wanted
	m.mu.Unlock()
	return fn == nil || fn(t)
}

// replacementInstalled returns the adapter's Installed hook, if any.
func (m *Manager) replacementInstalled() func(Target) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.replacementHooks.Installed
}
