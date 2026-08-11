package app

import (
	"context"

	"github.com/GizClaw/flowcraft/sdk/agent"
	sdkconfig "github.com/GizClaw/flowcraft/sdk/config"
	"github.com/GizClaw/flowcraft/sdk/memory"
	sdkdeploy "github.com/GizClaw/flowcraft/sdkx/deploy"

	"github.com/GizClaw/opencraft/internal/app/worldstate"
	opmemory "github.com/GizClaw/opencraft/internal/memory"
)

type hookSettings struct {
	RuntimeID string `json:"runtime_id,omitempty"`
	UserID    string `json:"user_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
}

func (s hookSettings) scope() memory.Scope {
	scope := memory.Scope{UserID: s.UserID, AgentID: s.AgentID}
	if s.RuntimeID != "" {
		scope.RuntimeID = s.RuntimeID
	} else {
		scope.RuntimeID = "opencraft"
	}
	return scope
}

func (s hookSettings) scopeFor(id agent.Identity) memory.Scope {
	scope := s.scope()
	if scope.AgentID == "" {
		scope.AgentID = id.AgentID
	}
	return scope
}

// prepareHookFactory builds the opencraft.prepare hook: it gathers the
// world state (AGENTS.md, permissions, environment, memory summary)
// into board vars; the graph's world script node renders the final
// model-facing message list.
type prepareHookFactory struct {
	world *worldstate.Service
}

var _ sdkconfig.Factory = prepareHookFactory{}

func (prepareHookFactory) Spec() sdkconfig.Spec {
	return sdkconfig.Spec{
		Kind: sdkdeploy.HookKindPreparer,
		Impl: "opencraft.prepare",
		Deps: []sdkconfig.DepSpec{
			{Name: "memory", Type: opmemory.ResourceKind},
		},
	}
}

func (f prepareHookFactory) New(ctx context.Context, in sdkconfig.Input) (any, error) {
	dep, _ := in.Dep("memory")
	mem, _ := dep.(memory.ContextProvider)
	return agent.PreparerFunc(func(
		ctx context.Context, id agent.Identity, req *agent.Request, prev *agent.Board,
	) (*agent.Board, error) {
		board := prev
		if board == nil {
			board = agent.NewBoard()
		}
		if f.world != nil {
			f.world.SetMemory(mem)
			if err := f.world.RenderToBoard(ctx, req.ContextID, board); err != nil {
				return board, err
			}
		}
		return board, nil
	}), nil
}

// commitHookFactory builds the opencraft.commit hook: it commits the
// turn's new messages to the memory TurnSink (persist + fold).
type commitHookFactory struct{}

var _ sdkconfig.Factory = commitHookFactory{}

func (commitHookFactory) Spec() sdkconfig.Spec {
	return sdkconfig.Spec{
		Kind: sdkdeploy.HookKindCommitter,
		Impl: "opencraft.commit",
		Deps: []sdkconfig.DepSpec{
			{Name: "memory", Type: opmemory.ResourceKind},
		},
	}
}

func (commitHookFactory) New(ctx context.Context, in sdkconfig.Input) (any, error) {
	dep, _ := in.Dep("memory")
	sink, _ := dep.(memory.TurnSink)
	settings, err := sdkconfig.DecodeSettings[hookSettings](in.Settings)
	if err != nil {
		return nil, err
	}
	return agent.CommitterFunc(func(
		ctx context.Context, id agent.Identity, req *agent.Request, res *agent.Result,
	) error {
		if sink == nil || len(res.Messages) == 0 {
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
