// Package app assembles opencraft's runtime from a deploy document:
// embedded deploy assets, user-facing config, factory registration,
// platform sandbox selection, and lifecycle hooks.
package app

import (
	"context"
	"os"
	"path/filepath"

	"github.com/GizClaw/flowcraft/backends/checkpoint/sqlite"
	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/agent/scriptrt"
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

	"github.com/GizClaw/opencraft/internal/app/worldstate"
	"github.com/GizClaw/opencraft/internal/config"
	opmemory "github.com/GizClaw/opencraft/internal/memory"
	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
	"github.com/GizClaw/opencraft/internal/state"
	opentools "github.com/GizClaw/opencraft/internal/tools"
	"github.com/GizClaw/opencraft/internal/tools/plan"
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
	// the model actually invoked). Nil disables observation.
	usageObserver func(inference.Usage)
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
func WithUsageObserver(fn func(inference.Usage)) Option {
	return func(o *Options) { o.usageObserver = fn }
}

// planStoreResource exposes the runtime-scoped plan store as a deploy
// resource so the worldstate prepare hook can inject the latest plan.
type planStoreResource struct {
	store *plan.Store
}

var _ resource.Factory = planStoreResource{}

func (planStoreResource) Spec() resource.Spec {
	return resource.Spec{Kind: "opencraft.planstore", Impl: "holder"}
}

func (f planStoreResource) New(
	_ context.Context,
	_ resource.Input,
) (any, error) {
	return f.store, nil
}

// BuildRuntime assembles an opencraft runtime from a deploy document.
func BuildRuntime(ctx context.Context, doc deploy.Document, opts ...Option) (*runtimecore.Runtime, error) {
	o := Options{}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	if o.ConfigBase == "" {
		o.ConfigBase, _ = config.UserConfigDir()
	}
	if o.WorkBase == "" {
		o.WorkBase, _ = os.Getwd()
	}
	dataDir, err := config.UserDataDir()
	if err != nil {
		return nil, err
	}
	cacheDir := filepath.Join(dataDir, "cache")
	for _, sub := range []string{"go", "tmp"} {
		if err := os.MkdirAll(filepath.Join(cacheDir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	// Project conversation state (memory DB + checkpoints) lives under
	// <project>/.opencraft/sessions; ensure the directory exists before
	// any resource opens a database there.
	sessionDir := filepath.Join(o.WorkBase, ".opencraft", "sessions")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return nil, err
	}

	// Scalar settings in the deploy document reference runtime paths
	// through ${env:...}; publish them before resources are built.
	_ = os.Setenv("OPEN_CRAFT_WORKDIR", o.WorkBase)
	_ = os.Setenv("OPEN_CRAFT_CACHE", cacheDir)
	_ = os.Setenv("OPEN_CRAFT_DATA_DIR", dataDir)

	// Runtime-scoped shared state for tools: the exec policy manager
	// (filled by the sandbox factory during build, exposed on every
	// turn host) and the plan store.
	policy := &policyHolder{}
	planStore := plan.NewStore(
		filepath.Join(o.WorkBase, ".opencraft", "plans.json"))

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
		inference.Register,
		route.Register,
		scriptrt.Register,
		sandboxlocal.Register,
		bwrap.Register,
		seatbelt.Register,
		sqlite.Register,
		anthropic.Register,
		azure.Register,
		bytedance.Register,
		deepseek.Register,
		kimi.Register,
		minimax.Register,
		openai.Register,
		qwen.Register,
		opmemory.Register,
		ocsessions.Register,
		opentools.Register,
		worldstate.Register,
	}
	for _, register := range registers {
		if err := register(reg); err != nil {
			return nil, err
		}
	}
	reg.MustRegister(sandboxFactory{holder: policy})
	reg.MustRegister(execPolicyResource{holder: policy})
	reg.MustRegister(planStoreResource{store: planStore})
	reg.MustRegister(opentools.NewPlanSourceFactory(planStore))
	reg.MustRegister(state.Factory{
		DefaultPath: filepath.Join(sessionDir, "session.db"),
	})

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
			// Expose the exec policy (built by the sandbox factory)
			// so request_permissions can grant command rules.
			if p := policy.get(); p != nil {
				host = WithExecPolicy(host, p)
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
					observer(usage)
					return nil
				}
			}
			return hf, nil
		}), nil
	}); err != nil {
		return nil, err
	}
	return builder.Build(ctx, doc)
}
