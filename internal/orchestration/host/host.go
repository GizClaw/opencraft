// Package host owns one assembled workspace runtime for all callers in
// the process. UI turns and automation turns share the same Host and
// the same sessions.Store; prompt backends differ per run through the
// interact resolver.
package host

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/telemetry"

	ocsagents "github.com/GizClaw/opencraft/internal/capabilities/agents"
	"github.com/GizClaw/opencraft/internal/capabilities/automations"
	"github.com/GizClaw/opencraft/internal/capabilities/hooks"
	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
	pluginagent "github.com/GizClaw/opencraft/internal/capabilities/plugins/agent"
	pluginruntime "github.com/GizClaw/opencraft/internal/capabilities/plugins/runtime"
	"github.com/GizClaw/opencraft/internal/capabilities/rollout"
	"github.com/GizClaw/opencraft/internal/capabilities/sandbox"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/usage"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/db"
	"github.com/GizClaw/opencraft/internal/orchestration/engine"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/orchestration/migrations"
)

// UsageRecorder receives one model usage delta (a finished turn, one
// auto-title call, or an imported session) so adapters can persist
// user-level accounting rows. workspaceID and sessionID are explicit;
// the recorder must not re-derive them from process state. at is the
// moment the engine reported the usage (report-arrival time) and
// drives the user-level hourly bucket. Errors are logged by the host
// and never fail the turn.
type UsageRecorder func(
	ctx context.Context,
	workspaceID, sessionID string,
	usage sessions.Usage,
	at time.Time,
) error

// usageDelta is one model usage slice with the moment it was reported.
// A turn can span several models and hours; each engine report becomes
// its own bucket so statistics keep both dimensions accurate.
type usageDelta struct {
	usage sessions.Usage
	at    time.Time
}

// Manager pools Hosts by workspace and keeps one sessions.Store per
// workspace root. Document-only configuration reloads go through
// Host.ReloadDocument in place; Manager invalidation (and the
// stale/retire machinery below) is reserved for engine-input changes
// (plugin install/uninstall, workspace switches) and fallback rebuilds.
type Manager struct {
	userDir string
	dataDir string

	mu            sync.Mutex
	openMu        sync.Mutex
	hosts         map[string]*hostRef
	stores        map[string]*storeRef
	engineOptFunc func() []engine.Option
	usageObserver func(context.Context, inference.Usage)
	usageRecorder UsageRecorder
	// retiring maps a workspace root to a Host that was removed from
	// the pool and is draining its last runs. Acquire waits for these
	// hosts to finish teardown instead of assembling a second Host for
	// the same workspace, which would let two runtimes serve one
	// conversation concurrently.
	retiring map[string]*Host
	// closeHost is the teardown entry point. It is a field so tests
	// can substitute a fake close without spinning up a runtime.
	closeHost func(*Host)

	// User-level database state, opened on demand by OpenUserDB.
	userDB          *db.DB
	userUsage       *usage.Store
	userAutomations *automations.Store
}

type hostRef struct {
	host  *Host
	refs  int
	stale bool
}

type storeRef struct {
	store *sessions.Store
	refs  int
}

