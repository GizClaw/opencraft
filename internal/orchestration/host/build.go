// Assembly: building one Host (engine deployment, session store,
// interact broker) and the desktop event wiring that runs on every path
// that hands a Host out.

package host

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/deploy"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/telemetry"

	ocsagents "github.com/GizClaw/opencraft/internal/capabilities/agents"
	"github.com/GizClaw/opencraft/internal/capabilities/apps"
	"github.com/GizClaw/opencraft/internal/capabilities/hooks"
	"github.com/GizClaw/opencraft/internal/capabilities/rollout"
	"github.com/GizClaw/opencraft/internal/capabilities/sandbox"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/engine"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"

	otellog "go.opentelemetry.io/otel/log"
)

// SetHostConfigurator installs a callback applied once to every Host
// the pool hands out, before Acquire returns it. Adapters use it to
// wire UI observers (artifacts, session updates) without
// re-registering on shared hosts.
func (m *Manager) SetHostConfigurator(fn func(*Host)) {
	m.mu.Lock()
	m.hostConfigurator = fn
	m.mu.Unlock()
}

// SetAppRegistry wires the application registry every application
// assembly reads: the content root a target's layers are installed
// into, the layers themselves, the entry agent, and whether the user
// has the application enabled. Call it before the first Acquire for an
// application; a manager without one refuses app targets the way it
// refuses any kinds it cannot build (ErrNoAssembly) instead of
// assembling a workspace's runtime for them.
func (m *Manager) SetAppRegistry(store *apps.Store) {
	m.mu.Lock()
	m.appRegistry = store
	m.mu.Unlock()
}

// appRegistryStore returns the configured registry, if any.
func (m *Manager) appRegistryStore() *apps.Store {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.appRegistry
}

// configureHost applies the host configurator once per pooled Host, on
// every path that hands one out for work (Acquire, and Ensure's pooled
// branch). The callback runs without the pool lock held: adapter
// callbacks ask the pool questions of their own.
//
// A Host the pool has already forgotten is skipped instead of wired
// late: the apply-once marker rides the pool entry, and a missing (or
// replaced) entry means that Host retired. It is closing, and the paths
// that hand out work do not return one of those — Ensure refuses it,
// Acquire waits out its teardown.
func (m *Manager) configureHost(h *Host) {
	if h == nil {
		return
	}
	m.mu.Lock()
	fn := m.hostConfigurator
	ref := m.hosts[h.target.Key()]
	if fn == nil || ref == nil || ref.host != h || ref.configured {
		m.mu.Unlock()
		return
	}
	ref.configured = true
	m.mu.Unlock()
	fn(h)
}

// assembleShared runs the one assembly for a target and publishes
// its result: the Host enters the pool (or is closed when another
// caller installed one meanwhile), and every waiting Acquire is woken.
// The pool is updated before the wake-up, so a follower either finds
// the pooled Host on its next pass or replays the shared error.
func (m *Manager) assembleShared(
	ctx context.Context,
	t Target,
	fallback interact.Backend,
	resolver func(runID string) interact.Backend,
	call *assemblyCall,
) (h *Host, err error) {
	key := t.Key()
	// One exit point publishes the result: the in-flight entry goes away,
	// the error is recorded, and the waiters are released, whatever the
	// assembly did.
	defer func() {
		m.mu.Lock()
		delete(m.assembling, key)
		call.err = err
		m.mu.Unlock()
		close(call.done)
	}()
	h, err = m.assembleHost(ctx, t, fallback, resolver)
	if err != nil {
		return nil, err
	}
	// The Host has to carry the target it was asked for: a builder that
	// returns one for another target would be published under this
	// target's key, handed out as if it served t, and never found again
	// by Host.Close (which looks its pool entry up by its own target).
	// That can only be a builder bug, so it fails here, loudly, and the
	// Host never reaches the pool.
	if h == nil {
		return nil, fmt.Errorf("host: assembly returned no host for %s", t)
	}
	if h.target != t {
		built := h.target
		h.doClose()
		return nil, fmt.Errorf(
			"host: assembly returned a host for %s, want %s", built, t)
	}
	m.mu.Lock()
	if ref := m.hosts[key]; ref != nil {
		ref.refs++
		existing := ref.host
		m.mu.Unlock()
		// The freshly assembled host never reached the pool and has no
		// runs; close it outside the manager lock so it can release its
		// own session-store reference.
		h.doClose()
		return existing, nil
	}
	m.hosts[key] = &hostRef{host: h, target: t, refs: 1}
	m.mu.Unlock()
	return h, nil
}

