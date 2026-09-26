// Package engine assembles opencraft's runtime from a deploy document:
// embedded deploy assets, user-facing config, factory registration,
// platform sandbox selection, and lifecycle hooks.
package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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
	sbwindows "github.com/GizClaw/flowcraft/core/sandbox/windows"
	"github.com/GizClaw/flowcraft/core/secret"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"github.com/GizClaw/flowcraft/core/tool"
	"github.com/GizClaw/flowcraft/core/tool/mcp"
	"github.com/GizClaw/flowcraft/core/tool/middleware"
	"github.com/GizClaw/flowcraft/core/workspace"
	"github.com/GizClaw/flowcraft/driver/anthropic"
	"github.com/GizClaw/flowcraft/driver/bytedance"
	"github.com/GizClaw/flowcraft/driver/minimax"
	"github.com/GizClaw/flowcraft/driver/openai"
	"go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/capabilities/agents"
	"github.com/GizClaw/opencraft/internal/capabilities/execpolicy"
	"github.com/GizClaw/opencraft/internal/capabilities/hooks"
	opmedia "github.com/GizClaw/opencraft/internal/capabilities/media"
	opmemory "github.com/GizClaw/opencraft/internal/capabilities/memory"
	"github.com/GizClaw/opencraft/internal/capabilities/memory/userstore"
	pluginagent "github.com/GizClaw/opencraft/internal/capabilities/plugins/agent"
	"github.com/GizClaw/opencraft/internal/capabilities/review"
	reviewstore "github.com/GizClaw/opencraft/internal/capabilities/review/store"
	"github.com/GizClaw/opencraft/internal/capabilities/sandbox"
	"github.com/GizClaw/opencraft/internal/capabilities/secrets"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/skills"
	skillusage "github.com/GizClaw/opencraft/internal/capabilities/skills/usage"
	"github.com/GizClaw/opencraft/internal/capabilities/subagents"
	opentools "github.com/GizClaw/opencraft/internal/capabilities/tools"
	automationtool "github.com/GizClaw/opencraft/internal/capabilities/tools/automation"
	plugininstalltool "github.com/GizClaw/opencraft/internal/capabilities/tools/plugininstall"
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
	// AppHome is the shared content and credential root the deploy
	// document resolves ${ocraft:APP_HOME} to (keyring, plugins,
	// agents, user skills, hooks.json). Empty falls back to the
	// configuration directory's parent, which is the historical
	// single-root layout (~/.opencraft/config -> ~/.opencraft).
	AppHome string
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
	// PluginInstaller supplies the desktop plugin registry to the
	// agent plugin install tools. Nil yields an empty installer (no
	// tools exposed).
	PluginInstaller plugininstalltool.Installer
	// UserMemory supplies the user-level long-term memory store
	// (user.db). Nil yields the empty memory: no section is injected
	// and no remember tool is contributed.
	UserMemory userstore.Memory
	// SkillUsage supplies the skill lifecycle store (user.db). Nil
	// yields the empty lifecycle: nothing is recorded and no skill is
	// ever retired.
	SkillUsage skillusage.Lifecycle
	// ReviewQueue supplies the post-turn review queue (user.db). Nil
	// yields the empty queue: a review runs but cannot queue anything.
	ReviewQueue reviewstore.Queue
	// DelegationStreamResolver materializes a persisted delegation
	// stream target back into a live sink on the worker side. Nil
	// keeps escrow-only delivery for async delegations.
	DelegationStreamResolver sdkdelegation.StreamTargetResolver
	// DelegationStreamExporter describes the caller's live sink as a
	// durable target at async submit time. Nil means the delegation
	// carries no resolvable destination.
	DelegationStreamExporter sdkdelegation.StreamTargetExporter
	// SessionStore overrides session-store construction so every
	// runtime in one workspace shares a single Store.
	SessionStore func(
		ctx context.Context, root string, window int,
	) (*ocsessions.Store, error)
	// WorkspaceLayout explicitly supplies the workspace state root.
	// Required: BuildRuntime rejects a nil layout so assemblies never
	// fall back to project-local state.
	WorkspaceLayout *config.WorkspaceLayout
}

type Option func(*Options)