// NewManager creates a Host manager rooted at the global user config
// directory.
func NewManager(userDir string) *Manager {
	m := &Manager{
		userDir: userDir,
		hosts:   make(map[string]*hostRef),
		stores:  make(map[string]*storeRef),
	}
	m.closeHost = func(h *Host) { h.beginClose() }
	m.usageRecorder = m.recordUserUsage
	return m
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

// SetAgentPlugins wires the plugin registry into every runtime
// assembly: skills, MCP servers, hooks and capability tools
// contributed by enabled plugins become runtime resources. Adapters
// do not need to import orchestration/engine for this.
func (m *Manager) SetAgentPlugins(
	store *plugins.Store,
	cap *pluginruntime.Manager,
) {
	m.SetEngineOptionsFunc(func() []engine.Option {
		return []engine.Option{
			engine.WithAgentPlugins(
				pluginagent.NewHost(context.Background(), store, cap),
			),
		}
	})
}

// SetUsageObserver installs a host-level usage reporter for
// non-run generations such as automatic titles.
func (m *Manager) SetUsageObserver(fn func(context.Context, inference.Usage)) {
	m.mu.Lock()
	m.usageObserver = fn
	m.mu.Unlock()
}

// SetUsageRecorder installs the user-level usage sink. A finished turn
// may deliver several deltas (one per model + hour bucket), plus one
// for each auto-title/background generation. UI and automation turns
// share the Host, so one recorder covers both paths. A nil fn restores
// the default recorder, which writes into the usage store attached by
// OpenUserDB (a no-op before the store is attached).
func (m *Manager) SetUsageRecorder(fn UsageRecorder) {
	m.mu.Lock()
	if fn == nil {
		fn = m.recordUserUsage
	}
	m.usageRecorder = fn
	m.mu.Unlock()
}

// OpenUserDB opens the user-level database (user.db under the manager
// data root) once, applies user migrations and attaches the usage and
// automations stores. It is idempotent and safe to call from several
// adapters sharing the manager. The default usage recorder starts
// persisting as soon as the usage store is attached.
func (m *Manager) OpenUserDB(ctx context.Context) error {
	m.mu.Lock()
	if m.userDB != nil {
		m.mu.Unlock()
		return nil
	}
	dataDir := m.dataDir
	m.mu.Unlock()
	if dataDir == "" {
		var err error
		dataDir, err = config.UserDataDir()
		if err != nil {
			return fmt.Errorf("host: resolve user data dir: %w", err)
		}
	}

	// Serialize first open so two adapters cannot migrate and attach
	// the same database concurrently.
	m.openMu.Lock()
	defer m.openMu.Unlock()

	m.mu.Lock()
	if m.userDB != nil {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	handle, err := db.Open(filepath.Join(dataDir, "user.db"))
	if err != nil {
		return fmt.Errorf("host: open user db: %w", err)
	}
	if err := migrations.User(ctx, handle); err != nil {
		telemetry.WarnErr(ctx, "host: close user db after migration failure",
			handle.Close())
		return fmt.Errorf("host: migrate user db: %w", err)
	}
	usageStore, err := usage.Attach(handle)
	if err != nil {
		telemetry.WarnErr(ctx, "host: close user db after usage attach failure",
			handle.Close())
		return fmt.Errorf("host: attach usage: %w", err)
	}
	automationStore, err := automations.Attach(handle)
	if err != nil {
		telemetry.WarnErr(ctx,
			"host: close user db after automations attach failure",
			handle.Close())
		return fmt.Errorf("host: attach automations: %w", err)
	}
	m.mu.Lock()
	if m.userDB != nil {
		m.mu.Unlock()
		telemetry.WarnErr(ctx, "host: close duplicate user db", handle.Close())
		return nil
	}
	m.userDB = handle
	m.userUsage = usageStore
	m.userAutomations = automationStore
	m.mu.Unlock()
	return nil
}

// CloseUserDB closes the user-level database handle opened by
// OpenUserDB. Idempotent; safe to call even when OpenUserDB never
// succeeded.
func (m *Manager) CloseUserDB() {
	m.mu.Lock()
	handle := m.userDB
	m.userDB = nil
	m.userUsage = nil
	m.userAutomations = nil
	m.mu.Unlock()
	if handle != nil {
		telemetry.WarnErr(context.Background(),
			"host: close user db failed", handle.Close())
	}
}

// UsageStore returns the user-level usage store attached by
// OpenUserDB, or nil before the database is open.
func (m *Manager) UsageStore() *usage.Store {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.userUsage
}

// AutomationsStore returns the user-level automation store attached
// by OpenUserDB, or nil before the database is open.
func (m *Manager) AutomationsStore() *automations.Store {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.userAutomations
}

// RecordUsage invokes the currently installed user-level usage
// recorder. It lets callers persist usage outside a run lifecycle
// (imports, tests) through the same sink the hosts use.
func (m *Manager) RecordUsage(
	ctx context.Context,
	workspaceID, sessionID string,
	usage sessions.Usage,
	at time.Time,
) error {
	m.mu.Lock()
	fn := m.usageRecorder
	m.mu.Unlock()
	if fn == nil {
		return nil
	}
	return fn(ctx, workspaceID, sessionID, usage, at)
}

// recordUserUsage is the default usage recorder: it writes into the
// usage store attached by OpenUserDB and no-ops before then, so usage
// accounting can never fail a turn when the database is unavailable.
func (m *Manager) recordUserUsage(
	ctx context.Context,
	workspaceID, sessionID string,
	usage sessions.Usage,
	at time.Time,
) error {
	m.mu.Lock()
	store := m.userUsage
	m.mu.Unlock()
	if store == nil {
		return nil
	}
	return store.RecordSessionUsage(ctx, workspaceID, sessionID, usage, at)
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
		m.mu.Unlock()
		break
	}

	h, err := m.assemble(ctx, workDir, fallback, resolver)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if ref := m.hosts[workDir]; ref != nil {
		ref.refs++
		existing := ref.host
		m.mu.Unlock()
		// The freshly assembled host never reached the pool and has no
		// runs; close it outside the manager lock so it can release its
		// own session-store reference.
		h.doClose()
		return existing, nil
	}
	m.hosts[workDir] = &hostRef{host: h, refs: 1}
	m.mu.Unlock()
	return h, nil
}

// Invalidate marks one workspace's Host as stale (the rebuild path).
// An idle Host closes immediately; a Host with active runs stays
// pooled and keeps serving new turns on the old runtime until the last
// run ends, then retires itself through hostIdle. This defers
// engine-input swaps to idle so a second Host (and a second flowcraft
// Session for the same conversation) is never assembled while the old
// runtime still has live runs.
func (m *Manager) Invalidate(workDir string) {
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
	var closeNow bool
	if !h.hasActiveRuns() {
		delete(m.hosts, workDir)
		closeNow = m.trackRetiringLocked(workDir, h)
	}
	m.mu.Unlock()
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
	m.InvalidateAll()
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
	usageRecorder := m.usageRecorder
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
		var err error
		dataDir, err = config.UserDataDir()
		if err != nil {
			telemetry.WarnErr(ctx, "host: resolve user data dir failed", err)
		}
	}
	layout, err := config.ResolveWorkspace(dataDir, workDir)
	if err != nil {
		return nil, err
	}
	telemetry.WarnErr(ctx, "host: ensure workspace layout failed",
		layout.Ensure())
	doc, err := engine.LoadDocument(ctx, userDir)
	if err != nil {
		return nil, err
	}
	h := &Host{
		workDir:       workDir,
		userDir:       userDir,
		workspaceID:   layout.ID,
		manager:       m,
		usage:         usageObserver,
		usageRecorder: usageRecorder,
		runs:          make(map[RunID]*runDetail),
		rollouts:      make(map[ConversationID]*rollout.Recorder),
		titling:       make(map[ConversationID]bool),
		deleting:      make(map[ConversationID]bool),
		deleted:       make(map[ConversationID]bool),
		closeDone:     make(chan struct{}),
	}
	h.runsCond = sync.NewCond(&h.mu)
	var sessionStore *sessions.Store
	buildOpts := append([]engine.Option{
		engine.WithConfigBase(userDir),
		engine.WithWorkBase(workDir),
		engine.WithWorkspaceLayout(&layout),
		engine.WithSessionStore(func(
			ctx context.Context, root string, window int,
		) (*sessions.Store, error) {
			store, err := m.acquireStore(ctx, workDir, root, window)
			if err == nil {
				sessionStore = store
			}
			return store, err
		}),
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
	rt, err := engine.BuildRuntime(ctx, doc, buildOpts...)
	if err != nil {
		if sessionStore != nil {
			m.releaseStore(sessionStore)
		}
		return nil, err
	}
	ctrl := engine.NewController(rt)
	if resolver == nil {
		resolver = h.backendForRun
	}
	broker := interact.NewWithBackendResolver(rt, fallback, resolver)
	if err := broker.Attach(ctx); err != nil {
		telemetry.WarnErr(ctx, "host: close controller after broker attach failure",
			ctrl.Close())
		if sessionStore != nil {
			m.releaseStore(sessionStore)
		}
		return nil, err
	}
	value, ok := rt.Resource("sessions")
	store, ok2 := value.(*sessions.Store)
	if !ok || !ok2 || store == nil {
		broker.Close()
		telemetry.WarnErr(ctx, "host: close controller after session resource failure",
			ctrl.Close())
		if sessionStore != nil {
			m.releaseStore(sessionStore)
		}
		return nil, errors.New("host: session store resource missing")
	}
	h.store = store
	h.ctrl = ctrl
	h.broker = broker
	if value, ok := rt.Resource("agentlifecycle"); ok {
		if lifecycle, ok := value.(*ocsagents.Lifecycle); ok && lifecycle != nil {
			h.agents.Store(lifecycle)
		}
	}
	if value, ok := rt.Resource("hooks"); ok {
		if mgr, ok := value.(*hooks.Manager); ok && mgr != nil {
			h.hooks.Store(mgr)
		}
	}
	if value, ok := rt.Resource("artifacts"); ok {
		if obs, ok := value.(*sandbox.ArtifactObserver); ok && obs != nil {
			obs.SetSink(h.onArtifactWrite)
		}
	}
	h.attachRuntimeReloadObserver(ctx)
	return h, nil
}

// reportUsage routes an engine usage report to the run that owns it.
func (h *Host) reportUsage(ctx context.Context, usage inference.Usage) {
	runID := ""
	if info, ok := agent.RunInfoFromContext(ctx); ok {
		runID = info.RunID
	}
	delta := sessions.UsageFromReport(usage)
	h.mu.Lock()
	d := h.runs[RunID(runID)]
	if d == nil {
		h.mu.Unlock()
		return
	}
	d.usage = sessions.AddUsage(d.usage, delta)
	if delta.Model != "" && d.usageHours != nil {
		hour := time.Now().UTC().Truncate(time.Hour).Format(time.RFC3339)
		key := modelHourKey(delta.Model, hour)
		d.usageHours[key] = sessions.AddUsage(d.usageHours[key], delta)
	}
	fn := d.notify
	h.mu.Unlock()
	if fn != nil {
		fn(ctx, usage)
	}
}

func modelHourKey(model, hour string) string {
	return model + "\x00" + hour
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

// takeUsageDeltas drains the per-model, per-hour usage buckets of one
// run. Delays inside the run do not shift usage between hourly buckets
// because each report's hour was captured when the report arrived.
func (h *Host) takeUsageDeltas(runID string) []usageDelta {
	h.mu.Lock()
	defer h.mu.Unlock()
	d := h.runs[RunID(runID)]
	if d == nil || len(d.usageHours) == 0 {
		return nil
	}
	out := make([]usageDelta, 0, len(d.usageHours))
	for key, usage := range d.usageHours {
		model, hour, ok := strings.Cut(key, "\x00")
		if !ok || model == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339, hour)
		if err != nil {
			// Bucket keys are only written by reportUsage, so the hour
			// always parses. Ignore rather than silently mis-bucket.
			continue
		}
		usage.Model = model
		out = append(out, usageDelta{usage: usage, at: at})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].usage.Model != out[j].usage.Model {
			return out[i].usage.Model < out[j].usage.Model
		}
		return out[i].at.Before(out[j].at)
	})
	return out
}