// assemble builds one Host without holding the manager lock and logs
// the one line every assembly is identified by: who asked for it, how
// long it took, and whether the app was busy while it ran. The target
// kind decides how it is built; a kind with no builder is refused here
// rather than assembled as a workspace, so one scope can never be
// served by another scope's runtime.
func (m *Manager) assemble(
	ctx context.Context,
	t Target,
	fallback interact.Backend,
	resolver func(runID string) interact.Backend,
) (*Host, error) {
	started := time.Now()
	var h *Host
	var err error
	switch t.Kind {
	case TargetWorkspace:
		h, err = m.buildWorkspaceHost(ctx, t, fallback, resolver)
	case TargetApp:
		h, err = m.buildAppHost(ctx, t, fallback, resolver)
	default:
		err = fmt.Errorf("%w: %s", ErrNoAssembly, t)
	}
	duration := time.Since(started)
	if err != nil {
		telemetry.WarnErr(ctx, "host: runtime assembly failed", err,
			otellog.String("reason", string(AssemblyReasonFrom(ctx))),
			otellog.String("target", t.String()),
			otellog.Int64("duration_ms", duration.Milliseconds()))
		return nil, err
	}
	m.logAssembled(ctx, h, duration)
	return h, nil
}

// logAssembled reports one completed assembly. assembly_seq counts the
// workspace's assemblies in this process: the line that turns "turns
// feel slow" into "this workspace was assembled 33 times in a minute".
func (m *Manager) logAssembled(
	ctx context.Context,
	h *Host,
	duration time.Duration,
) {
	m.mu.Lock()
	if m.assemblies == nil {
		m.assemblies = make(map[string]int)
	}
	m.assemblies[h.target.Key()]++
	seq := m.assemblies[h.target.Key()]
	m.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	telemetry.Info(ctx, "host: runtime assembled",
		otellog.String("reason", string(AssemblyReasonFrom(ctx))),
		otellog.String("workspace", h.workDir),
		otellog.Int64("duration_ms", duration.Milliseconds()),
		otellog.Bool("in_turn", m.anyActiveRuns()),
		otellog.Int("assembly_seq", seq),
		otellog.String("host_ptr", fmt.Sprintf("%p", h)))
}

// roots are the process-wide values every assembly resolves against,
// read once so a concurrent SetAppHome/SetEngineOptionsFunc cannot
// change the answer mid-build.
type roots struct {
	userDir       string
	dataDir       string
	appHome       string
	usageObserver func(context.Context, inference.Usage)
	usageRecorder UsageRecorder
	engineOptions []engine.Option
}

