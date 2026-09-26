// Late-bound engine options: the values adapters inject after
// construction, plus the option assembly that reads them.

package host

import (
	"context"

	"github.com/GizClaw/flowcraft/core/delegation"

	"github.com/GizClaw/opencraft/internal/capabilities/memory/userstore"
	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
	pluginagent "github.com/GizClaw/opencraft/internal/capabilities/plugins/agent"
	pluginruntime "github.com/GizClaw/opencraft/internal/capabilities/plugins/runtime"
	reviewstore "github.com/GizClaw/opencraft/internal/capabilities/review/store"
	skillusage "github.com/GizClaw/opencraft/internal/capabilities/skills/usage"
	automationtool "github.com/GizClaw/opencraft/internal/capabilities/tools/automation"
	plugininstalltool "github.com/GizClaw/opencraft/internal/capabilities/tools/plugininstall"
	"github.com/GizClaw/opencraft/internal/orchestration/engine"
)

// SetAppHome installs the shared content root this manager's assemblies
// resolve ${ocraft:APP_HOME} to. Call it before the first assembly;
// empty keeps the historical single-root layout.
func (m *Manager) SetAppHome(dir string) {
	m.mu.Lock()
	m.appHome = dir
	m.mu.Unlock()
}

// SetLeaseKind names this process in the per-workspace lock file it
// keeps ("gui", "headless"). It is diagnostics only — the lock itself
// decides who may recover — but it is what makes "who holds this
// workspace" answerable after the fact.
func (m *Manager) SetLeaseKind(kind string) {
	m.mu.Lock()
	m.leaseKind = kind
	m.mu.Unlock()
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
	m.mu.Lock()
	m.pluginStore = store
	m.pluginCap = cap
	m.mu.Unlock()
	m.refreshEngineOptions()
}

// SetAutomationHost wires the scheduled-task persistence host into every
// runtime assembly. A nil host keeps the engine's empty-host fallback so
// headless runtimes simply expose no automation tools.
func (m *Manager) SetAutomationHost(h automationtool.Host) {
	m.mu.Lock()
	m.automationHost = h
	m.mu.Unlock()
	m.refreshEngineOptions()
}

// SetPluginInstaller wires the desktop plugin registry into every
// runtime assembly, exposing the agent's plugin install/update tools.
// A nil installer keeps the engine's empty fallback so headless
// runtimes simply expose no plugin authoring tools.
func (m *Manager) SetPluginInstaller(i plugininstalltool.Installer) {
	m.mu.Lock()
	m.pluginInstall = i
	m.mu.Unlock()
	m.refreshEngineOptions()
}

// SetDelegationStreams wires the shell's delegation stream delivery:
// the exporter describes a live conversation sink as a durable target
// at async submit time, the resolver materializes that target back
// into a sink. Passing nil for either leaves the runtime on the
// in-process escrow path (the headless behaviour).
func (m *Manager) SetDelegationStreams(
	resolver delegation.StreamTargetResolver,
	exporter delegation.StreamTargetExporter,
) {
	m.mu.Lock()
	m.delegationStreamResolver = resolver
	m.delegationStreamExporter = exporter
	m.mu.Unlock()
	m.refreshEngineOptions()
}

// refreshEngineOptions reinstalls the engine option builder so plugin,
// plugin-installer and automation hosts are all injected into every
// runtime assembly.
func (m *Manager) refreshEngineOptions() {
	m.SetEngineOptionsFunc(func() []engine.Option {
		m.mu.Lock()
		defer m.mu.Unlock()
		var opts []engine.Option
		if m.pluginCap != nil {
			opts = append(opts, engine.WithAgentPlugins(
				pluginagent.NewHost(context.Background(), m.pluginStore, m.pluginCap),
			))
		}
		if m.automationHost != nil {
			opts = append(opts, engine.WithAutomationHost(m.automationHost))
		}
		if m.pluginInstall != nil {
			opts = append(opts, engine.WithPluginInstaller(m.pluginInstall))
		}
		if m.delegationStreamResolver != nil && m.delegationStreamExporter != nil {
			opts = append(opts, engine.WithDelegationStreams(
				m.delegationStreamResolver, m.delegationStreamExporter,
			))
		}
		opts = append(opts,
			engine.WithUserMemory(m.userMemoryValue()),
			engine.WithSkillUsage(m.skillUsageValue()),
			engine.WithReviewQueue(m.reviewQueueValue()),
		)
		return opts
	})
}

// userMemoryValue returns the memory store as the engine's dependency
// interface: the empty memory before user.db is open, so an assembly
// that runs early still resolves the deploy graph and simply exposes
// neither the injected section nor the remember tool.
func (m *Manager) userMemoryValue() userstore.Memory {
	if m.userMemory == nil {
		return userstore.Empty()
	}
	return m.userMemory
}

// skillUsageValue returns the skill lifecycle store, or the empty
// lifecycle before user.db is open.
func (m *Manager) skillUsageValue() skillusage.Lifecycle {
	if m.userSkillUsage == nil {
		return skillusage.EmptyLifecycle()
	}
	return m.userSkillUsage
}

// reviewQueueValue returns the review queue, or the empty queue before
// user.db is open.
func (m *Manager) reviewQueueValue() reviewstore.Queue {
	if m.userReview == nil {
		return reviewstore.Empty()
	}
	return m.userReview
}