// OpenSessions returns the shared Store for one workspace without
// assembling a Host. It runs the same first-open adoption and
// migration path as Host acquisition. Callers must ReleaseSessions.
func (m *Manager) OpenSessions(
	ctx context.Context, workDir string, layout config.WorkspaceLayout, window int,
) (*sessions.Store, error) {
	return m.acquireStore(ctx, workDir, layout.SessionsDir, window)
}

// ReleaseSessions drops one caller's reference to a shared Store.
func (m *Manager) ReleaseSessions(store *sessions.Store) {
	m.releaseStore(store)
}

// acquireStore opens one Store per root and reference-counts it.
// workDir supplies the v0.1.x project-local session location that is
// adopted into the new layout on first open.
func (m *Manager) acquireStore(
	ctx context.Context, workDir, root string, window int,
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

	// Serialize first-open adoption/schema work across Host assembly
	// and adapter-only store opens for every workspace.
	m.openMu.Lock()
	defer m.openMu.Unlock()

	m.mu.Lock()
	if ref := m.stores[root]; ref != nil {
		ref.refs++
		s := ref.store
		m.mu.Unlock()
		return s, nil
	}
	m.mu.Unlock()

	if err := migrations.AdoptLegacySessions(
		ctx, migrations.LegacySessionsDir(workDir), root,
	); err != nil {
		return nil, err
	}

	store, err := sessions.New(root, window)
	if err != nil {
		return nil, err
	}
	if err := migrations.Workspace(ctx, store.Database(), root); err != nil {
		telemetry.WarnErr(ctx, "host: close store after workspace migration failure",
			store.CloseDB())
		return nil, err
	}
	m.mu.Lock()
	if ref := m.stores[root]; ref != nil {
		telemetry.WarnErr(ctx, "host: close duplicate session store",
			store.CloseDB())
		ref.refs++
		s := ref.store
		m.mu.Unlock()
		return s, nil
	}
	m.stores[root] = &storeRef{store: store, refs: 1}
	m.mu.Unlock()
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
			telemetry.WarnErr(context.Background(),
				"host: close released session store failed", store.CloseDB())
		}
		return
	}
}

