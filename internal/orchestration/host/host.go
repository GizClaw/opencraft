// Package host owns one assembled workspace runtime for all callers in
// the process. UI turns and automation turns share the same Host and
// the same sessions.Store; prompt backends differ per run through the
// interact resolver.
package host

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/inference"

	ocsagents "github.com/GizClaw/opencraft/internal/capabilities/agents"
	"github.com/GizClaw/opencraft/internal/capabilities/hooks"
	"github.com/GizClaw/opencraft/internal/capabilities/rollout"
	"github.com/GizClaw/opencraft/internal/capabilities/sandbox"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/engine"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/orchestration/migrations"
)

// Manager pools Hosts by workspace and keeps one sessions.Store per
// workspace root.
type Manager struct {
	userDir string
	dataDir string

	mu            sync.Mutex
	hosts         map[string]*hostRef
	stores        map[string]*storeRef
	engineOptFunc func() []engine.Option
	usageObserver func(context.Context, inference.Usage)
}

type hostRef struct {
	host *Host
	refs int
}

type storeRef struct {
	store *sessions.Store
	refs  int
}

// NewManager creates a Host manager rooted at the global user config
// directory.
func NewManager(userDir string) *Manager {
	return &Manager{
		userDir: userDir,
		hosts:   make(map[string]*hostRef),
		stores:  make(map[string]*storeRef),
	}
}

// NewManagerAt creates a manager with explicit user data and config
// roots.
func NewManagerAt(dataDir, userDir string) *Manager {
	m := NewManager(userDir)
	m.dataDir = dataDir
	return m
}

// SetEngineOptionsFunc installs a function called before every runtime
// assembly. Returning fresh options lets adapters rebuild plugin hosts
// (whose entry caches must rescan after plugin changes).
func (m *Manager) SetEngineOptionsFunc(fn func() []engine.Option) {
	m.mu.Lock()
	m.engineOptFunc = fn
	m.mu.Unlock()
}

// SetUsageObserver installs a host-level usage reporter for
// non-run generations such as automatic titles.
func (m *Manager) SetUsageObserver(fn func(context.Context, inference.Usage)) {
	m.mu.Lock()
	m.usageObserver = fn
	m.mu.Unlock()
}

// InvalidateAll drops every pooled Host. Idle hosts close immediately;
// hosts with active runs finish on the old runtime and close after the
// last run ends.
func (m *Manager) InvalidateAll() {
	m.mu.Lock()
	dirs := make([]string, 0, len(m.hosts))
	for wd := range m.hosts {
		dirs = append(dirs, wd)
	}
	m.mu.Unlock()
	for _, wd := range dirs {
		m.Invalidate(wd)
	}
}

// Acquire returns (creating if needed) the shared Host for workDir.
// fallback is used for runs without a resolver hit.
func (m *Manager) Acquire(
	ctx context.Context,
	workDir string,
	fallback interact.Backend,
	resolver func(runID string) interact.Backend,
) (*Host, error) {
	workDir = filepath.Clean(workDir)
	m.mu.Lock()
	if ref := m.hosts[workDir]; ref != nil {
		ref.refs++
		h := ref.host
		m.mu.Unlock()
		return h, nil
	}
	m.mu.Unlock()

	h, err := m.assemble(ctx, workDir, fallback, resolver)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if ref := m.hosts[workDir]; ref != nil {
		_ = h.Close()
		ref.refs++
		return ref.host, nil
	}
	m.hosts[workDir] = &hostRef{host: h, refs: 1}
	return h, nil
}

// Invalidate drops one workspace's Host from the pool. If the Host is
// idle it closes immediately; active runs finish on the old runtime
// and close when the last run ends.
func (m *Manager) Invalidate(workDir string) {
	workDir = filepath.Clean(workDir)
	m.mu.Lock()
	ref := m.hosts[workDir]
	if ref == nil {
		m.mu.Unlock()
		return
	}
	delete(m.hosts, workDir)
	h := ref.host
	m.mu.Unlock()
	_ = h.Close()
}

