// Package engine assembles opencraft's runtime from a deploy document:
// embedded deploy assets, user-facing config, factory registration,
// platform sandbox selection, and lifecycle hooks.
//
// The assembly itself is craft's: one Craft per Host, its definition the
// embedded layers this binary ships, its overlay the on-disk layers a
// reload re-reads, and its capabilities the factories and decorators
// below. What stays here is the application's half — which factories
// exist, which caller-owned values are injected, and which values the
// deploy document resolves through ${ocraft:...}.
package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/GizClaw/flowcraft/backends/secret/keychain"
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
	"github.com/GizClaw/flowcraft/craft"
	"github.com/GizClaw/flowcraft/driver/anthropic"
	"github.com/GizClaw/flowcraft/driver/bytedance"
	"github.com/GizClaw/flowcraft/driver/minimax"
	"github.com/GizClaw/flowcraft/driver/openai"
	"go.opentelemetry.io/otel/attribute"

	"github.com/GizClaw/flowcraft/core/hooks"
	mediahook "github.com/GizClaw/flowcraft/core/media/hook"

	"github.com/GizClaw/opencraft/internal/capabilities/agents"
	"github.com/GizClaw/opencraft/internal/capabilities/execpolicy"
	opmemory "github.com/GizClaw/opencraft/internal/capabilities/memory"
	"github.com/GizClaw/opencraft/internal/capabilities/memory/userstore"
	pluginagent "github.com/GizClaw/opencraft/internal/capabilities/plugins/agent"
	"github.com/GizClaw/opencraft/internal/capabilities/review"
	reviewstore "github.com/GizClaw/opencraft/internal/capabilities/review/store"
	"github.com/GizClaw/opencraft/internal/capabilities/sandbox"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/skills"
	skillusage "github.com/GizClaw/opencraft/internal/capabilities/skills/usage"
	"github.com/GizClaw/opencraft/internal/capabilities/subagents"
	opentools "github.com/GizClaw/opencraft/internal/capabilities/tools"
	automationtool "github.com/GizClaw/opencraft/internal/capabilities/tools/automation"
	plugininstalltool "github.com/GizClaw/opencraft/internal/capabilities/tools/plugininstall"
	"github.com/GizClaw/opencraft/internal/capabilities/worldstate"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/version"
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
	// Required: BuildAssembly rejects a nil layout so assemblies never
	// fall back to project-local state.
	WorkspaceLayout *config.WorkspaceLayout
}

type Option func(*Options)

// LoadView loads the merged deployment view for one user config
// directory: the document, and the layer split it was merged from.
// Runtime assembly (BuildAssembly) and in-place reload
// (Controller.Reload) share this entry point so both always operate on
// the same layers and cannot drift apart.
func LoadView(ctx context.Context, userDir string) (*config.View, error) {
	mgr, err := config.Open(config.Options{UserDir: userDir})
	if err != nil {
		return nil, fmt.Errorf("engine: open config: %w", err)
	}
	view, err := mgr.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("engine: load config: %w", err)
	}
	return view, nil
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
// travel with the assembly instead of the process environment, so
// assemblies for different workspaces never share mutable expansion
// state and may run concurrently.
//
// It stays a capability scheme rather than craft's ${craft:NAME} values
// because the embedded assets spell the references this way; the two
// spellings meet when the assets become a craft.yaml.
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

// registrar adapts one factory registration into a capability. Most of
// the registry's kinds are registered by a package-level function with
// no state, which is exactly what a Registrar is.
type registrar struct {
	name string
	fn   func(*resource.Registry) error
}

func (r registrar) Name() string { return r.name }

func (r registrar) Register(reg *resource.Registry) error { return r.fn(reg) }