// resolveRoots reads the manager's configured roots, defaulting the
// config and state directories the way an unconfigured manager always
// has, and snapshots the late-bound engine options an assembly runs
// with.
func (m *Manager) resolveRoots(ctx context.Context) (roots, error) {
	r := roots{userDir: m.userDir, dataDir: m.dataDir}
	m.mu.Lock()
	r.appHome = m.appHome
	r.usageObserver = m.usageObserver
	r.usageRecorder = m.usageRecorder
	engineOptFunc := m.engineOptFunc
	m.mu.Unlock()
	if engineOptFunc != nil {
		r.engineOptions = engineOptFunc()
	}
	if r.userDir == "" {
		var err error
		r.userDir, err = config.UserConfigDir()
		if err != nil {
			return roots{}, err
		}
		telemetry.Info(ctx, "host: config dir defaulted",
			otellog.String("config_dir", r.userDir))
	}
	if r.dataDir == "" {
		var err error
		r.dataDir, err = config.UserDataDir()
		if err != nil {
			telemetry.WarnErr(ctx, "host: resolve user data dir failed", err)
		}
		if r.dataDir != "" {
			telemetry.Info(ctx, "host: state root defaulted",
				otellog.String("state_root", r.dataDir))
		}
	}
	return r, nil
}

// hostPlan is what one assembly is for: the values that differ between
// a user workspace and an installed application. Everything past it —
// building the runtime, attaching the brokers, extracting the
// resources, the recovery pass, the reload observer, the delegation
// reflow — is the same for both, because none of it is about which
// directory the turns belong to.
type hostPlan struct {
	roots  roots
	target Target
	// layout is the target's state layout: the workspace's own, or the
	// application's root with its private workspace inside it.
	layout config.WorkspaceLayout
	// doc is the merged deployment document the runtime builds from.
	doc deploy.Document
	// agentID is the agent the runtime serves; empty reads as the
	// assistant (see Host.agentName).
	agentID string
	// fileBase anchors every {file:} reference inside doc. Empty keeps
	// the config base — the assistant's single root; an application's
	// layers resolve against its content root.
	fileBase string
	// adoptWorkDir is the work dir whose v0.1.x project-local session
	// store a first open migrates from. Empty skips that migration: an
	// application never had a project-local store.
	adoptWorkDir string
	// appDefaults are an application's manifest run defaults
	// (app.yaml defaults), applied to a turn that names neither a model
	// nor a level and whose conversation has none either. Empty for a
	// workspace, whose defaults are the user's own and arrive as the
	// page's request values.
	appDefaults apps.Defaults
}

// buildWorkspaceHost builds one user workspace's Host without holding
// the manager lock. t is a workspace target, and its ID — the cleaned
// work dir — is what the runtime, the state root and the session store
// are derived from.
func (m *Manager) buildWorkspaceHost(
	ctx context.Context,
	t Target,
	fallback interact.Backend,
	resolver func(runID string) interact.Backend,
) (*Host, error) {
	r, err := m.resolveRoots(ctx)
	if err != nil {
		return nil, err
	}
	layout, err := config.ResolveWorkspace(r.dataDir, t.ID)
	if err != nil {
		return nil, err
	}
	telemetry.WarnErr(ctx, "host: ensure workspace layout failed",
		layout.Ensure())
	doc, err := engine.LoadDocument(ctx, r.userDir)
	if err != nil {
		return nil, err
	}
	return m.buildHost(ctx, hostPlan{
		roots:        r,
		target:       t,
		layout:       layout,
		doc:          doc,
		agentID:      assistantAgent,
		adoptWorkDir: t.ID,
	}, fallback, resolver)
}