// Host is one shared workspace runtime.
type Host struct {
	workDir       string
	userDir       string
	workspaceID   string
	store         *sessions.Store
	ctrl          *engine.Controller
	broker        *interact.Broker
	manager       *Manager
	agents        atomic.Pointer[ocsagents.Lifecycle]
	hooks         atomic.Pointer[hooks.Manager]
	usage         func(context.Context, inference.Usage)
	usageRecorder UsageRecorder

	mu       sync.Mutex
	runs     map[RunID]*runDetail
	runsCond *sync.Cond
	rollouts map[ConversationID]*rollout.Recorder
	titling  map[ConversationID]bool
	titleWG  sync.WaitGroup
	// rebindMu serializes onRuntimeReload. ReloadDocument rebinds
	// synchronously before returning while the runtime event router
	// may dispatch the same rebuild event concurrently, and both must
	// never interleave resource Bind/LoadAll side effects for
	// different generations.
	rebindMu sync.Mutex
	// deleting marks one conversation whose removal is in flight.
	// StartRun refuses new runs for it and DeleteConversation cancels
	// every run the Host already owns for it.
	deleting map[ConversationID]bool
	// deleted tombstones conversations whose rows were removed for the
	// lifetime of this Host, so a stale explicit StartRun cannot mint
	// the same conversation id again.
	deleted map[ConversationID]bool
	// importMu serializes archive write + memory seed across callers
	// so a duplicate import with the same Source cannot double-seed.
	importMu   sync.Mutex
	artifact   func(context.Context, string, []byte)
	sessionUpd func(context.Context, string)
	// stale records a Manager retirement on the Host itself. The pool
	// entry carries the same flag while the Host is pooled; the
	// Host-level copy survives pool removal so adapters can still tell
	// a draining Host from a fresh replacement handed out by Acquire.
	stale   atomic.Bool
	closing bool
	closed  bool
	// closeDone is closed once a drained host has finished teardown;
	// active runs wait on it before returning so the shared session
	// store outlives every post-run write (including auto titles).
	closeDone chan struct{}
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
	// usageHours aggregates engine reports by model + UTC hour so a
	// multi-model or long turn still lands in the right user-level
	// statistics buckets.
	usageHours map[string]sessions.Usage
	notify     func(context.Context, inference.Usage)
	buffer     *rolloutBuffer
	manifest   map[string]fileStat
	backend    interact.Backend
}

