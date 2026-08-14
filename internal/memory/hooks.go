package memory

import (
	"context"

	"github.com/GizClaw/flowcraft/core/agent"
	corememory "github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/sessions"
	"github.com/GizClaw/opencraft/internal/utils/resourcedep"
)

// commitHookFactory builds the opencraft.commit hook: it commits the
// turn's new messages to the memory TurnSink (persist + fold).
type commitHookFactory struct{}

var _ resource.Factory = commitHookFactory{}

func (commitHookFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "hook.commit",
		Impl: "opencraft.commit",
		Deps: []resource.DepSpec{
			{Name: "memory", Type: ResourceKind, Required: true},
			{Name: "sessions", Type: sessions.ResourceKind, Required: true},
		},
	}
}

type commitSettings struct {
	RuntimeID string `json:"runtime_id,omitempty"`
	UserID    string `json:"user_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
}

func (commitHookFactory) New(_ context.Context, in resource.Input) (any, error) {
	sink, err := resourcedep.Required[corememory.TurnSink](in, "memory", "memory")
	if err != nil {
		return nil, err
	}
	store, err := resourcedep.Required[*sessions.Store](in, "memory", "sessions")
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
		// Result.Messages excludes the input request; prepend it so the
		// conversation archive has both sides.
		history := append(
			[]message.Message{req.Message}, res.Messages...)
		// Full text/reasoning history goes to the project session
		// store; memory summarization stays in the state DB.
		if err := store.AppendTurn(ctx, req.ContextID, history); err != nil {
			return err
		}
		return sink.CommitTurn(ctx, corememory.Turn{
			Scope:          settings.scopeFor(id),
			ConversationID: req.ContextID,
			IdempotencyKey: res.RunID,
			Messages:       res.Messages,
		})
	}), nil
}

func (s commitSettings) scopeFor(id agent.Identity) corememory.Scope {
	scope := corememory.Scope{UserID: s.UserID, AgentID: s.AgentID}
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