// CloseAll invalidates every pooled Host.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	hosts := make([]*Host, 0, len(m.hosts))
	for wd := range m.hosts {
		hosts = append(hosts, m.hosts[wd].host)
		delete(m.hosts, wd)
	}
	m.mu.Unlock()
	for _, h := range hosts {
		_ = h.Close()
	}
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

// assemble builds one Host without holding the manager lock.
func (m *Manager) assemble(
	ctx context.Context,
	workDir string,
	fallback interact.Backend,
	resolver func(runID string) interact.Backend,
) (*Host, error) {
	userDir := m.userDir
	dataDir := m.dataDir
	m.mu.Lock()
	engineOptFunc := m.engineOptFunc
	usageObserver := m.usageObserver
	m.mu.Unlock()
	var engineOptions []engine.Option
	if engineOptFunc != nil {
		engineOptions = engineOptFunc()
	}
	if userDir == "" {
		var err error
		userDir, err = config.UserConfigDir()
		if err != nil {
			return nil, err
		}
	}
	if dataDir == "" {
		dataDir, _ = config.UserDataDir()
	}
	layout, err := config.ResolveWorkspace(dataDir, workDir)
	if err != nil {
		return nil, err
	}
	_ = layout.Ensure()
	mgr, err := config.Open(config.Options{
		UserDir: userDir,
	})
	if err != nil {
		return nil, err
	}
	view, err := mgr.Load(ctx)
	if err != nil {
		return nil, err
	}
	h := &Host{
		workDir:  workDir,
		userDir:  userDir,
		manager:  m,
		usage:    usageObserver,
		runs:     make(map[RunID]*runDetail),
		rollouts: make(map[ConversationID]*rollout.Recorder),
		titling:  make(map[ConversationID]bool),
	}
	buildOpts := append([]engine.Option{
		engine.WithConfigBase(userDir),
		engine.WithWorkBase(workDir),
		engine.WithWorkspaceLayout(&layout),
		engine.WithSessionStore(m.acquireStore),
		engine.WithUsageObserver(func(ctx context.Context, usage inference.Usage) {
			if _, ok := agent.RunInfoFromContext(ctx); ok {
				h.reportUsage(ctx, usage)
				return
			}
			if h.usage != nil {
				h.usage(ctx, usage)
			}
		}),
	}, engineOptions...)
	rt, err := engine.BuildRuntime(ctx, view.Document, buildOpts...)
	if err != nil {
		return nil, err
	}
	ctrl := engine.NewController(rt)
	if resolver == nil {
		resolver = h.backendForRun
	}
	broker := interact.NewWithBackendResolver(rt, fallback, resolver)
	if err := broker.Attach(ctx); err != nil {
		_ = ctrl.Close()
		return nil, err
	}
	value, ok := rt.Resource("sessions")
	store, ok2 := value.(*sessions.Store)
	if !ok || !ok2 || store == nil {
		broker.Close()
		_ = ctrl.Close()
		return nil, errors.New("host: session store resource missing")
	}
	h.store = store
	h.ctrl = ctrl
	h.broker = broker
	if value, ok := rt.Resource("agentlifecycle"); ok {
		if lifecycle, ok := value.(*ocsagents.Lifecycle); ok && lifecycle != nil {
			h.agents = lifecycle
		}
	}
	if value, ok := rt.Resource("hooks"); ok {
		if mgr, ok := value.(*hooks.Manager); ok && mgr != nil {
			h.hooks = mgr
		}
	}
	if value, ok := rt.Resource("artifacts"); ok {
		if obs, ok := value.(*sandbox.ArtifactObserver); ok && obs != nil {
			obs.SetSink(h.onArtifactWrite)
		}
	}
	return h, nil
}

