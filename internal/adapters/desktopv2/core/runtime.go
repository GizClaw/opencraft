package core

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/capabilities/automations"
	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
	pluginagent "github.com/GizClaw/opencraft/internal/capabilities/plugins/agent"
	pluginruntime "github.com/GizClaw/opencraft/internal/capabilities/plugins/runtime"
	"github.com/GizClaw/opencraft/internal/capabilities/usage"
	"github.com/GizClaw/opencraft/internal/foundation/db"
	"github.com/GizClaw/opencraft/internal/orchestration/engine"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/orchestration/migrations"
)

// Runtime owns the shared workspace Host manager and the user-level
// usage database. It is the desktopv2 replacement for the old App host
// wiring and is not a Wails binding.
type Runtime struct {
	mu sync.Mutex

	dataDir string
	userDir string

	manager *host.Manager
	current *host.Host

	userDB            *db.DB
	usage             *usage.Store
	automations       *automations.Store
	automationManager *automations.Manager

	hostConfigured   map[*host.Host]bool
	hostConfigurator func(*host.Host)
}

// NewRuntime creates the runtime service rooted at dataDir/userDir.
func NewRuntime(dataDir, userDir string) *Runtime {
	return &Runtime{
		dataDir:        dataDir,
		userDir:        userDir,
		manager:        host.NewManagerAt(dataDir, userDir),
		hostConfigured: make(map[*host.Host]bool),
	}
}

// SetAgentPlugins wires the plugin registry into every runtime
// assembly: skills, MCP servers, hooks and capability tools contributed
// by enabled plugins become runtime resources.
func (r *Runtime) SetAgentPlugins(
	store *plugins.Store,
	cap *pluginruntime.Manager,
) {
	if r.manager == nil {
		return
	}
	r.manager.SetEngineOptionsFunc(func() []engine.Option {
		return []engine.Option{
			engine.WithAgentPlugins(
				pluginagent.NewHost(context.Background(), store, cap),
			),
		}
	})
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
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.usage
}

// Automations returns the automation store after OpenUserDB.
func (r *Runtime) Automations() *automations.Store {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.automations
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

// OpenUserDB opens ~/.opencraft/user.db once, applies user migrations
// and attaches usage (automations attach in a later phase).
func (r *Runtime) OpenUserDB(ctx context.Context) error {
	r.mu.Lock()
	if r.userDB != nil {
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()

	udb, err := db.Open(filepath.Join(r.dataDir, "user.db"))
	if err != nil {
		return fmt.Errorf("runtime: open user db: %w", err)
	}
	if err := migrations.User(ctx, udb); err != nil {
		telemetry.WarnErr(ctx, "desktop runtime: close user db after migration failure",
			udb.Close())
		return fmt.Errorf("runtime: migrate user db: %w", err)
	}
	usageStore, err := usage.Attach(udb)
	if err != nil {
		telemetry.WarnErr(ctx, "desktop runtime: close user db after usage attach failure",
			udb.Close())
		return fmt.Errorf("runtime: attach usage: %w", err)
	}
	automationStore, err := automations.Attach(udb)
	if err != nil {
		telemetry.WarnErr(ctx,
			"desktop runtime: close user db after automations attach failure",
			udb.Close())
		return fmt.Errorf("runtime: attach automations: %w", err)
	}

	r.mu.Lock()
	r.userDB = udb
	r.usage = usageStore
	r.automations = automationStore
	r.mu.Unlock()
	return nil
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

// Close cancels hosts and closes the user database handle.
func (r *Runtime) Close() {
	if r.manager != nil {
		r.manager.CancelAll()
		r.manager.CloseAll()
	}
	r.mu.Lock()
	udb := r.userDB
	r.userDB = nil
	r.usage = nil
	r.automations = nil
	r.automationManager = nil
	r.current = nil
	r.mu.Unlock()
	if udb != nil {
		telemetry.WarnErr(context.Background(),
			"desktop runtime: close user db failed", udb.Close())
	}
}
