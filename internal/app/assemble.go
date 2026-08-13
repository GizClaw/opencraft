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
	"github.com/GizClaw/flowcraft/core/deploy"
	"github.com/GizClaw/flowcraft/core/event"
	graphresource "github.com/GizClaw/flowcraft/core/graph/resource"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/resource"
	runtimecore "github.com/GizClaw/flowcraft/core/runtime"
	"github.com/GizClaw/flowcraft/core/sandbox/bwrap"
	sandboxlocal "github.com/GizClaw/flowcraft/core/sandbox/local"
	"github.com/GizClaw/flowcraft/core/sandbox/seatbelt"
	"github.com/GizClaw/flowcraft/core/agent/scriptrt"
	"github.com/GizClaw/flowcraft/core/tool"
	"github.com/GizClaw/flowcraft/core/tool/middleware"
	"github.com/GizClaw/flowcraft/core/workspace"
	"github.com/GizClaw/flowcraft/driver/deepseek"
	"github.com/GizClaw/flowcraft/driver/openai"
	sessions "github.com/GizClaw/flowcraft/core/runtime/session"

	"github.com/GizClaw/opencraft/internal/config"
	"github.com/GizClaw/opencraft/internal/hooks"
	opmemory "github.com/GizClaw/opencraft/internal/memory"
	"github.com/GizClaw/opencraft/internal/state"
	opentools "github.com/GizClaw/opencraft/internal/tools"
)

// Options controls assembly paths.
type Options struct {
	// ConfigBase anchors the user configuration directory. Defaults to
	// ~/.opencraft/config.
	ConfigBase string
	// WorkBase is the sandbox/workspace root. Defaults to the current
	// working directory (where opencraft was invoked).
	WorkBase string
	// userPrompter routes model questions (ask_user) to a user-facing
	// surface such as the TUI. Nil keeps the runtime's default.
	userPrompter agent.UserPrompter
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

// WithUserPrompter injects the user-prompt surface used by ask_user.
func WithUserPrompter(p agent.UserPrompter) Option {
	return func(o *Options) { o.userPrompter = p }
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

	// Scalar settings in the deploy document reference runtime paths
	// through ${env:...}; publish them before resources are built.
	_ = os.Setenv("OPEN_CRAFT_WORKDIR", o.WorkBase)
	_ = os.Setenv("OPEN_CRAFT_CACHE", cacheDir)
	_ = os.Setenv("OPEN_CRAFT_DATA_DIR", dataDir)

	loader := resource.NewLoader(
		resource.WithBaseDir(o.ConfigBase),
		resource.WithEmbed(config.FS()),
	)
	reg := resource.NewRegistry()
	if err := event.Register(reg); err != nil {
		return nil, err
	}
	if err := graphresource.Register(reg); err != nil {
		return nil, err
	}
	if err := workspace.Register(reg); err != nil {
		return nil, err
	}
	reg.MustRegister(workspaceFactory{})
	reg.MustRegister(sandboxFactory{})
	if err := tool.Register(reg); err != nil {
		return nil, err
	}
	if err := middleware.Register(reg); err != nil {
		return nil, err
	}
	if err := inference.Register(reg); err != nil {
		return nil, err
	}
	if err := scriptrt.Register(reg); err != nil {
		return nil, err
	}
	if err := sandboxlocal.Register(reg); err != nil {
		return nil, err
	}
	if err := bwrap.Register(reg); err != nil {
		return nil, err
	}
	if err := seatbelt.Register(reg); err != nil {
		return nil, err
	}
	if err := sqlite.Register(reg); err != nil {
		return nil, err
	}
	if err := deepseek.Register(reg); err != nil {
		return nil, err
	}
	if err := openai.Register(reg); err != nil {
		return nil, err
	}

	reg.MustRegister(state.Factory{
		DefaultPath: filepath.Join(dataDir, "opencraft.db"),
	})
	reg.MustRegister(opmemory.Factory{})
	if err := opentools.Register(reg); err != nil {
		return nil, err
	}

	if err := hooks.Register(reg); err != nil {
		return nil, err
	}

	builder := runtimecore.NewBuilder(reg)
	if err := builder.WithLoader(loader); err != nil {
		return nil, err
	}
	if o.userPrompter != nil {
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
				return agent.HostFuncs{
					Inner:     host,
					AskUserFn: o.userPrompter.AskUser,
				}, nil
			}), nil
		}); err != nil {
			return nil, err
		}
	}
	return builder.Build(ctx, doc)
}