// reportUsage routes an engine usage report to the run that owns it.
func (h *Host) reportUsage(ctx context.Context, usage inference.Usage) {
	runID := ""
	if info, ok := agent.RunInfoFromContext(ctx); ok {
		runID = info.RunID
	}
	h.mu.Lock()
	d := h.runs[RunID(runID)]
	if d == nil {
		h.mu.Unlock()
		return
	}
	d.usage.TotalTokens += usage.TotalTokens
	d.usage.InputTokens += usage.InputTokens
	d.usage.OutputTokens += usage.OutputTokens
	if usage.Model.ID.Provider != "" && usage.Model.ID.Name != "" {
		d.usage.Model = usage.Model.ID.Provider + "/" + usage.Model.ID.Name
	}
	if usage.Output.ReasoningTokens != nil {
		d.usage.ReasoningTokens += *usage.Output.ReasoningTokens
	}
	if usage.Input.CacheReadTokens != nil {
		d.usage.CacheReadTokens += *usage.Input.CacheReadTokens
	}
	if usage.Input.CacheWriteTokens != nil {
		d.usage.CacheWriteTokens += *usage.Input.CacheWriteTokens
	}
	fn := d.notify
	h.mu.Unlock()
	if fn != nil {
		fn(ctx, usage)
	}
}

func (h *Host) takeUsage(runID string) sessions.Usage {
	h.mu.Lock()
	defer h.mu.Unlock()
	if d := h.runs[RunID(runID)]; d != nil {
		usage := d.usage
		d.usage = sessions.Usage{}
		return usage
	}
	return sessions.Usage{}
}

// acquireStore opens one Store per root and reference-counts it.
func (m *Manager) acquireStore(
	ctx context.Context, root string, window int,
) (*sessions.Store, error) {
	root = filepath.Clean(root)
	m.mu.Lock()
	if ref := m.stores[root]; ref != nil {
		ref.refs++
		s := ref.store
		m.mu.Unlock()
		return s, nil
	}
	m.mu.Unlock()

	store, err := sessions.New(root, window)
	if err != nil {
		return nil, err
	}
	if err := migrations.Workspace(ctx, store.Database(), root); err != nil {
		_ = store.Close()
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if ref := m.stores[root]; ref != nil {
		_ = store.Close()
		ref.refs++
		return ref.store, nil
	}
	m.stores[root] = &storeRef{store: store, refs: 1}
	return store, nil
}

// releaseStore drops one runtime reference to a shared Store.
func (m *Manager) releaseStore(store *sessions.Store) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for root, ref := range m.stores {
		if ref.store != store {
			continue
		}
		ref.refs--
		if ref.refs == 0 {
			delete(m.stores, root)
			_ = store.Close()
		}
		return
	}
}

// Host is one shared workspace runtime.
type Host struct {
	workDir string
	userDir string
	store   *sessions.Store
	ctrl    *engine.Controller
	broker  *interact.Broker
	manager *Manager
	agents  *ocsagents.Lifecycle
	hooks   *hooks.Manager
	usage   func(context.Context, inference.Usage)

	mu         sync.Mutex
	runs       map[RunID]*runDetail
	rollouts   map[ConversationID]*rollout.Recorder
	titling    map[ConversationID]bool
	artifact   func(context.Context, string, []byte)
	sessionUpd func(context.Context, string)
	closing    bool
	closed     bool
}

// RunID identifies one engine run inside a Host.
type RunID string

// ConversationID identifies one conversation inside a Host.
type ConversationID string

// runDetail is the internal per-run state owned by Host.
type runDetail struct {
	run *Run

	contextID string
	usage     sessions.Usage
	notify    func(context.Context, inference.Usage)
	buffer    *rolloutBuffer
	manifest  map[string]fileStat
	backend   interact.Backend
}

// dropRun removes an ended run from the active set. Usage for the run
// must already have been captured before this is called.
func (h *Host) dropRun(runID RunID) {
	h.mu.Lock()
	delete(h.runs, runID)
	h.mu.Unlock()
}

// RunView is the read-only identity of one active run.
type RunView struct {
	RunID          string
	ConversationID string
}

