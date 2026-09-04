// Package engine assembles opencraft's runtime from a deploy document:
// embedded deploy assets, user-facing config, factory registration,
// platform sandbox selection, and lifecycle hooks.
package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/agent/scriptrt"
	sdkdelegation "github.com/GizClaw/flowcraft/core/delegation"
	delegationhostwrap "github.com/GizClaw/flowcraft/core/delegation/hostwrap"
	delegationkanban "github.com/GizClaw/flowcraft/core/delegation/kanban/resource"
	tooldelegation "github.com/GizClaw/flowcraft/core/delegation/tool"
	"github.com/GizClaw/flowcraft/core/deploy"
	"github.com/GizClaw/flowcraft/core/event"
	graphresource "github.com/GizClaw/flowcraft/core/graph/resource"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/route"
	"github.com/GizClaw/flowcraft/core/resource"
	runtimecore "github.com/GizClaw/flowcraft/core/runtime"
	sessions "github.com/GizClaw/flowcraft/core/runtime/session"
	"github.com/GizClaw/flowcraft/core/sandbox/bwrap"
	sandboxlocal "github.com/GizClaw/flowcraft/core/sandbox/local"
	"github.com/GizClaw/flowcraft/core/sandbox/seatbelt"
	"github.com/GizClaw/flowcraft/core/secret"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"github.com/GizClaw/flowcraft/core/tool"
	"github.com/GizClaw/flowcraft/core/tool/mcp"
	"github.com/GizClaw/flowcraft/core/tool/middleware"
	"github.com/GizClaw/flowcraft/core/workspace"
	"github.com/GizClaw/flowcraft/driver/anthropic"
	"github.com/GizClaw/flowcraft/driver/azure"
	"github.com/GizClaw/flowcraft/driver/bytedance"
	"github.com/GizClaw/flowcraft/driver/deepseek"
	"github.com/GizClaw/flowcraft/driver/kimi"
	"github.com/GizClaw/flowcraft/driver/minimax"
	"github.com/GizClaw/flowcraft/driver/openai"
	"github.com/GizClaw/flowcraft/driver/qwen"
	"go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/capabilities/agents"
	"github.com/GizClaw/opencraft/internal/capabilities/execpolicy"
	"github.com/GizClaw/opencraft/internal/capabilities/hooks"
	opmedia "github.com/GizClaw/opencraft/internal/capabilities/media"
	opmemory "github.com/GizClaw/opencraft/internal/capabilities/memory"
	pluginagent "github.com/GizClaw/opencraft/internal/capabilities/plugins/agent"
	"github.com/GizClaw/opencraft/internal/capabilities/sandbox"
	"github.com/GizClaw/opencraft/internal/capabilities/secrets"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/skills"
	opentools "github.com/GizClaw/opencraft/internal/capabilities/tools"
	automationtool "github.com/GizClaw/opencraft/internal/capabilities/tools/automation"
	"github.com/GizClaw/opencraft/internal/capabilities/worldstate"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// Options controls assembly paths.
type Options struct {
	// ConfigBase anchors the user configuration directory. Defaults to
	// ~/.opencraft/config.
	ConfigBase string
	// WorkBase is the sandbox/workspace root. Defaults to the current
	// working directory (where opencraft was invoked).
	WorkBase string
	// usageObserver receives every reported inference usage (including
	// the model actually invoked), with the run context so callers can
	// attribute usage to the owning turn. Nil disables observation.
	usageObserver func(context.Context, inference.Usage)
	// AgentHost supplies plugin-contributed agent capabilities
	// (skills, MCP, hooks, tools). Nil yields an empty host.
	AgentHost *pluginagent.Host
	// AutomationHost supplies scheduled-task persistence for the agent
	// automation tool. Nil yields an empty host (no tools exposed).
	AutomationHost automationtool.Host
	// SessionStore overrides session-store construction so every
	// runtime in one workspace shares a single Store.
	SessionStore func(
		ctx context.Context, root string, window int,
	) (*ocsessions.Store, error)
	// WorkspaceLayout explicitly supplies the workspace state root.
	// Nil keeps legacy project-local paths for current callers.
	WorkspaceLayout *config.WorkspaceLayout
}

