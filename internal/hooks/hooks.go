// Package hooks registers opencraft's agent lifecycle hooks as deploy
// hook resources. The prepare hook builds the world state from its
// workspace dep and settings; the commit hook persists each turn
// through the memory dep.
package hooks

import (
	"context"
	"errors"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/workspace"

	"github.com/GizClaw/opencraft/internal/app/worldstate"
	opmemory "github.com/GizClaw/opencraft/internal/memory"
)

// Register adds the opencraft.prepare and opencraft.commit hook
// factories to r.
func Register(r *resource.Registry) error {
	return errors.Join(
		r.Register(prepareFactory{}),
		r.Register(commitFactory{}),
	)
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
	mem, err := requiredDep[memory.ContextProvider](in, "memory")
	if err != nil {
		return nil, err
	}
	ws, err := requiredDep[workspace.Workspace](in, "workspace")
	if err != nil {
		return nil, err
	}
	settings, err := resource.DecodeTyped[prepareSettings](
		in.Settings, resource.ExpandEnv())
	if err != nil {
		return nil, err
	}
	world := worldstate.New(worldstate.Options{
		WorkBase:          settings.WorkDir,
		UserDir:           settings.UserDir,
		CollaborationMode: settings.CollaborationMode,
		PermissionProfile: settings.PermissionProfile,
		Workspace:         ws,
	})
	world.SetMemory(mem)
	return agent.PreparerFunc(func(
		ctx context.Context, _ agent.Identity, req *agent.Request, prev *agent.Board,
	) (*agent.Board, error) {
		board := prev
		if board == nil {
			board = agent.NewBoard()
		}
		if err := world.RenderToBoard(ctx, req.ContextID, board); err != nil {
			return board, err
		}
		return board, nil
	}), nil
}

// commitFactory builds the opencraft.commit hook: it commits the
// turn's new messages to the memory TurnSink (persist + fold).
type commitFactory struct{}

var _ resource.Factory = commitFactory{}

func (commitFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "hook.commit",
		Impl: "opencraft.commit",
		Deps: []resource.DepSpec{
			{Name: "memory", Type: opmemory.ResourceKind, Required: true},
		},
	}
}

type commitSettings struct {
	RuntimeID string `json:"runtime_id,omitempty"`
	UserID    string `json:"user_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
}

func (commitFactory) New(_ context.Context, in resource.Input) (any, error) {
	sink, err := requiredDep[memory.TurnSink](in, "memory")
	if err != nil {
		return nil, err
	}
	settings, err := resource.DecodeTyped[commitSettings](in.Settings)
	if err != nil {
		return nil, err
	}
	return agent.CommitterFunc(func(
		ctx context.Context, id agent.Identity, req *agent.Request, res *agent.Result,
	) error {
		if len(res.Messages) == 0 {
			return nil
		}
		return sink.CommitTurn(ctx, memory.Turn{
			Scope:          settings.scopeFor(id),
			ConversationID: req.ContextID,
			IdempotencyKey: res.RunID,
			Messages:       res.Messages,
		})
	}), nil
}

func (s commitSettings) scopeFor(id agent.Identity) memory.Scope {
	scope := memory.Scope{UserID: s.UserID, AgentID: s.AgentID}
	if s.RuntimeID != "" {
		scope.RuntimeID = s.RuntimeID
	} else {
		scope.RuntimeID = "opencraft"
	}
	if scope.AgentID == "" {
		scope.AgentID = id.AgentID
	}
	return scope
}

func requiredDep[T any](in resource.Input, name string) (T, error) {
	var zero T
	dep, ok := in.Dep(name)
	if !ok {
		return zero, errdefs.Validationf(
			"hook: dep %q is required", name)
	}
	value, ok := dep.(T)
	if !ok {
		return zero, errdefs.Validationf(
			"hook: dep %q is %T, want %T", name, dep, zero)
	}
	return value, nil
}
