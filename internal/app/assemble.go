// Package app assembles opencraft's runtime from a deploy document:
// embedded deploy assets, user-facing config seeding, factory
// registration (graph engine, state, memory, workspace, tools,
// inference, event), platform sandbox selection, and lifecycle hooks.
package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"

	sdkconfig "github.com/GizClaw/flowcraft/sdk/config"
	"github.com/GizClaw/flowcraft/sdk/errdefs"
	eventconfig "github.com/GizClaw/flowcraft/sdk/event/config"
	graphconfig "github.com/GizClaw/flowcraft/sdk/graph/config"
	inferenceconfig "github.com/GizClaw/flowcraft/sdk/inference/config"
	envresolver "github.com/GizClaw/flowcraft/sdk/inference/config/env"
	"github.com/GizClaw/flowcraft/sdk/sandbox"
	"github.com/GizClaw/flowcraft/sdk/tool"
	toolconfig "github.com/GizClaw/flowcraft/sdk/tool/config"
	"github.com/GizClaw/flowcraft/sdk/workspace"
	workspaceconfig "github.com/GizClaw/flowcraft/sdk/workspace/config"
	jsrt "github.com/GizClaw/flowcraft/sdkx/agent/script/jsrt"
	sdkdeploy "github.com/GizClaw/flowcraft/sdkx/deploy"
	"github.com/GizClaw/flowcraft/sdkx/inference/deepseek"
	"github.com/GizClaw/flowcraft/sdkx/inference/openai"
	runtimecore "github.com/GizClaw/flowcraft/sdkx/runtime"
	"github.com/GizClaw/flowcraft/sdkx/sandbox/bwrap"
	"github.com/GizClaw/flowcraft/sdkx/sandbox/seatbelt"
	"github.com/GizClaw/flowcraft/sdkx/tool/dynamic"
	exectool "github.com/GizClaw/flowcraft/sdkx/tool/exec"
	"github.com/GizClaw/flowcraft/sdkx/tool/mcp"

	"github.com/GizClaw/opencraft/internal/app/worldstate"
	"github.com/GizClaw/opencraft/internal/config"
	"github.com/GizClaw/opencraft/internal/memory"
	"github.com/GizClaw/opencraft/internal/state"
	"github.com/GizClaw/opencraft/internal/tools/applypatch"
	"github.com/GizClaw/opencraft/internal/tools/webfetch"
)

// Options controls assembly paths.
type Options struct {
	// ConfigBase anchors file references in user override documents.
	// Defaults to ~/.opencraft/config.
	ConfigBase string
	// WorkBase is the sandbox/workspace root. Defaults to the current
	// working directory (where opencraft was invoked).
	WorkBase string
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

// BuildRuntime assembles an opencraft runtime from a deploy document.
func BuildRuntime(ctx context.Context, doc sdkdeploy.Document, opts ...Option) (*runtimecore.Runtime, error) {
	o := Options{}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	if o.ConfigBase == "" {
		o.ConfigBase, _ = UserConfigDir()
	}
	if o.WorkBase == "" {
		o.WorkBase, _ = os.Getwd()
	}
	dataDir, err := UserDataDir()
	if err != nil {
		return nil, err
	}

	loader := sdkconfig.NewLoader(
		sdkconfig.WithBaseDir(o.ConfigBase),
		sdkconfig.WithEmbed(config.FS()),
	)

	builder := sdkdeploy.NewBuilder(sdkdeploy.WithLoader(loader))
	builder.MustRegisterEngine(graphconfig.NewFactory(graphconfig.WithLoader(loader)))
	builder.MustRegisterResource(eventconfig.NewMemoryDeployFactory())
	builder.MustRegisterResource(jsrt.NewDeployFactory())
	builder.MustRegisterResource(state.Factory{DefaultPath: filepath.Join(dataDir, "opencraft.db")})
	builder.MustRegisterResource(memory.Factory{})

	ws, err := workspace.NewLocalWorkspace(o.WorkBase)
	if err != nil {
		return nil, err
	}
	builder.MustRegisterResource(workspaceconfig.NewBuilder(
		workspaceconfig.Deps{BaseDir: o.WorkBase}))
	builder.MustRegisterResource(localSandboxFactory{})

	world := worldstate.New(worldstate.Options{
		WorkBase:          o.WorkBase,
		UserDir:           dataDir,
		Workspace:         ws,
		CollaborationMode: "default",
		PermissionProfile: "workspace",
	})
	builder.RegisterPreparer("opencraft.prepare", prepareHookFactory{world: world})
	builder.RegisterCommitter("opencraft.commit", commitHookFactory{})

	toolBuilder := toolconfig.NewBuilder(toolconfig.Deps{})
	toolRunner := platformRunner(o.WorkBase)
	toolBuilder.RegisterBuiltin(exectool.MustNew(toolRunner))
	if pm := sandbox.ProcessManagerOf(toolRunner); pm != nil {
		toolBuilder.RegisterBuiltin(exectool.MustNewSession(pm))
	}
	toolBuilder.RegisterBuiltin(applypatch.MustNew(ws))
	toolBuilder.RegisterBuiltin(webfetch.New())
	toolBuilder.RegisterFactory("record_calls",
		func(context.Context, sdkconfig.Input) (tool.Middleware, error) {
			return dynamic.RecordCalls(), nil
		})
	toolBuilder.RegisterSourceFactory(mcp.SpecKind, mcp.SourceFactory)
	builder.MustRegisterResource(toolBuilder)

	factories := map[string]inferenceconfig.Factory{
		"openai":   openai.Factory(),
		"deepseek": deepseek.Factory(),
	}
	resolvers := map[string]inferenceconfig.SecretResolver{
		"env": envresolver.New(),
	}
	builder.MustRegisterResource(inferenceconfig.NewDeployFactory(factories, resolvers))

	runtimeBuilder := runtimecore.NewBuilder(builder)
	return runtimeBuilder.Build(ctx, doc)
}

// platformRunner picks the sandbox backend for the current platform and
// falls back to the local runner when a platform backend is unavailable.
func platformRunner(workBase string) sandbox.Runner {
	switch runtime.GOOS {
	case "darwin":
		if r, err := seatbelt.New(workBase); err == nil {
			return r
		}
	case "linux":
		if r, err := bwrap.New(workBase); err == nil {
			return r
		}
	}
	return sandbox.NewLocalRunner(workBase)
}

// localSandboxFactory exposes the platform-selected runner as a deploy
// resource of kind sandbox.Runner. It depends on the workspace resource
// and builds the runner from the workspace root (root assertion via the
// optional Root() accessor), so sandbox and workspace always share a
// root.
type localSandboxFactory struct{}

var _ sdkconfig.Factory = localSandboxFactory{}

func (f localSandboxFactory) Spec() sdkconfig.Spec {
	return sdkconfig.Spec{
		Kind: "sandbox.Runner",
		Impl: "auto",
		Deps: []sdkconfig.DepSpec{
			{Name: "workspace", Type: "workspace.Workspace", Required: true},
		},
	}
}

func (f localSandboxFactory) New(_ context.Context, in sdkconfig.Input) (any, error) {
	dep, ok := in.Dep("workspace")
	if !ok {
		return nil, errdefs.Validationf("sandbox: workspace dependency is required")
	}
	rooter, ok := dep.(interface{ Root() string })
	if !ok || rooter.Root() == "" {
		return nil, errdefs.Validationf(
			"sandbox: workspace dependency must expose a root")
	}
	return platformRunner(rooter.Root()), nil
}
