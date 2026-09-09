package core

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/GizClaw/opencraft/internal/capabilities/automations"
	"github.com/GizClaw/opencraft/internal/capabilities/usage"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
)

// Runtime owns the shared workspace Host manager and the user-level
// usage database. It is the desktop replacement for the old App host
// wiring and is not a Wails binding. The user-level database itself is
// opened and owned by host.Manager (OpenUserDB), which also installs
// the default usage recorder; this type only forwards the accessors.
type Runtime struct {
	mu sync.Mutex

	dataDir string
	userDir string

	manager *host.Manager
	current *host.Host

	automationManager *automations.Manager

	hostConfigured   map[*host.Host]bool
	hostConfigurator func(*host.Host)
	// ensureHost resolves a usable Host for one workspace. It defaults
	// to EnsureUsableHost and is swappable in tests so deadline-window
	// behaviour can be pinned without assembling a real engine.
	ensureHost func(context.Context, string) (*host.Host, error)
}

// NewRuntime creates the runtime service rooted at dataDir/userDir.
func NewRuntime(dataDir, userDir string) *Runtime {
	// The desktop app is the single composition root of this process
	// (one Desktop -> one Core -> one Runtime), so one Manager per
	// process is guaranteed structurally: main.go creates exactly one
	// Desktop and Wails' SingleInstanceLock prevents a second process.
	// If multi-window support is ever added, share this Runtime (and
	// therefore this Manager) across windows instead of constructing
	// a second one.
	r := &Runtime{
		dataDir:        dataDir,
		userDir:        userDir,
		manager:        host.NewManagerAt(dataDir, userDir),
		hostConfigured: make(map[*host.Host]bool),
	}
	r.ensureHost = r.EnsureUsableHost
	return r
}

// Manager returns the shared host manager.
func (r *Runtime) Manager() *host.Manager {
	return r.manager
}

// Current returns the currently acquired Host, or nil.
func (r *Runtime) Current() *host.Host {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current
}

// SetHostConfigurator installs a callback applied once to every Host
// acquired from the manager. Adapters use it to wire UI observers
// (artifacts, session updates) without re-registering on shared hosts.
func (r *Runtime) SetHostConfigurator(fn func(*host.Host)) {
	r.mu.Lock()
	r.hostConfigurator = fn
	r.mu.Unlock()
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

// Acquire returns a shared Host for workDir. The prompt backend is
// supplied by the caller (UI bridge, automation Auto, headless Auto).
func (r *Runtime) Acquire(
	ctx context.Context,
	workDir string,
	backend interact.Backend,
) (*host.Host, error) {
	if r.manager == nil {
		return nil, fmt.Errorf("runtime: host manager is not configured")
	}
	h, err := r.manager.Acquire(ctx, workDir, backend, nil)
	if err != nil {
		return nil, err
	}
	r.configureHost(h)
	r.mu.Lock()
	r.current = h
	r.mu.Unlock()
	return h, nil
}

// AcquireBackground returns a shared Host without making it the
// active workspace Host. Automation and headless-style runs use it so
// opening or running a background workspace never steals the UI's
// current Host.
func (r *Runtime) AcquireBackground(
	ctx context.Context,
	workDir string,
	backend interact.Backend,
) (*host.Host, error) {
	if r.manager == nil {
		return nil, fmt.Errorf("runtime: host manager is not configured")
	}
	h, err := r.manager.Acquire(ctx, workDir, backend, nil)
	if err != nil {
		return nil, err
	}
	r.configureHost(h)
	return h, nil
}

// EnsureUsableHost returns a Host for workDir that can accept new
// turns. When the current Host already serves workDir and is live it
// is returned untouched; otherwise the manager waits out any retiring
// Host and assembles a fresh one, which becomes the current Host.
// Adapters use it to recover from the transient host lifecycle guards
// (runtime closing / not ready) inside one binding RPC.
func (r *Runtime) EnsureUsableHost(
	ctx context.Context,
	workDir string,
) (*host.Host, error) {
	if r.manager == nil {
		return nil, fmt.Errorf("runtime: host manager is not configured")
	}
	if h := r.Current(); h != nil &&
		filepath.Clean(h.WorkDir()) == filepath.Clean(workDir) &&
		!h.IsClosing() {
		return h, nil
	}
	return r.Acquire(ctx, workDir, interact.Auto{})
}

// EnsureUsableHostWithin waits for a replacement Host for workDir, but
// only while the retry deadline still has time left. The wait itself
// is bounded by the remaining window (not the caller's whole RPC), so
// a slow rebuild cannot stretch one StartTurn/Delete call past its
// advertised retry window. lastErr is returned unchanged when the
// window expires or the ensure fails, mirroring the pre-ensure error
// the caller should surface.
func (r *Runtime) EnsureUsableHostWithin(
	ctx context.Context,
	deadline time.Time,
	workDir string,
	lastErr error,
) error {
	if r.ensureHost == nil {
		return lastErr
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return lastErr
	}
	attemptCtx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	if _, err := r.ensureHost(attemptCtx, workDir); err != nil {
		return lastErr
	}
	if time.Now().After(deadline) {
		return lastErr
	}
	return nil
}

// configureHost runs the adapter host configurator once per Host.
func (r *Runtime) configureHost(h *host.Host) {
	r.mu.Lock()
	if r.hostConfigured[h] {
		r.mu.Unlock()
		return
	}
	r.hostConfigured[h] = true
	fn := r.hostConfigurator
	r.mu.Unlock()
	if fn != nil {
		fn(h)
	}
}

// Reload invalidates pooled hosts so the next Acquire rebuilds from
// the current configuration.
func (r *Runtime) Reload(ctx context.Context) error {
	if r.manager != nil {
		r.manager.InvalidateAll()
	}
	r.mu.Lock()
	r.current = nil
	r.mu.Unlock()
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
	r.current = nil
	r.mu.Unlock()
}
