package worldstate

import (
	"context"
	"errors"
	"os"
	"sync"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/skills"
	"github.com/GizClaw/opencraft/internal/foundation/utils/resourcedep"
)

// activationsStateKey is the session-store key for model-requested
// skill activations, keyed by agent id (mirrors plans.json).
const activationsStateKey = "skill_activations"

// maxModelActivations caps how many model-requested skills a single
// turn may inject, preventing a mention-heavy reply from blowing up
// the next turn's context.
const maxModelActivations = 3

// activateObserverFactory builds the opencraft.skillactivate observe
// hook: it scans each completed turn's assistant output for $name
// mentions and persists them per session. The worldstate prepare hook
// consumes and clears them on the next turn, so the model can request
// a skill by mentioning it (codex-style implicit invocation).
type activateObserverFactory struct{}

var _ resource.Factory = activateObserverFactory{}

func (activateObserverFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "hook.observe",
		Impl: "opencraft.skillactivate",
		Deps: []resource.DepSpec{
			{Name: "skills", Type: skills.ResourceKind, Required: true},
			{Name: "sessions", Type: sessions.ResourceKind, Required: true},
		},
	}
}

func (activateObserverFactory) New(_ context.Context, in resource.Input) (any, error) {
	svc, err := resourcedep.Required[*skills.Service](in, "skillactivate", "skills")
	if err != nil {
		return nil, err
	}
	store, err := resourcedep.Required[*sessions.Store](in, "skillactivate", "sessions")
	if err != nil {
		return nil, err
	}
	return &activateObserver{svc: svc, store: store}, nil
}

// activateObserver persists model-requested skill mentions. Observer
// side effects are best-effort: failures must not fail the run.
type activateObserver struct {
	agent.BaseObserver

	svc   *skills.Service
	store *sessions.Store

	mu sync.Mutex
}

var _ agent.Observer = (*activateObserver)(nil)

func (o *activateObserver) OnRunEnd(ctx context.Context, id agent.Identity, res *agent.Result) {
	if res == nil || res.Status != agent.StatusCompleted {
		return
	}
	var names []string
	seen := map[string]bool{}
	for _, msg := range res.Messages {
		for _, sk := range o.svc.Mentioned(msg.Content.Text()) {
			if !seen[sk.Path] {
				seen[sk.Path] = true
				names = append(names, sk.Name)
			}
		}
	}
	if len(names) == 0 {
		return
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	byAgent := map[string][]string{}
	if err := o.store.ReadState(id.ConversationID, activationsStateKey, &byAgent); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			telemetry.WarnErr(ctx,
				"worldstate: load skill activations failed", err,
				otellog.String("conversation.id", id.ConversationID),
				otellog.String("agent.id", id.AgentID))
		}
		byAgent = map[string][]string{}
	}
	byAgent[id.AgentID] = append(byAgent[id.AgentID], names...)
	if err := o.store.WriteState(id.ConversationID, activationsStateKey, byAgent); err != nil {
		telemetry.Warn(ctx, "worldstate: persist skill activations failed",
			otellog.String("conversation", id.ConversationID),
			otellog.String("agent", id.AgentID),
			otellog.String("error", err.Error()))
	}
}

// consumeActivations reads and clears the model-requested skill names
// for one agent/session pair.
func (s *Service) consumeActivations(
	ctx context.Context, agentID, contextID string,
) []string {
	if s.sessionStore == nil {
		return nil
	}
	var byAgent map[string][]string
	if err := s.sessionStore.ReadState(contextID, activationsStateKey, &byAgent); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			telemetry.WarnErr(ctx, "worldstate: read skill activations failed", err,
				otellog.String("conversation.id", contextID),
				otellog.String("agent.id", agentID))
		}
		return nil
	}
	names := byAgent[agentID]
	if len(names) == 0 {
		return nil
	}
	byAgent[agentID] = nil // consume-on-read
	telemetry.WarnErr(ctx, "worldstate: clear consumed skill activations failed",
		s.sessionStore.WriteState(contextID, activationsStateKey, byAgent),
		otellog.String("conversation.id", contextID),
		otellog.String("agent.id", agentID))
	if len(names) > maxModelActivations {
		names = names[:maxModelActivations]
	}
	return names
}
