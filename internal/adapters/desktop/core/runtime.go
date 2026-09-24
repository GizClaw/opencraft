package core

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/GizClaw/opencraft/internal/capabilities/automations"
	"github.com/GizClaw/opencraft/internal/capabilities/usage"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
)

// The retry window every host lifecycle guard is absorbed inside one
// binding RPC: how long a caller waits for the workspace's replacement
// Host, and how many times it may ask for one.
const (
	// startRetryWindow bounds how long a StartTurn/Delete call waits
	// for a replacement Host.
	startRetryWindow = 10 * time.Second
	// maxStartAttempts caps the retries inside one binding RPC.
	maxStartAttempts = 3
)

// Runtime owns the shared workspace Host manager and the user-level
// usage database. It is the desktop replacement for the old App host
// wiring and is not a Wails binding. The user-level database itself is
// opened and owned by host.Manager (OpenUserDB), which also installs
// the default usage recorder; this type only forwards the accessors.
//
// Runtime holds no Host of its own: which generation serves which
// workspace is host.Manager's answer, and it is asked per workspace
// (Current/HostFor), never once for the whole process. A cached "the
// current Host" pointer used to be the layer everything read, and every
// path that acquired a Host for a workspace the window had left had to
// remember not to overwrite it.
type Runtime struct {
	mu sync.Mutex

	manager *host.Manager

	automationManager *automations.Manager

	// ensureHost resolves the Host that serves one workspace. It
	// defaults to EnsureHost and is swappable in tests so the retry
	// window and the loop can be pinned without assembling a real
	// engine.
	ensureHost func(context.Context, string) (*host.Host, error)
}

// NewRuntime creates the runtime service rooted at the three launch
// paths: dataDir (state root), userDir (config directory) and appHome
// (shared content/credential root; empty follows the state root).
func NewRuntime(dataDir, userDir, appHome string) *Runtime {
	// The desktop app is the single composition root of this process
	// (one Desktop -> one Core -> one Runtime), so one Manager per
	// process is guaranteed structurally: main.go creates exactly one
	// Desktop and the single-instance lock (whose id derives from the
	// state root, see config.ResolveLaunch) prevents a second process
	// on the same root. If multi-window support is ever added, share
	// this Runtime (and therefore this Manager) across windows instead
	// of constructing a second one.
	manager := host.NewManagerAt(dataDir, userDir)
	if appHome != "" {
		manager.SetAppHome(appHome)
	}
	// Name this process in the per-workspace lock files: the desktop is
	// the GUI half of a workspace that headless runs may share.
	manager.SetLeaseKind("gui")
	r := &Runtime{
		manager: manager,
	}
	r.ensureHost = r.EnsureHost
	return r
}