// ActiveRuns snapshots every live run owned by the Host.
func (h *Host) ActiveRuns() []RunView {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]RunView, 0, len(h.runs))
	for id, d := range h.runs {
		out = append(out, RunView{
			RunID:          string(id),
			ConversationID: d.contextID,
		})
	}
	return out
}

// backendForRun selects the prompt backend bound to one run. Nil means
// the Host's fallback backend applies.
func (h *Host) backendForRun(runID string) interact.Backend {
	h.mu.Lock()
	defer h.mu.Unlock()
	if d := h.runs[RunID(runID)]; d != nil {
		return d.backend
	}
	return nil
}

// SetArtifactObserver installs a callback invoked for every observed
// workspace write before the artifact is buffered.
func (h *Host) SetArtifactObserver(fn func(context.Context, string, []byte)) {
	h.mu.Lock()
	h.artifact = fn
	h.mu.Unlock()
}

// SetSessionUpdated installs a callback fired when a conversation
// title changes.
func (h *Host) SetSessionUpdated(fn func(context.Context, string)) {
	h.mu.Lock()
	h.sessionUpd = fn
	h.mu.Unlock()
}

// rolloutBuffer accumulates one run's assistant text/reasoning until
// the stream finish delta.
type rolloutBuffer struct {
	text      strings.Builder
	reasoning strings.Builder
}

// WorkDir returns the workspace path.
func (h *Host) WorkDir() string { return h.workDir }

// Sessions returns the shared conversation store.
func (h *Host) Sessions() *sessions.Store { return h.store }

// Controller returns the flowcraft runtime lifecycle controller.
func (h *Host) Controller() *engine.Controller { return h.ctrl }

// Broker returns the run-routed prompt broker.
func (h *Host) Broker() *interact.Broker { return h.broker }

// Agents returns the runtime's agent lifecycle registry, or nil when
// the runtime does not wire one.
func (h *Host) Agents() *ocsagents.Lifecycle { return h.agents }

// CancelRun cancels one live engine turn. It returns an error when the
// run is not active on this Host.
func (h *Host) CancelRun(runID string) error {
	h.mu.Lock()
	d := h.runs[RunID(runID)]
	h.mu.Unlock()
	if d == nil || d.run == nil || d.run.turn == nil {
		return errors.New("host: turn not found")
	}
	d.run.turn.Cancel()
	return nil
}

// CancelAll cancels every live run on this Host.
func (h *Host) CancelAll() {
	h.mu.Lock()
	runs := make([]*Run, 0, len(h.runs))
	for _, d := range h.runs {
		if d != nil && d.run != nil {
			runs = append(runs, d.run)
		}
	}
	h.mu.Unlock()
	for _, r := range runs {
		if r != nil && r.turn != nil {
			r.turn.Cancel()
		}
	}
}

// Close releases the Host. When the last Host for a workspace closes,
// its runtime and session store are torn down.
func (h *Host) Close() error {
	if h == nil {
		return nil
	}
	m := h.manager
	if m == nil {
		return nil
	}
	m.mu.Lock()
	ref := m.hosts[h.workDir]
	if ref != nil && ref.host != h {
		m.mu.Unlock()
		return nil
	}
	if ref != nil {
		ref.refs--
		if ref.refs > 0 {
			m.mu.Unlock()
			return nil
		}
		delete(m.hosts, h.workDir)
	}
	m.mu.Unlock()

	h.mu.Lock()
	if len(h.runs) > 0 {
		h.closing = true
		h.mu.Unlock()
		return nil
	}
	h.mu.Unlock()
	h.doClose()
	return nil
}

func (h *Host) doClose() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	h.mu.Unlock()
	h.closeRollouts()
	h.broker.Close()
	_ = h.ctrl.Close()
	if h.manager != nil {
		h.manager.releaseStore(h.store)
	}
}

func (h *Host) finishCloseIfIdle() {
	h.mu.Lock()
	closing := h.closing && len(h.runs) == 0
	h.mu.Unlock()
	if closing {
		h.doClose()
	}
}