type Option func(*Options)

// WithConfigBase overrides the config reference base directory.
func WithConfigBase(dir string) Option {
	return func(o *Options) { o.ConfigBase = dir }
}

// WithWorkBase overrides the sandbox/workspace root.
func WithWorkBase(dir string) Option {
	return func(o *Options) { o.WorkBase = dir }
}

// WithUsageObserver installs a usage-report observer. The callback runs
// on the engine's goroutine and must be non-blocking.
func WithUsageObserver(fn func(context.Context, inference.Usage)) Option {
	return func(o *Options) { o.usageObserver = fn }
}

// WithAgentPlugins injects the desktop plugin host into the runtime.
func WithAgentPlugins(h *pluginagent.Host) Option {
	return func(o *Options) { o.AgentHost = h }
}

// WithAutomationHost injects the desktop automation host used by the
// agent's automation tool.
func WithAutomationHost(h automationtool.Host) Option {
	return func(o *Options) { o.AutomationHost = h }
}

// WithSessionStore overrides session store construction.
func WithSessionStore(fn func(
	ctx context.Context, root string, window int,
) (*ocsessions.Store, error)) Option {
	return func(o *Options) { o.SessionStore = fn }
}

// WithWorkspaceLayout injects the typed workspace state layout.
func WithWorkspaceLayout(l *config.WorkspaceLayout) Option {
	return func(o *Options) { o.WorkspaceLayout = l }
}

// buildMu serializes runtime assembly. The flowcraft resource builder
// expands ${env:OPEN_CRAFT_*} paths from the process environment, so
// assemblies for different workspaces must never overlap.
var buildMu sync.Mutex

