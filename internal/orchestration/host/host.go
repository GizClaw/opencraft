// Package host owns one assembled workspace runtime for all callers in
// the process. UI turns and automation turns share the same Host and
// the same sessions.Store; prompt backends differ per run through the
// interact resolver.

package host

import (
	"context"
	"sync"

	"github.com/GizClaw/flowcraft/core/delegation"
	"github.com/GizClaw/flowcraft/core/inference"

	"github.com/GizClaw/opencraft/internal/capabilities/automations"
	"github.com/GizClaw/opencraft/internal/capabilities/execd"
	"github.com/GizClaw/opencraft/internal/capabilities/memory/userstore"
	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
	pluginruntime "github.com/GizClaw/opencraft/internal/capabilities/plugins/runtime"
	reviewstore "github.com/GizClaw/opencraft/internal/capabilities/review/store"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	skillusage "github.com/GizClaw/opencraft/internal/capabilities/skills/usage"
	metricstore "github.com/GizClaw/opencraft/internal/capabilities/telemetry/metric"
	automationtool "github.com/GizClaw/opencraft/internal/capabilities/tools/automation"
	plugininstalltool "github.com/GizClaw/opencraft/internal/capabilities/tools/plugininstall"
	"github.com/GizClaw/opencraft/internal/capabilities/usage"
	"github.com/GizClaw/opencraft/internal/foundation/db"
	"github.com/GizClaw/opencraft/internal/foundation/platform/wslock"
	"github.com/GizClaw/opencraft/internal/orchestration/engine"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
)

// Manager pools Hosts by workspace and keeps one sessions.Store per
// workspace root. Document-only configuration reloads go through
// Host.ReloadDocument in place; Manager invalidation (and the
// stale/retire machinery below) is reserved for engine-input changes
// (plugin install/uninstall, workspace switches) and fallback rebuilds.
type Manager struct {
	userDir string
	dataDir string
	// appHome is the shared content/credential root (keyring/,
	// plugins/, agents/, user skills). It follows the config
	// directory's parent until SetAppHome replaces it; the deploy
	// document resolves ${ocraft:APP_HOME} to this value.
	appHome string

	mu             sync.Mutex
	openMu         sync.Mutex
	hosts          map[string]*hostRef
	stores         map[string]*storeRef
	engineOptFunc  func() []engine.Option
	pluginStore    *plugins.Store
	pluginCap      *pluginruntime.Manager
	pluginInstall  plugininstalltool.Installer
	automationHost automationtool.Host
	usageObserver  func(context.Context, inference.Usage)
	usageRecorder  UsageRecorder
	// retiring maps a workspace root to a Host that was removed from
	// the pool and is draining its last runs. Acquire waits for these
	// hosts to finish teardown instead of assembling a second Host for
	// the same workspace, which would let two runtimes serve one
	// conversation concurrently.
	retiring map[string]*Host
	// assembling holds the in-flight assembly per workspace so
	// concurrent Acquire calls share one build instead of racing
	// (see assemblyCall). Entries live only while assembleHost runs.
	assembling map[string]*assemblyCall
	// hostConfigurator is applied once per pooled Host, before the pool
	// hands it out (see SetHostConfigurator). The apply-once marker
	// rides the pool entry, so nothing here keeps a retired Host
	// reachable.
	hostConfigurator func(*Host)
	// replacementHooks carry the adapter's half of the deferred
	// rebuild (see SetReplacementHooks).
	replacementHooks ReplacementHooks
	// armed holds the workspaces whose deferred replacement is already
	// scheduled (see ScheduleReplacement).
	armed map[string]struct{}
	// closeHost is the teardown entry point. It is a field so tests
	// can substitute a fake close without spinning up a runtime.
	closeHost func(*Host)
	// assembleHost is the assembly entry point behind Acquire. It is a
	// field so tests can substitute a fake build without spinning up a
	// runtime; production always uses (*Manager).assemble.
	assembleHost func(
		context.Context,
		string,
		interact.Backend,
		func(string) interact.Backend,
	) (*Host, error)
	// assemblies counts one workspace's runtime assemblies in this
	// process. A rebuild storm is visible as this number climbing: a
	// single workspace is supposed to assemble once per engine-input
	// change, not once per turn.
	assemblies map[string]int
	// recovered records, by session root, what this process's
	// crash-recovery pass did. Recovery is idempotent by itself; the
	// guard keeps a runtime reload from re-scanning, and the stored
	// summary lets the Host a reload assembles report the pass the
	// previous one ran instead of claiming nothing happened.
	recovered map[string]RecoveryReport
	// leases holds this process's advisory lock on each workspace state
	// root it assembled. The lock is what tells a process starting later
	// that this one is still alive and may be mid-run (see
	// foundation/platform/wslock); it lives for the process lifetime, so it
	// is deliberately not released when a Host closes.
	leases map[string]*wslock.Handle
	// acquireLease is the lock entry point. It is a field so tests can
	// present a foreign holder without spawning one.
	acquireLease func(ctx context.Context, path, kind string) (*wslock.Handle, error)
	// leaseKind names this process in the lock file ("gui", "headless").
	leaseKind string

	// User-level database state, opened on demand by OpenUserDB.
	userDB          *db.DB
	userUsage       *usage.Store
	userAutomations *automations.Store
	userMetrics     *metricstore.Store
	userMemory      *userstore.Store
	userSkillUsage  *skillusage.Store
	userReview      *reviewstore.Store

	// Delegation stream delivery, injected by the desktop shell: the
	// resolver/exporter pair that keeps an async delegation's stream
	// destination durable. Nil in headless deployments, which keep the
	// in-process escrow path.
	delegationStreamResolver delegation.StreamTargetResolver
	delegationStreamExporter delegation.StreamTargetExporter
}

type hostRef struct {
	host  *Host
	refs  int
	stale bool
	// configured records that the host configurator already ran for
	// this Host. The marker rides the pool entry on purpose: it goes
	// away together with the entry, so a retired Host — and the
	// runtime it owns, from the skills search index to the MCP
	// clients — stays collectable (see TestRebuiltHostIsCollectable).
	configured bool
}

// assemblyCall is one in-flight assembly shared by every Acquire caller
// that asked for the same workspace while it ran. A rebuild storm
// (several invalidations arming replacements for one workspace, all
// waking when the old Host drains) used to assemble one runtime per
// waker and close all but the first; followers now wait for the leader
// and reuse its result, error included, so a failed assembly is not
// retried once per waiter.
type assemblyCall struct {
	done chan struct{}
	err  error
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
	m.assembleHost = m.assemble
	m.usageRecorder = m.recordUserUsage
	return m
}

// NewManagerAt creates a manager with explicit user data and config
// roots.
func NewManagerAt(dataDir, userDir string) *Manager {
	m := NewManager(userDir)
	m.dataDir = dataDir
	// The exec supervisor keeps its orphan journal under the user data
	// root. Resolving that root is assembly's job (AGENTS.md §6), so
	// execd is handed the directory instead of looking it up itself.
	execd.SetJournalRoot(dataDir)
	return m
}
