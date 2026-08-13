package worldstate

import (
	"context"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/workspace"

	opmemory "github.com/GizClaw/opencraft/internal/memory"
	"github.com/GizClaw/opencraft/internal/utils/resourcedep"
)

// Register adds the opencraft.prepare hook factory (the worldstate
// injection hook) to r.
func Register(r *resource.Registry) error {
	return r.Register(prepareFactory{})
}

// prepareFactory builds the opencraft.prepare hook: it gathers the
// world state (AGENTS.md, permissions, environment, memory summary)
// into board vars; the graph's world script node renders the final
// model-facing message list.
type prepareFactory struct{}

var _ resource.Factory = prepareFactory{}

func (prepareFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "hook.prepare",
		Impl: "opencraft.prepare",
		Deps: []resource.DepSpec{
			{Name: "memory", Type: opmemory.ResourceKind, Required: true},
			{Name: "workspace", Type: "workspace.Workspace", Required: true},
		},
	}
}

type prepareSettings struct {
	WorkDir           string `json:"work_dir"`
	UserDir           string `json:"user_dir"`
	CollaborationMode string `json:"collaboration_mode,omitempty"`
	PermissionProfile string `json:"permission_profile,omitempty"`
}

func (prepareFactory) New(_ context.Context, in resource.Input) (any, error) {
	mem, err := resourcedep.Required[memory.ContextProvider](in, "worldstate", "memory")
	if err != nil {
		return nil, err
	}
	ws, err := resourcedep.Required[workspace.Workspace](in, "worldstate", "workspace")
	if err != nil {
		return nil, err
	}
	settings, err := resource.DecodeTyped[prepareSettings](
		in.Settings, resource.ExpandEnv())
	if err != nil {
		return nil, err
	}
	service := New(Options{
		WorkBase:          settings.WorkDir,
		UserDir:           settings.UserDir,
		CollaborationMode: settings.CollaborationMode,
		PermissionProfile: settings.PermissionProfile,
		Workspace:         ws,
	})
	service.SetMemory(mem)
	return agent.PreparerFunc(func(
		ctx context.Context, _ agent.Identity, req *agent.Request, prev *agent.Board,
	) (*agent.Board, error) {
		board := prev
		if board == nil {
			board = agent.NewBoard()
		}
		if err := service.RenderToBoard(ctx, req.ContextID, board); err != nil {
			return board, err
		}
		return board, nil
	}), nil
}