// SameWorkspace reports whether two paths name the same workspace
// directory. Empty paths never match: an unresolved workspace must not
// borrow another workspace's identity.
func SameWorkspace(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// Manager returns the shared host manager.
func (r *Runtime) Manager() *host.Manager {
	return r.manager
}

// HostFor returns the Host that serves one workspace right now — the
// pooled one, or the one retiring while its last runs drain — or nil
// when the workspace has none. Callers read state off it directly; a
// closing Host is refused by EnsureHost, not by this.
func (r *Runtime) HostFor(workDir string) *host.Host {
	return r.manager.Current(workDir)
}

// Usage returns the user-level usage store after OpenUserDB.
func (r *Runtime) Usage() *usage.Store {
	return r.manager.UsageStore()
}

// Automations returns the automation store after OpenUserDB.
func (r *Runtime) Automations() *automations.Store {
	return r.manager.AutomationsStore()
}

// AutomationManager returns the scheduler manager when wired.
func (r *Runtime) AutomationManager() *automations.Manager {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.automationManager
}

// SetAutomationManager wires the scheduler manager.
func (r *Runtime) SetAutomationManager(m *automations.Manager) {
	r.mu.Lock()
	r.automationManager = m
	r.mu.Unlock()
}

// OpenUserDB opens ~/.opencraft/user.db once on the shared manager,
// applies user migrations and attaches the usage, automations and
// metric stores. UI and automation turns both count toward the
// attached usage store through the manager's default recorder.
func (r *Runtime) OpenUserDB(ctx context.Context) error {
	return r.manager.OpenUserDB(ctx)
}

// EnsureHost returns a Host that can serve new work for workDir: the
// pooled one when it is live, and otherwise a fresh assembly, waiting
// out any Host that is still retiring from the workspace. There is one
// of these for every caller — a window turn, a draft draining after a
// switch, an automation, a session import — because the answer is now
// the same for all of them: the workspace's Host. Adapters use it to
// recover from the transient host lifecycle guards (runtime closing /
// runtime not ready) inside one binding RPC.
func (r *Runtime) EnsureHost(
	ctx context.Context,
	workDir string,
) (*host.Host, error) {
	if r.manager == nil {
		return nil, fmt.Errorf("runtime: host manager is not configured")
	}
	return r.manager.Ensure(ctx, workDir)
}

// ScheduleReplacement arms the pool's deferred replacement for one
// workspace and reports whether this call armed it. It is how a reload
// hands off a workspace that is still running on the assembly it
// retired: the pool waits out the drain and assembles the replacement
// (see host.Manager.ScheduleReplacement for the once-per-workspace
// rule).
func (r *Runtime) ScheduleReplacement(ctx context.Context, workDir string) bool {
	return r.manager.ScheduleReplacement(ctx, workDir)
}

// ReplacementArmed reports whether a deferred replacement is already
// scheduled for one workspace, i.e. whether the stale generation
// serving it is about to be replaced.
func (r *Runtime) ReplacementArmed(workDir string) bool {
	return r.manager.ReplacementArmed(workDir)
}

// Do runs fn against the Host that serves workDir, absorbing the
// transient host lifecycle guards (the runtime is closing, the shared
// session store is not ready yet) by waiting — inside one retry window
// — for the workspace's replacement Host and running fn again. It is
// the one place those guards are retried: every caller used to spell
// out the same window, attempt budget and re-ensure sequence.
//
// fn returning nil ends the loop. A non-retryable error from fn ends it
// too — those are the caller's own failures (a busy conversation, an
// invalid id, a validation refusal) and repeating them is pointless. A
// retryable one is retried until the window or the attempt budget runs
// out, and what the caller saw last is what comes back: the guard it
// hit, or "the runtime is not ready" when the workspace never got a
// usable Host before the window closed. A pool error that is not a
// lifecycle guard (an assembly failure, no workspace named) is reported
// as-is instead of being retried into a timeout.
//
// The wait itself is bounded by the remaining window rather than by the
// caller's whole RPC: a turn that has to cross a drain waits at most
// startRetryWindow for it, and the RPC never hangs for the length of
// somebody else's run.
//
// stop is consulted before every retry, never before the first
// attempt: it lets a caller whose premise expired give up instead of
// holding the RPC open for a workspace nobody is looking at. A caller
// waiting on the window's workspace drops the retry when the window
// moves; the first attempt still runs, because it is resolved against
// the workspace the call named rather than against whatever Host the
// window happens to show — a send that raced a workspace switch lands
// where its conversation lives. Nil means "keep trying until the
// window runs out" — what a call that is bound to a workspace rather
// than to the window (a queued draft draining behind a turn, an import
// aimed elsewhere) wants.
func (r *Runtime) Do(
	ctx context.Context,
	workDir string,
	stop func() bool,
	fn func(*host.Host) error,
) error {
	deadline := time.Now().Add(startRetryWindow)
	lastErr := host.ErrRuntimeNotReady
	for attempt := 0; attempt < maxStartAttempts; attempt++ {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return lastErr
		}
		attemptCtx, cancel := context.WithTimeout(ctx, remaining)
		h, err := r.ensure(attemptCtx, workDir)
		// Only the window's own expiry is absorbed below. A caller whose
		// context died (a canceled RPC, a deadline of its own) keeps
		// its error, and so does an assembly that failed for its own
		// reasons.
		expired := ctx.Err() == nil &&
			errors.Is(attemptCtx.Err(), context.DeadlineExceeded)
		cancel()
		switch {
		case expired:
			// The window closed while the workspace was still
			// draining. Who waited is not the caller's business: it
			// gets the guard it last hit.
			return lastErr
		case err != nil && !host.IsRetryableStartError(err):
			return err
		case err != nil:
			lastErr = err
		case h == nil:
			// The pool answers "no host for this workspace" only
			// when it was asked for nothing: wait for one like any
			// other lifecycle guard.
			lastErr = host.ErrRuntimeNotReady
		default:
			lastErr = fn(h)
			if lastErr == nil || !host.IsRetryableStartError(lastErr) {
				return lastErr
			}
		}
		// Retry only while the premise holds: the caller's request is
		// still alive, and (when it named one) its window is still on
		// the workspace it asked about.
		if ctx.Err() != nil || (stop != nil && stop()) {
			return lastErr
		}
	}
	return lastErr
}

// ensure is the one way the retry paths resolve the Host for a
// workspace, so a test can substitute the resolution (see the
// ensureHost field).
func (r *Runtime) ensure(
	ctx context.Context,
	workDir string,
) (*host.Host, error) {
	if r.ensureHost == nil {
		return r.EnsureHost(ctx, workDir)
	}
	return r.ensureHost(ctx, workDir)
}

// Reload invalidates pooled hosts so the next EnsureHost rebuilds from
// the current configuration.
func (r *Runtime) Reload(ctx context.Context) error {
	if r.manager != nil {
		r.manager.InvalidateAll(ctx)
	}
	return nil
}

// Close cancels hosts, closes pooled workspace stores and closes the
// user database handle.
func (r *Runtime) Close() {
	if r.manager != nil {
		r.manager.CancelAll()
		r.manager.CloseAll()
		r.manager.CloseUserDB()
	}
	r.mu.Lock()
	r.automationManager = nil
	r.mu.Unlock()
}
