package app

import (
	"context"
	"fmt"

	"github.com/GizClaw/flowcraft/sdk/sandbox"
	sandboxconfig "github.com/GizClaw/flowcraft/sdk/sandbox/config"
	workspaceconfig "github.com/GizClaw/flowcraft/sdk/workspace/config"
	"github.com/GizClaw/flowcraft/sdkx/sandbox/bwrap"
	"github.com/GizClaw/flowcraft/sdkx/sandbox/seatbelt"

	"github.com/GizClaw/opencraft/internal/config"
)

// SandboxProcessManager builds the sandbox.Registry from the merged
// configuration for workDir and returns the ProcessManager of the
// "main" sandbox. The execd subprocess uses it to construct its
// backend from the same configuration the main process uses.
func SandboxProcessManager(
	ctx context.Context,
	workDir string,
) (sandbox.ProcessManager, error) {
	mgr, err := config.Open(config.Options{WorkDir: workDir})
	if err != nil {
		return nil, err
	}
	view, err := mgr.Load(ctx)
	if err != nil {
		return nil, err
	}
	workspaces, err := workspaceconfig.NewBuilder(
		workspaceconfig.Deps{BaseDir: workDir}).Build(ctx, *view.Workspace)
	if err != nil {
		return nil, fmt.Errorf("execd workspace: %w", err)
	}
	sandboxBuilder := sandboxconfig.NewBuilder(
		sandboxconfig.Deps{Workspaces: workspaces})
	if err := seatbelt.Register(sandboxBuilder); err != nil {
		return nil, err
	}
	if err := bwrap.Register(sandboxBuilder); err != nil {
		return nil, err
	}
	registry, err := sandboxBuilder.Build(ctx, *view.Sandbox)
	if err != nil {
		return nil, fmt.Errorf("execd sandbox: %w", err)
	}
	runner, ok := registry.Get("main")
	if !ok {
		return nil, fmt.Errorf("execd: sandbox %q not found", "main")
	}
	pm := sandbox.ProcessManagerOf(runner)
	if pm == nil {
		return nil, fmt.Errorf(
			"execd: sandbox backend has no process manager")
	}
	return pm, nil
}