// buildAppHost builds one installed application's Host: the same
// runtime, brokers and cross-cutting wiring as a workspace's, over the
// application's own document, state root and private workspace. The
// four differences the plan spells out live here — the layout, the
// document, the file base and the identity — and nothing else does.
//
// The registry is consulted at assembly rather than anywhere earlier:
// the content root it names is the one the layers were installed into,
// and the two refusals below are the ones that keep a pool entry from
// outliving the application it serves — an application that was
// uninstalled or disabled between the page's decision and this call is
// refused here instead of running on.
func (m *Manager) buildAppHost(
	ctx context.Context,
	t Target,
	fallback interact.Backend,
	resolver func(runID string) interact.Backend,
) (*Host, error) {
	registry := m.appRegistryStore()
	if registry == nil {
		return nil, fmt.Errorf("%w: %s", ErrNoAssembly, t)
	}
	app, err := registry.Get(t.ID)
	if err != nil {
		return nil, fmt.Errorf("host: %s: %w", t, err)
	}
	if !app.Enabled {
		return nil, fmt.Errorf("%w: %s", ErrAppNotEnabled, t)
	}
	r, err := m.resolveRoots(ctx)
	if err != nil {
		return nil, err
	}
	layout, err := config.AppLayout(r.dataDir, t.ID)
	if err != nil {
		return nil, err
	}
	telemetry.WarnErr(ctx, "host: ensure application layout failed",
		layout.Ensure())
	doc, err := engine.LoadAppDocument(ctx, engine.AppDoc{
		ID:         app.ID,
		ContentDir: app.ContentDir,
		Layers:     app.Layers,
	}, r.userDir)
	if err != nil {
		return nil, err
	}
	return m.buildHost(ctx, hostPlan{
		roots:       r,
		target:      t,
		layout:      layout,
		doc:         doc,
		agentID:     app.Agent,
		fileBase:    app.ContentDir,
		appDefaults: app.Defaults,
	}, fallback, resolver)
}

// buildHost builds one Host from a plan: the one assembly path every
// target kind goes through, so a capability that lands here lands for
// workspaces and applications together.
func (m *Manager) buildHost(
	ctx context.Context,
	plan hostPlan,
	fallback interact.Backend,
	resolver func(runID string) interact.Backend,
) (*Host, error) {
	r := plan.roots
	layout := plan.layout
	t := plan.target
	h := &Host{
		target:        t,
		workDir:       layout.WorkDir,
		userDir:       r.userDir,
		agentID:       plan.agentID,
		appDefaults:   plan.appDefaults,
		workspaceID:   layout.ID,
		manager:       m,
		usage:         r.usageObserver,
		usageRecorder: r.usageRecorder,
		runs:          make(map[RunID]*runDetail),
		startGates:    make(map[ConversationID]*sync.Mutex),
		rollouts:      make(map[ConversationID]*rollout.Recorder),
		titling:       make(map[ConversationID]bool),
		deleting:      make(map[ConversationID]bool),
		deleted:       make(map[ConversationID]bool),
		closeDone:     make(chan struct{}),
	}
	h.runsCond = sync.NewCond(&h.mu)
	var sessionStore *sessions.Store
	buildOpts := append([]engine.Option{
		engine.WithConfigBase(r.userDir),
		engine.WithAppHome(r.appHome),
		engine.WithWorkBase(layout.WorkDir),
		engine.WithWorkspaceLayout(&layout),
		engine.WithSessionStore(func(
			ctx context.Context, root string, window int,
		) (*sessions.Store, error) {
			store, err := m.acquireStore(
				ctx, plan.adoptWorkDir, root, window)
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
	}, r.engineOptions...)
	if plan.fileBase != "" {
		buildOpts = append(buildOpts, engine.WithFileBase(plan.fileBase))
	}
	rt, err := engine.BuildRuntime(ctx, plan.doc, buildOpts...)
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
	if value, ok := rt.Resource("processes"); ok {
		if feed, ok := value.(*sandbox.ProcessFeed); ok && feed != nil {
			h.procs.Store(feed)
		}
	}
	// Materialize the turns a previous process never archived before
	// this Host starts serving reads: the window's first session read
	// must already see an interrupted turn instead of a gap.
	if prior, owed := m.claimRecovery(ctx, layout); !owed {
		h.setRecoveryReport(prior)
	} else {
		h.recoverInterruptedRuns(ctx)
		if report, ok := h.RecoveryReport(); ok {
			m.recordRecovery(layout.SessionsDir, report)
		}
	}
	h.attachRuntimeReloadObserver(ctx)
	// Route finished async delegations back into the conversation
	// that asked for them (see reflow.go).
	h.attachReflow(ctx, rt)
	return h, nil
}