// dropRun removes an ended run from the active set. Usage for the run
// must already have been captured before this is called.
func (h *Host) dropRun(runID RunID) {
	h.mu.Lock()
	delete(h.runs, runID)
	idle := len(h.runs) == 0
	h.runsCond.Broadcast()
	h.mu.Unlock()
	if idle {
		if m := h.manager; m != nil {
			m.hostIdle(h)
		}
	}
}

// hasActiveRuns reports whether the Host still owns live runs.
func (h *Host) hasActiveRuns() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.runs) > 0
}

// markStale records that the Manager retired this Host: it keeps
// serving new turns on its old runtime until the last active run ends,
// then closes itself through hostIdle.
func (h *Host) markStale() {
	h.stale.Store(true)
}

// IsStale reports whether the Manager retired this Host. Callers that
// receive a stale Host from Acquire must schedule a replacement for
// after teardown so the pool (and the adapter's current Host) never
// stays pinned to a closed runtime.
func (h *Host) IsStale() bool {
	return h != nil && h.stale.Load()
}

// IsClosing reports whether the Host stopped accepting new turns: it
// is either draining its live runs or already torn down. A closing
// Host is never returned by Manager.Acquire, so adapters can treat
// IsClosing on Runtime.current as "wait for the replacement Host".
func (h *Host) IsClosing() bool {
	if h == nil {
		return true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closing || h.closed
}

// WaitClosed blocks until the Host has fully torn down (or ctx is
// canceled). A nil or already-closed Host returns immediately.
func (h *Host) WaitClosed(ctx context.Context) error {
	if h == nil {
		return nil
	}
	for {
		h.mu.Lock()
		closed := h.closed
		closeDone := h.closeDone
		h.mu.Unlock()
		if closed || closeDone == nil {
			return nil
		}
		select {
		case <-closeDone:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (h *Host) waitClosed(ctx context.Context) error {
	return h.WaitClosed(ctx)
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

// notifySessionUpdated fires the installed session-title callback
// without holding h.mu while the callback runs.
func (h *Host) notifySessionUpdated(
	ctx context.Context, contextID string,
) {
	h.mu.Lock()
	fn := h.sessionUpd
	h.mu.Unlock()
	if fn != nil {
		fn(ctx, contextID)
	}
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
func (h *Host) Agents() *ocsagents.Lifecycle { return h.agents.Load() }

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
// its runtime and session store are torn down; hosts with active runs
// are drained in the background and only torn down after every turn
// finishes naturally.
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
		if m.retiring == nil {
			m.retiring = make(map[string]*Host)
		}
		m.retiring[h.workDir] = h
	}
	m.mu.Unlock()

	h.beginClose()
	return nil
}

// beginClose marks the Host as closing and starts teardown. Idle hosts
// drain and close synchronously; hosts with active runs drain in the
// background while the old runtime keeps serving their turns.
func (h *Host) beginClose() {
	h.mu.Lock()
	if h.closed || h.closing {
		h.mu.Unlock()
		return
	}
	h.closing = true
	active := len(h.runs) > 0
	h.mu.Unlock()
	if active {
		go h.closeWhenDrained()
		return
	}
	h.closeWhenDrained()
}

// closeWhenDrained waits for active runtime sessions through
// flowcraft's Drain API, then releases broker, runtime, and store.
// Drain never interrupts turns, so background streams finish first.
func (h *Host) closeWhenDrained() {
	ctx := context.WithoutCancel(context.Background())
	telemetry.WarnErr(ctx, "host: drain before close failed", h.drain(ctx))
	h.doClose()
}

func (h *Host) drain(ctx context.Context) error {
	ctrl := h.Controller()
	if ctrl == nil {
		return nil
	}
	return ctrl.Drain(ctx)
}

func (h *Host) doClose() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	// Run.Wait registers the post-run auto-title before it drops the
	// run from the active set, so waiting for the set to drain here
	// guarantees titleWG.Wait below can never observe an empty group
	// before that registration happens. flowcraft's drain returns as
	// soon as the engine turn ends, which can precede Run.Wait's own
	// bookkeeping; titleWG.Wait alone would then tear the shared store
	// down under the late title write.
	for len(h.runs) > 0 {
		h.runsCond.Wait()
	}
	h.closed = true
	closeDone := h.closeDone
	h.mu.Unlock()
	// Auto-titles borrow the runtime and shared session store after a
	// turn ends. Wait for them before tearing either down so a title
	// never races the DB close.
	h.titleWG.Wait()
	h.closeRollouts()
	h.broker.Close()
	telemetry.WarnErr(context.Background(), "host: close controller failed",
		h.ctrl.Close())
	if h.manager != nil {
		h.manager.releaseStore(h.store)
	}
	if closeDone != nil {
		close(closeDone)
	}
	if h.manager != nil {
		h.manager.hostClosed(h.workDir, h)
	}
}

// awaitCloseIfClosing blocks the final run wait on the host teardown
// when the host was invalidated mid-turn. Returning before teardown
// would let callers resume the workspace while the shared store was
// still owned by this host's post-run writes.
func (h *Host) awaitCloseIfClosing() {
	h.mu.Lock()
	closing := h.closing && !h.closed
	closeDone := h.closeDone
	h.mu.Unlock()
	if closing && closeDone != nil {
		<-closeDone
	}
}
