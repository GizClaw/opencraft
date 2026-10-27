package worldstate

import (
	"context"
	"errors"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/workspace"

	opmemory "github.com/GizClaw/opencraft/internal/capabilities/memory"
	"github.com/GizClaw/opencraft/internal/capabilities/memory/userstore"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/skills"
)

// Register adds this package's factories to r: the opencraft.prepare
// hook (the worldstate injection hook), its per-session companions, and
// the script surface carrying the "ocworld" global.
func Register(r *resource.Registry) error {
	return errors.Join(
		r.Register(prepareFactory{}),
		r.Register(activateObserverFactory{}),
		r.Register(usageAnchorCommitFactory{}),
		r.Register(scriptBindingsFactory{}),
	)
}

// prepareFactory builds the opencraft.prepare hook: it gathers the
// world state (AGENTS.md, permissions, environment, memory summary)
// into board vars; the graph's world node merges them into the final
// model-facing message list (see Merge).
type prepareFactory struct{}

var _ resource.Factory[agent.PreparerFunc] = prepareFactory{}

var (
	depPrepareMemory     = resource.Require[memory.ContextProvider]("memory")
	depPrepareWorkspace  = resource.Require[workspace.Workspace]("workspace")
	depPrepareSkills     = resource.Optional[*skills.Service]("skills")
	depPrepareExecPolicy = resource.Optional[PrefixProvider]("execpolicy")
	depPrepareSessions   = resource.Optional[*ocsessions.Store]("sessions")
	depPrepareUserMemory = resource.Optional[*userstore.Binding]("usermemory")
)

func (prepareFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "hook.prepare",
		Impl: "opencraft.prepare",
		Deps: []resource.Requirement{
			depPrepareMemory.Spec(), depPrepareWorkspace.Spec(),
			// Optional: without them the sections they contribute (the
			// approved-prefix line, the yolo marker, the skills block,
			// the user-memory block) are simply absent.
			depPrepareExecPolicy.Spec(), depPrepareSessions.Spec(),
			depPrepareSkills.Spec(), depPrepareUserMemory.Spec(),
		},
	}
}

type prepareSettings struct {
	WorkDir           string `json:"work_dir"`
	UserDir           string `json:"user_dir"`
	CollaborationMode string `json:"collaboration_mode,omitempty"`
	Personality       string `json:"personality,omitempty"`
	PermissionProfile string `json:"permission_profile,omitempty"`
	MemoryMaxItems    int    `json:"memory_max_items,omitempty"`
	MemoryMaxChars    int    `json:"memory_max_chars,omitempty"`
}

func (prepareFactory) New(ctx context.Context, in resource.Input) (agent.PreparerFunc, error) {
	mem, err := depPrepareMemory.Get(in)
	if err != nil {
		return nil, err
	}
	ws, err := depPrepareWorkspace.Get(in)
	if err != nil {
		return nil, err
	}
	settings, err := resource.DecodeTyped[prepareSettings](
		ctx, in.Settings)
	if err != nil {
		return nil, err
	}
	service := New(Options{
		WorkBase:          settings.WorkDir,
		UserDir:           settings.UserDir,
		CollaborationMode: settings.CollaborationMode,
		Personality:       settings.Personality,
		PermissionProfile: settings.PermissionProfile,
		MemoryMaxItems:    settings.MemoryMaxItems,
		MemoryMaxChars:    settings.MemoryMaxChars,
		Workspace:         ws,
	})
	if svc, ok, err := depPrepareSkills.TryGet(in); err != nil {
		return nil, err
	} else if ok {
			service.SetSkills(svc)
		}
	}
	service.SetMemory(mem)
	if prefixes, ok, err := depPrepareExecPolicy.TryGet(in); err != nil {
		return nil, err
	} else if ok {
			service.SetPrefixProvider(prefixes)
		}
	}
	if st, ok, err := depPrepareSessions.TryGet(in); err != nil {
		return nil, err
	} else if ok {
			service.SetSessions(st)
		}
	}
	if binding, ok, err := depPrepareUserMemory.TryGet(in); err != nil {
		return nil, err
	} else if ok {
			service.SetUserMemory(binding)
		}
	}
	return agent.PreparerFunc(func(
		ctx context.Context, identity agent.Identity, req *agent.Request, prev *agent.Board,
	) (*agent.Board, error) {
		board := prev
		if board == nil {
			board = agent.NewBoard()
		}
		seedClientMetadata(board, identity)
		// The conversation is the request's context id (subagent turns
		// carry their own "ctx-..." id); the run id rides along so the
		// skill usage events of this turn can be traced back to it.
		if err := service.RenderTurn(
			ctx,
			agent.Identity{
				AgentID:        identity.AgentID,
				RunID:          identity.RunID,
				ConversationID: req.ContextID,
			},
			req.Message.Content.Text(),
			nil,
			board,
		); err != nil {
			return board, err
		}
		return board, nil
	}), nil
}