// LoadDocument loads the merged deploy document for one user config
// directory. Runtime assembly (Manager) and in-place reload (Host)
// share this entry point so both always operate on the same layer
// merge and cannot drift apart.
func LoadDocument(
	ctx context.Context,
	userDir string,
) (deploy.Document, error) {
	mgr, err := config.Open(config.Options{UserDir: userDir})
	if err != nil {
		return deploy.Document{}, fmt.Errorf("engine: open config: %w", err)
	}
	view, err := mgr.Load(ctx)
	if err != nil {
		return deploy.Document{}, fmt.Errorf("engine: load config: %w", err)
	}
	return view.Document, nil
}

// WithConfigBase overrides the config reference base directory.
func WithConfigBase(dir string) Option {
	return func(o *Options) { o.ConfigBase = dir }
}

// WithWorkBase overrides the sandbox/workspace root.
func WithWorkBase(dir string) Option {
	return func(o *Options) { o.WorkBase = dir }
}

// WithAppHome overrides the shared content/credential root the deploy
// document resolves ${ocraft:APP_HOME} to.
func WithAppHome(dir string) Option {
	return func(o *Options) { o.AppHome = dir }
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

// WithPluginInstaller injects the desktop plugin registry used by the
// agent's plugin install tools.
func WithPluginInstaller(i plugininstalltool.Installer) Option {
	return func(o *Options) { o.PluginInstaller = i }
}

// WithUserMemory injects the user-level long-term memory store.
func WithUserMemory(m userstore.Memory) Option {
	return func(o *Options) { o.UserMemory = m }
}

// WithSkillUsage injects the skill lifecycle store.
func WithSkillUsage(s skillusage.Lifecycle) Option {
	return func(o *Options) { o.SkillUsage = s }
}

// WithReviewQueue injects the post-turn review suggestion queue.
func WithReviewQueue(q reviewstore.Queue) Option {
	return func(o *Options) { o.ReviewQueue = q }
}

// WithDelegationStreams injects the app-owned halves of delegation
// stream delivery: the exporter that describes a caller-side sink as a
// durable target, and the resolver that materializes that target on
// the worker side. Both belong together — an exporter without a
// resolver persists a destination nothing can restore.
func WithDelegationStreams(
	resolver sdkdelegation.StreamTargetResolver,
	exporter sdkdelegation.StreamTargetExporter,
) Option {
	return func(o *Options) {
		o.DelegationStreamResolver = resolver
		o.DelegationStreamExporter = exporter
	}
}

// WithSessionStore overrides session store construction.
func WithSessionStore(fn func(
	ctx context.Context, root string, window int,
) (*ocsessions.Store, error),
) Option {
	return func(o *Options) { o.SessionStore = fn }
}

// WithWorkspaceLayout injects the typed workspace state layout.
func WithWorkspaceLayout(l *config.WorkspaceLayout) Option {
	return func(o *Options) { o.WorkspaceLayout = l }
}

// ocraftScheme names the resolver scheme carrying per-assembly path
// values into the deploy document (${ocraft:<NAME>} references). It
// replaces the historical ${env:OPEN_CRAFT_*} publishing: values now
// travel with the flowcraft Builder instead of the process
// environment, so assemblies for different workspaces never share
// mutable expansion state and may run concurrently.
const ocraftScheme = "ocraft"

// ocraftResolver maps the assembly path values into the ocraftScheme.
// The names mirror the retired OPEN_CRAFT_* environment variables;
// WORKSPACE_DIR and friends are only resolvable when a workspace
// layout is injected.
func ocraftResolver(o *Options, dataDir, cacheDir string) *resource.ReferenceResolver {
	values := map[string]string{
		"WORKDIR":  o.WorkBase,
		"CACHE":    cacheDir,
		"DATA_DIR": dataDir,
		"APP_HOME": o.AppHome,
	}
	if o.WorkspaceLayout != nil {
		values["WORKSPACE_DIR"] = o.WorkspaceLayout.Root
		values["SESSIONS_DIR"] = o.WorkspaceLayout.SessionsDir
		values["APPROVALS"] = o.WorkspaceLayout.ApprovalsFile
		values["TOOL_CACHE"] = o.WorkspaceLayout.CacheDir
		values["AUDIT_DIR"] = o.WorkspaceLayout.AuditDir
	}
	return resource.NewResolver(resource.SchemeFunc{
		SchemeName: ocraftScheme,
		Fn: func(_ context.Context, ref resource.Reference) (any, error) {
			value, ok := values[ref.Path]
			if !ok {
				return nil, fmt.Errorf(
					"engine: deploy document references unknown %s value %q",
					ocraftScheme, ref.Path)
			}
			return value, nil
		},
	})
}

// BuildRuntime assembles an opencraft runtime from a deploy document.
// Assembly path values are injected through a per-build resolver (see
// ocraftScheme), so concurrent calls for different workspaces do not
// race shared process state.
func BuildRuntime(ctx context.Context, doc deploy.Document, opts ...Option) (*runtimecore.Runtime, error) {
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
	if o.AppHome == "" {
		// The historical single-root layout: ~/.opencraft/config is the
		// config directory and ~/.opencraft the content root that
		// carries keyring/, plugins/ and agents/. The desktop passes the
		// resolved root explicitly (it can differ from the state root).
		o.AppHome = filepath.Dir(o.ConfigBase)
	}
	// Inference wiring is generated into the user configuration layer,
	// so nothing declares the router before the setup page runs. Say
	// that plainly instead of letting the graph fail on a missing
	// resource reference.
	configured, err := config.RouterConfigured(doc)
	if err != nil {
		return nil, fmt.Errorf("engine: check router configuration: %w", err)
	}
	if !configured {
		return nil, fmt.Errorf(
			"engine: inference is not configured: %s declares no router "+
				"generate target (configure a provider in the settings page)",
			filepath.Join(o.ConfigBase, "opencraft.yaml"))
	}
	// Tell the media hook which turns may carry a video part. A model
	// that cannot take video rejects the request instead of ignoring the
	// part, so the hook keeps flattening video to its path everywhere
	// else. This reads only the deployment document, so it stays a
	// build-time computation.
	if doc, err = withVideoHints(doc); err != nil {
		return nil, err
	}
	// The layout is required for everything below: the state-resolved
	// path values and the cache directory this assembly creates. A
	// missing layout used to fall back to the global user data root,
	// which silently assembled a workspace against the wrong root.
	if o.WorkspaceLayout == nil {
		return nil, fmt.Errorf("engine: workspace layout is required")
	}
	dataDir := o.WorkspaceLayout.DataDir
	cacheDir := filepath.Join(dataDir, "cache")
	for _, sub := range []string{"go", "tmp"} {
		if err := os.MkdirAll(filepath.Join(cacheDir, sub), 0o755); err != nil {
			return nil, err
		}
	}

	loader := resource.NewLoader(
		resource.WithBaseDir(o.ConfigBase),
		resource.WithEmbed(config.FS()),
	)
	reg := resource.NewRegistry()
	if err := registerResources(reg, &o); err != nil {
		return nil, err
	}

	builder := runtimecore.NewBuilder(reg)
	if err := builder.WithLoader(loader); err != nil {
		return nil, err
	}
	if err := builder.WithResolver(ocraftResolver(&o, dataDir, cacheDir)); err != nil {
		return nil, err
	}
	// plugin.host / automation.host are caller-owned values injected
	// through flowcraft's external dependency mechanism instead of
	// being built from registry factories. The deploy document declares
	// them under runtime.external_deps (assets/runtime.yaml); flowcraft
	// keeps these values across in-place Runtime.Reload generations, so
	// stable app objects (plugin store/capability runtime, automation
	// manager) never need re-registration on document reloads.
	pluginHost := o.AgentHost
	if pluginHost == nil {
		pluginHost = pluginagent.NewEmpty()
	}
	automationHost := o.AutomationHost
	if automationHost == nil {
		automationHost = automationtool.EmptyHost()
	}
	pluginInstaller := o.PluginInstaller
	if pluginInstaller == nil {
		pluginInstaller = plugininstalltool.EmptyInstaller()
	}
	userMemory := o.UserMemory
	if userMemory == nil {
		userMemory = userstore.Empty()
	}
	skillUsage := o.SkillUsage
	if skillUsage == nil {
		skillUsage = skillusage.EmptyLifecycle()
	}
	reviewQueue := o.ReviewQueue
	if reviewQueue == nil {
		reviewQueue = reviewstore.Empty()
	}
	for _, ext := range []runtimecore.ExternalResource{
		{
			ExternalDependency: runtimecore.ExternalDependency{
				Name:     "plugin.host",
				Contract: pluginagent.ResourceKind,
			},
			Value: pluginHost,
		},
		{
			ExternalDependency: runtimecore.ExternalDependency{
				Name:     "automation.host",
				Contract: automationtool.ResourceKind,
			},
			Value: automationHost,
		},
		{
			ExternalDependency: runtimecore.ExternalDependency{
				Name:     "plugin.installer",
				Contract: plugininstalltool.ResourceKind,
			},
			Value: pluginInstaller,
		},
		{
			ExternalDependency: runtimecore.ExternalDependency{
				Name:     "user.memory",
				Contract: userstore.StoreContract,
			},
			Value: userMemory,
		},
		{
			ExternalDependency: runtimecore.ExternalDependency{
				Name:     "skill.usage",
				Contract: skillusage.StoreContract,
			},
			Value: skillUsage,
		},
		{
			ExternalDependency: runtimecore.ExternalDependency{
				Name:     "review.queue",
				Contract: reviewstore.StoreContract,
			},
			Value: reviewQueue,
		},
	} {
		if err := builder.WithExternalResource(ext); err != nil {
			return nil, err
		}
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

// registerResources installs every resource factory this build ships:
// flowcraft's own kinds, the OS backends, the inference drivers,
// opencraft's capabilities, and the user-level store bindings. It is
// the one registry the embedded deploy assets are validated against
// (TestEmbeddedAssetsResolveAgainstRegistry): every (kind, impl) an
// asset names must resolve here. A missing registration — the windows
// sandbox backend was one — only fails at deploy time, and only for
// the asset that names it, so the scan is what keeps the list honest.
func registerResources(reg *resource.Registry, o *Options) error {
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
		// The windows backend must be registered even though no embedded
		// asset deploys it: a user layer that picks impl: windows would
		// otherwise fail assembly with "no factory", and the flowcraft
		// backend exists precisely for that choice (the opencraft impl
		// stays the default because hostsandbox.go owns the env policy
		// and approval chain around it).
		sbwindows.Register,
		anthropic.Register,
		bytedance.Register,
		minimax.Register,
		openai.Register,
		func(r *resource.Registry) error {
			return opmemory.RegisterWithObserver(r, o.usageObserver)
		},
		func(r *resource.Registry) error {
			if o.SessionStore == nil {
				return fmt.Errorf(
					"engine: session store requires WithSessionStore " +
						"(schema migration is centralized in internal/foundation/compat)")
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
			// The stream resolver/exporter are app-owned behavior the
			// document cannot express: they describe a live sink as a
			// durable destination (exporter) and turn that
			// destination back into a sink (resolver), so an async
			// delegation's stream survives losing its in-process
			// escrow. A deployment that injects neither keeps the
			// escrow-only behavior.
			var opts []sdkdelegation.Option
			if o.DelegationStreamResolver != nil {
				opts = append(opts,
					sdkdelegation.WithStreamTargetResolver(o.DelegationStreamResolver))
			}
			if o.DelegationStreamExporter != nil {
				opts = append(opts,
					sdkdelegation.WithStreamTargetExporter(o.DelegationStreamExporter))
			}
			return r.Register(sdkdelegation.NewServiceFactory(opts...))
		},
		func(r *resource.Registry) error {
			return r.Register(tooldelegation.NewSourceFactory())
		},
		execpolicy.Register,
	}
	for _, register := range registers {
		if err := register(reg); err != nil {
			return err
		}
	}
	reg.MustRegister(hooks.Factory{})
	reg.MustRegister(hooks.ObserverFactory{})
	// User-level state bindings: each pairs the caller-owned store the
	// host attached to user.db with that feature's document settings,
	// so consumers read one dependency instead of two.
	reg.MustRegister(userstore.Factory{})
	reg.MustRegister(skillusage.Factory{})
	reg.MustRegister(reviewstore.Factory{})
	reg.MustRegister(review.Factory{})
	reg.MustRegister(subagents.PolicyFactory{})
	return nil
}