// coreCapabilities is the framework half of the factory list:
// flowcraft's own kinds, the OS sandbox backends and the inference
// drivers. Every (kind, impl) an embedded asset names must resolve
// here, which is what TestEmbeddedAssetsResolveAgainstRegistry scans
// for: a missing registration only fails at deploy time, and only for
// the asset that names it.
func coreCapabilities() []craft.Capability {
	return []craft.Capability{
		registrar{"events", event.Register},
		registrar{"graph", graphresource.Register},
		registrar{"workspace", workspace.Register},
		registrar{"tools", tool.Register},
		registrar{"tool-middleware", middleware.Register},
		registrar{"mcp", mcp.Register},
		registrar{"secrets", secret.Register},
		registrar{"inference", inference.Register},
		registrar{"inference-route", route.Register},
		registrar{"script-runtime", scriptrt.Register},
		registrar{"sandbox-local", sandboxlocal.Register},
		registrar{"sandbox-bwrap", bwrap.Register},
		registrar{"sandbox-seatbelt", seatbelt.Register},
		// The windows backend must be registered even though no embedded
		// asset deploys it: a user layer that picks impl: windows would
		// otherwise fail assembly with "no factory", and the flowcraft
		// backend exists precisely for that choice (the opencraft impl
		// stays the default because hostsandbox.go owns the env policy
		// and approval chain around it).
		registrar{"sandbox-windows", sbwindows.Register},
		registrar{"driver-anthropic", anthropic.Register},
		registrar{"driver-bytedance", bytedance.Register},
		registrar{"driver-minimax", minimax.Register},
		registrar{"driver-openai", openai.Register},
	}
}

// appCapabilities is the application half of the factory list: the
// kinds this repository ships, and the caller-owned stores they read.
func appCapabilities(o *Options) []craft.Capability {
	return []craft.Capability{
		registrar{"memory", func(r *resource.Registry) error {
			return opmemory.RegisterWithObserver(r, o.usageObserver)
		}},
		registrar{"sessions", func(r *resource.Registry) error {
			if o.SessionStore == nil {
				return fmt.Errorf(
					"engine: session store requires WithSessionStore " +
						"(schema migration is centralized in internal/foundation/compat)")
			}
			return r.Register(ocsessions.Factory{StoreFor: o.SessionStore})
		}},
		registrar{"media-hooks", mediahook.Register},
		registrar{"skills", skills.Register},
		registrar{"tools-opencraft", opentools.Register},
		registrar{"sandbox", sandbox.Register},
		registrar{"keychain", keychain.Register},
		registrar{"worldstate", worldstate.Register},
		registrar{"agents", func(r *resource.Registry) error {
			return r.Register(agents.Factory{})
		}},
		registrar{"delegation-kanban", delegationkanban.Register},
		registrar{"delegation-directory", sdkdelegation.RegisterDirectory},
		registrar{"delegation-sessions", sdkdelegation.RegisterSessionProvider},
		registrar{"delegation", func(r *resource.Registry) error {
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
			// The session initializer rides along: a delegated run is
			// handed the caller's sandbox mode instead of starting on
			// the store's confined default (see
			// ocsessions.ModeInitializer).
			return r.Register(delegationServiceFactory{opts: opts})
		}},
		registrar{"delegation-tool", func(r *resource.Registry) error {
			return r.Register(tooldelegation.NewSourceFactory())
		}},
		registrar{"exec-policy", execpolicy.Register},
		registrar{"hooks", func(r *resource.Registry) error {
			// External lifecycle hooks and the subagent observer, both
			// flowcraft's own kinds. The document declares the plugin host
			// as this runner's extra hook source, so enabled plugins keep
			// contributing their hooks.json files.
			return hooks.Register(r,
				hooks.WithSourceDep("plugin.host", "opencraft.plugins"),
			)
		}},
		// User-level state bindings: each pairs the caller-owned store the
		// host attached to user.db with that feature's document settings,
		// so consumers read one dependency instead of two.
		registrar{"user-memory-store", func(r *resource.Registry) error {
			return r.Register(userstore.Factory{})
		}},
		registrar{"skill-usage-store", func(r *resource.Registry) error {
			return r.Register(skillusage.Factory{})
		}},
		registrar{"review-store", func(r *resource.Registry) error {
			return r.Register(reviewstore.Factory{})
		}},
		registrar{"review", func(r *resource.Registry) error {
			return r.Register(review.Factory{})
		}},
		registrar{"subagent-policy", func(r *resource.Registry) error {
			return r.Register(subagents.PolicyFactory{})
		}},
	}
}