// BuildRuntime assembles an opencraft runtime from a deploy document.
func BuildRuntime(ctx context.Context, doc deploy.Document, opts ...Option) (*runtimecore.Runtime, error) {
	buildMu.Lock()
	defer buildMu.Unlock()

	o := Options{}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	if o.ConfigBase == "" {
		var err error
		o.ConfigBase, err = config.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("engine: user config dir: %w", err)
		}
	}
	if o.WorkBase == "" {
		var err error
		o.WorkBase, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("engine: workdir: %w", err)
		}
	}
	dataDir := ""
	if o.WorkspaceLayout != nil {
		dataDir = o.WorkspaceLayout.DataDir
	}
	if dataDir == "" {
		var err error
		dataDir, err = config.UserDataDir()
		if err != nil {
			return nil, err
		}
	}
	cacheDir := filepath.Join(dataDir, "cache")
	for _, sub := range []string{"go", "tmp"} {
		if err := os.MkdirAll(filepath.Join(cacheDir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	if o.WorkspaceLayout == nil {
		return nil, fmt.Errorf("engine: workspace layout is required")
	}
	// Scalar settings in the deploy document reference runtime paths
	// through ${env:...}; publish them before resources are built.
	// buildMu keeps concurrent assemblies from racing these values.
	envVars := []struct {
		name  string
		value string
	}{
		{"OPEN_CRAFT_WORKDIR", o.WorkBase},
		{"OPEN_CRAFT_CACHE", cacheDir},
		{"OPEN_CRAFT_DATA_DIR", dataDir},
		{"OPEN_CRAFT_WORKSPACE_DIR", o.WorkspaceLayout.Root},
		{"OPEN_CRAFT_SESSIONS_DIR", o.WorkspaceLayout.SessionsDir},
		{"OPEN_CRAFT_APPROVALS", o.WorkspaceLayout.ApprovalsFile},
		{"OPEN_CRAFT_TOOL_CACHE", o.WorkspaceLayout.CacheDir},
		{"OPEN_CRAFT_AUDIT_DIR", o.WorkspaceLayout.AuditDir},
	}
	for _, kv := range envVars {
		telemetry.WarnErr(ctx, "engine: publish runtime env failed",
			os.Setenv(kv.name, kv.value), log.String("name", kv.name))
	}

	loader := resource.NewLoader(
		resource.WithBaseDir(o.ConfigBase),
		resource.WithEmbed(config.FS()),
	)
	reg := resource.NewRegistry()
	registers := []func(*resource.Registry) error{
		event.Register,
		graphresource.Register,
		workspace.Register,
		tool.Register,
		middleware.Register,
		mcp.Register,
		secret.Register,
		inference.Register,
		route.Register,
		scriptrt.Register,
		sandboxlocal.Register,
		bwrap.Register,
		seatbelt.Register,
		anthropic.Register,
		azure.Register,
		bytedance.Register,
		deepseek.Register,
		kimi.Register,
		minimax.Register,
		openai.Register,
		qwen.Register,
		func(r *resource.Registry) error {
			return opmemory.RegisterWithObserver(r, o.usageObserver)
		},
		func(r *resource.Registry) error {
			if o.SessionStore == nil {
				return fmt.Errorf(
					"engine: session store requires WithSessionStore " +
						"(schema migration is centralized in orchestration/migrations)")
			}
			return r.Register(ocsessions.Factory{StoreFor: o.SessionStore})
		},
		opmedia.Register,
		skills.Register,
		opentools.Register,
		sandbox.Register,
		secrets.Register,
		worldstate.Register,
		func(r *resource.Registry) error {
			return r.Register(agents.Factory{})
		},
		delegationkanban.Register,
		sdkdelegation.RegisterDirectory,
		sdkdelegation.RegisterSessionProvider,
		func(r *resource.Registry) error {
			return r.Register(sdkdelegation.NewServiceFactory())
		},
		func(r *resource.Registry) error {
			return r.Register(tooldelegation.NewSourceFactory())
		},
		execpolicy.Register,
	}
	for _, register := range registers {
		if err := register(reg); err != nil {
			return nil, err
		}
	}
	reg.MustRegister(hooks.Factory{})
	reg.MustRegister(hooks.ObserverFactory{})
	reg.MustRegister(pluginagent.Factory{Host: o.AgentHost})
	reg.MustRegister(automationtool.Factory{Host: o.AutomationHost})

	builder := runtimecore.NewBuilder(reg)
	if err := builder.WithLoader(loader); err != nil {
		return nil, err
	}
	if err := builder.WithHostFactory(func(
		base sessions.HostFactory,
	) (sessions.HostFactory, error) {
		return sessions.HostFactoryFunc(func(
			ctx context.Context,
			req sessions.HostRequest,
		) (agent.Host, error) {
			host, err := base.NewHost(ctx, req)
			if err != nil {
				return nil, err
			}
			hf := agent.HostFuncs{Inner: host}
			if o.usageObserver != nil {
				observer := o.usageObserver
				hf.ReportUsageFn = func(
					ctx context.Context,
					usage inference.Usage,
				) error {
					if err := host.ReportUsage(ctx, usage); err != nil {
						return err
					}
					observer(ctx, usage)
					return nil
				}
			}
			return hf, nil
		}), nil
	}); err != nil {
		return nil, err
	}
	// Expose the delegation service on every turn host so the
	// delegate / delegation_status tools can resolve it at call time.
	if err := builder.WithResultHostFactory(func(
		result *deploy.Result,
		factory sessions.HostFactory,
	) (sessions.HostFactory, error) {
		return delegationhostwrap.Wrap(factory, result)
	}); err != nil {
		return nil, err
	}
	rt, err := builder.Build(ctx, doc)
	if err != nil {
		return nil, err
	}
	// Install the runtime so create_agent / unregister_agent and the
	// startup loader can register agents, then re-register every
	// persisted declaration. Failures never fail startup: a broken or
	// conflicting declaration must not block the runtime.
	if value, ok := rt.Resource(agentsResourceName); ok {
		if lifecycle, ok := value.(*agents.Lifecycle); ok {
			lifecycle.Bind(rt)
			for _, failure := range lifecycle.LoadAll(ctx) {
				telemetry.Warn(ctx, "agents: load declaration failed",
					log.String("agent", failure.Name),
					log.String("error", failure.Err.Error()))
			}
		}
	}
	return rt, nil
}

// agentsResourceName is the deploy-document resource id of the
// persistent subagent registry.
const agentsResourceName = "agentlifecycle"
