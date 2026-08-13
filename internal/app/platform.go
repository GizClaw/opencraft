package app

import (
	"context"
	goruntime "runtime"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/sandbox"
	"github.com/GizClaw/flowcraft/core/sandbox/bwrap"
	sandboxlocal "github.com/GizClaw/flowcraft/core/sandbox/local"
	"github.com/GizClaw/flowcraft/core/sandbox/seatbelt"
	"github.com/GizClaw/flowcraft/core/workspace"

	"github.com/GizClaw/opencraft/internal/execd"
)

// sandboxFactory implements the sandbox.Runner resource with the
// platform backend (seatbelt on macOS, bwrap on Linux, local
// elsewhere) and env-expanded settings.
type sandboxFactory struct{}

var _ resource.Factory = sandboxFactory{}

func (sandboxFactory) Spec() resource.Spec {
	return resource.Spec{Kind: "sandbox.Runner", Impl: "opencraft"}
}

type sandboxSettings struct {
	Root          string   `json:"root"`
	WritablePaths []string `json:"writable_paths,omitempty"`
	Remote        bool     `json:"remote,omitempty"`
}

func (sandboxFactory) New(ctx context.Context, in resource.Input) (any, error) {
	s, err := resource.DecodeTyped[sandboxSettings](
		in.Settings, resource.ExpandEnv())
	if err != nil {
		return nil, errdefs.Validationf(
			"opencraft sandbox: decode settings: %v", err)
	}
	if s.Root == "" {
		return nil, errdefs.Validationf(
			"opencraft sandbox: settings.root is required")
	}
	if s.Remote {
		client, stop, err := execd.Launch(ctx, s.Root)
		if err != nil {
			return nil, err
		}
		return execd.NewRemoteRunner(client, stop)
	}
	switch goruntime.GOOS {
	case "darwin":
		return seatbelt.New(s.Root, seatbelt.WithWritablePaths(s.WritablePaths...))
	case "linux":
		return bwrap.New(s.Root, bwrap.WithWritablePaths(s.WritablePaths...))
	default:
		return sandboxlocal.New(s.Root), nil
	}
}

// workspaceFactory implements the workspace.Workspace resource as a
// local workspace with env-expanded root.
type workspaceFactory struct{}

var _ resource.Factory = workspaceFactory{}

func (workspaceFactory) Spec() resource.Spec {
	return resource.Spec{Kind: "workspace.Workspace", Impl: "opencraft"}
}

type workspaceSettings struct {
	Root string `json:"root"`
}

func (workspaceFactory) New(_ context.Context, in resource.Input) (any, error) {
	s, err := resource.DecodeTyped[workspaceSettings](
		in.Settings, resource.ExpandEnv())
	if err != nil {
		return nil, err
	}
	if s.Root == "" {
		return nil, errdefs.Validationf(
			"opencraft workspace: settings.root is required")
	}
	return workspace.NewLocalWorkspace(s.Root)
}

var _ workspace.Workspace = (*workspace.LocalWorkspace)(nil)
var _ sandbox.Runner = (*sandboxlocal.Runner)(nil)