// valuesCapability carries the per-assembly path values (see
// ocraftScheme) as a resolver scheme.
type valuesCapability struct {
	resolver *resource.ReferenceResolver
}

func (valuesCapability) Name() string { return "assembly-values" }

func (c valuesCapability) Resolver() *resource.ReferenceResolver { return c.resolver }

// usageCapability reports every inference usage the runtime produces —
// the model actually invoked included — to the caller's observer, with
// the turn context intact.
type usageCapability struct {
	observer func(context.Context, inference.Usage)
}

func (usageCapability) Name() string { return "usage-observer" }

func (c usageCapability) HostFactoryDecorators() []runtimecore.HostFactoryDecorator {
	if c.observer == nil {
		return nil
	}
	observer := c.observer
	return []runtimecore.HostFactoryDecorator{func(
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
			return hf, nil
		}), nil
	}}
}

// delegationCapability exposes the delegation service on every turn
// host so the delegate / delegation_status tools can resolve it at call
// time.
type delegationCapability struct{}

func (delegationCapability) Name() string { return "delegation-host" }

func (delegationCapability) ResultHostFactoryDecorators() []runtimecore.ResultHostFactoryDecorator {
	return []runtimecore.ResultHostFactoryDecorator{func(
		result *deploy.Result,
		factory sessions.HostFactory,
	) (sessions.HostFactory, error) {
		return delegationhostwrap.Wrap(factory, result)
	}}
}

// assemblyCapabilities is the full capability list of one assembly.
func assemblyCapabilities(o *Options, resolver *resource.ReferenceResolver) []craft.Capability {
	caps := coreCapabilities()
	caps = append(caps, appCapabilities(o)...)
	caps = append(caps,
		valuesCapability{resolver: resolver},
		usageCapability{observer: o.usageObserver},
		delegationCapability{},
	)
	return caps
}

// assemblyExternals are the caller-owned values injected into the
// runtime instead of being built from registry factories. The deploy
// document declares them under runtime.external_deps
// (assets/runtime.yaml); craft keeps these values across in-place
// Runtime.Reload generations, so stable app objects (plugin store/kraft
// runtime, automation manager) never need re-registration on document
// reloads.
func assemblyExternals(o *Options) []deploy.ExternalResource {
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
	return []deploy.ExternalResource{
		{
			External: deploy.External{
				Name:     "plugin.host",
				Contract: pluginagent.ResourceKind,
			},
			Value: pluginHost,
		},
		{
			External: deploy.External{
				Name:     "automation.host",
				Contract: automationtool.ResourceKind,
			},
			Value: automationHost,
		},
		{
			External: deploy.External{
				Name:     "plugin.installer",
				Contract: plugininstalltool.ResourceKind,
			},
			Value: pluginInstaller,
		},
		{
			External: deploy.External{
				Name:     "user.memory",
				Contract: userstore.StoreContract,
			},
			Value: userMemory,
		},
		{
			External: deploy.External{
				Name:     "skill.usage",
				Contract: skillusage.StoreContract,
			},
			Value: skillUsage,
		},
		{
			External: deploy.External{
				Name:     "review.queue",
				Contract: reviewstore.StoreContract,
			},
			Value: reviewQueue,
		},
	}
}

