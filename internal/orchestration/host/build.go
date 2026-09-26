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
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/telemetry"

	ocsagents "github.com/GizClaw/opencraft/internal/capabilities/agents"
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
	ref := m.hosts[h.workDir]
	if fn == nil || ref == nil || ref.host != h || ref.configured {
		m.mu.Unlock()
		return
	}
	ref.configured = true
	m.mu.Unlock()
	fn(h)
}

// assembleShared runs the one assembly for a workspace and publishes
// its result: the Host enters the pool (or is closed when another
// caller installed one meanwhile), and every waiting Acquire is woken.
// The pool is updated before the wake-up, so a follower either finds
// the pooled Host on its next pass or replays the shared error.
func (m *Manager) assembleShared(
	ctx context.Context,
	workDir string,
	fallback interact.Backend,
	resolver func(runID string) interact.Backend,
	call *assemblyCall,
) (h *Host, err error) {
	// One exit point publishes the result: the in-flight entry goes away,
	// the error is recorded, and the waiters are released, whatever the
	// assembly did.
	defer func() {
		m.mu.Lock()
		delete(m.assembling, workDir)
		call.err = err
		m.mu.Unlock()
		close(call.done)
	}()
	h, err = m.assembleHost(ctx, workDir, fallback, resolver)
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

// assemble builds one Host without holding the manager lock and logs
// the one line every assembly is identified by: who asked for it, how
// long it took, and whether the app was busy while it ran.
func (m *Manager) assemble(
	ctx context.Context,
	workDir string,
	fallback interact.Backend,
	resolver func(runID string) interact.Backend,
) (*Host, error) {
	started := time.Now()
	h, err := m.buildHost(ctx, workDir, fallback, resolver)
	duration := time.Since(started)
	if err != nil {
		telemetry.WarnErr(ctx, "host: runtime assembly failed", err,
			otellog.String("reason", string(AssemblyReasonFrom(ctx))),
			otellog.String("workspace", workDir),
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
	m.assemblies[h.workDir]++
	seq := m.assemblies[h.workDir]
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

// buildHost builds one Host without holding the manager lock.
func (m *Manager) buildHost(
	ctx context.Context,
	workDir string,
	fallback interact.Backend,
	resolver func(runID string) interact.Backend,
) (*Host, error) {
	userDir := m.userDir
	dataDir := m.dataDir
	m.mu.Lock()
	appHome := m.appHome
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
		telemetry.Info(ctx, "host: config dir defaulted",
			otellog.String("config_dir", userDir))
	}
	if dataDir == "" {
		var err error
		dataDir, err = config.UserDataDir()
		if err != nil {
			telemetry.WarnErr(ctx, "host: resolve user data dir failed", err)
		}
		if dataDir != "" {
			telemetry.Info(ctx, "host: state root defaulted",
				otellog.String("state_root", dataDir))
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
		engine.WithConfigBase(userDir),
		engine.WithAppHome(appHome),
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