// baseLayers converts the embedded layer list into the definition's
// base layers. The embedded sources resolve against config.FS(), the
// Assets of the Craft below, so only name, priority and the embed path
// travel.
func baseLayers(layers []deploy.Layer) []craft.BaseLayer {
	out := make([]craft.BaseLayer, 0, len(layers))
	for i, layer := range layers {
		name := layer.Name
		if name == "" {
			name = fmt.Sprintf("base-%d", i)
		}
		out = append(out, craft.BaseLayer{
			Name:     name,
			Priority: layer.Priority,
			File:     layer.Source.File,
			Embed:    layer.Source.Embed,
		})
	}
	return out
}

// agentsResourceName is the deploy-document resource id of the
// persistent subagent registry.
const agentsResourceName = "agentlifecycle"

// closeOnFailure tears a Craft down after an assembly step failed: the
// step's error is what the caller reports, so a teardown failure is
// logged rather than merged into it (the error it would hide is the
// one that says why nothing was assembled).
func closeOnFailure(ctx context.Context, c *craft.Craft) {
	if err := c.Close(); err != nil {
		telemetry.WarnErr(ctx, "engine: close craft after failed assembly", err)
	}
}

// BuildAssembly assembles the runtime one Host runs on: a Craft whose
// definition is the embedded base of view, whose overlay is the on-disk
// half of view, and whose one runtime is opened under key.
//
// The caller keeps the view it passed (or a fresh one) for reloads:
// Controller.Reload hands craft a new overlay, and craft recomposes and
// swaps the generation. Assembly for different workspaces may run
// concurrently: everything per-workspace travels with this Craft
// instead of through process state.
func BuildAssembly(
	ctx context.Context,
	view *config.View,
	key string,
	opts ...Option,
) (*Controller, error) {
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
	if view == nil {
		return nil, fmt.Errorf("engine: deployment view is required")
	}
	if len(view.Base) == 0 {
		return nil, fmt.Errorf("engine: deployment view carries no base layers")
	}
	// Inference wiring is generated into the user layer, so nothing
	// declares the router before the setup page runs. Say that plainly
	// instead of letting the graph fail on a missing resource reference.
	configured, err := config.RouterConfigured(view.Document)
	if err != nil {
		return nil, fmt.Errorf("engine: check router configuration: %w", err)
	}
	if !configured {
		return nil, fmt.Errorf(
			"engine: inference is not configured: %s declares no router "+
				"generate target (configure a provider in the settings page)",
			filepath.Join(o.ConfigBase, "opencraft.yaml"))
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
	c, err := craft.New(craft.Definition{
		Craft: craft.Meta{
			ID:      "com.GizClaw.opencraft",
			Name:    "OpenCraft",
			Version: version.ServiceVersion,
		},
		BaseLayers: baseLayers(view.Base),
	}, craft.Options{
		ConfigDir:     o.ConfigBase,
		DataDir:       dataDir,
		AppHome:       o.AppHome,
		DefinitionDir: o.ConfigBase,
		Assets:        config.FS(),
		Loader:        loader,
		Capabilities:  assemblyCapabilities(&o, ocraftResolver(&o, dataDir, cacheDir)),
		Externals:     assemblyExternals(&o),
		Layers:        view.Overlay,
	})
	if err != nil {
		return nil, fmt.Errorf("engine: craft: %w", err)
	}
	if err := c.Start(ctx); err != nil {
		closeOnFailure(ctx, c)
		return nil, fmt.Errorf("engine: craft start: %w", err)
	}
	rt, err := c.OpenRuntime(ctx, craft.RuntimeKey(key), craft.RuntimeOptions{})
	if err != nil {
		closeOnFailure(ctx, c)
		return nil, fmt.Errorf("engine: open runtime %q: %w", key, err)
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
					attribute.String("agent", failure.Name),
					attribute.String("error", failure.Err.Error()))
			}
		}
	}
	return &Controller{craft: c, key: craft.RuntimeKey(key)}, nil
}
