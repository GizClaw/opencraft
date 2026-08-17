package memory

import (
	"context"
	"sync"

	"github.com/GizClaw/flowcraft/core/agent"
	corememory "github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/sessions"
	"github.com/GizClaw/opencraft/internal/utils/resourcedep"
)

// archiveObserverFactory builds the opencraft.archive observe hook: it
// archives turns the engine did not complete (canceled / interrupted /
// aborted / failed) into the session store and memory. Completed turns
// are archived by the opencraft.commit committer, so the two never
// double-write the same turn.
type archiveObserverFactory struct{}

var _ resource.Factory = archiveObserverFactory{}

func (archiveObserverFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "hook.observe",
		Impl: "opencraft.archive",
		Deps: []resource.DepSpec{
			{Name: "memory", Type: ResourceKind, Required: true},
			{Name: "sessions", Type: sessions.ResourceKind, Required: true},
		},
	}
}

func (archiveObserverFactory) New(_ context.Context, in resource.Input) (any, error) {
	sink, err := resourcedep.Required[corememory.TurnSink](in, "archive", "memory")
	if err != nil {
		return nil, err
	}
	store, err := resourcedep.Required[*sessions.Store](in, "archive", "sessions")
	if err != nil {
		return nil, err
	}
	settings, err := resource.DecodeTyped[commitSettings](in.Settings)
	if err != nil {
		return nil, err
	}
	return &archiveObserver{
		store:    store,
		sink:     sink,
		settings: settings,
		requests: make(map[string]*agent.Request),
	}, nil
}

// archiveObserver captures each run's request at OnRunStart and, in
// OnRunEnd, persists the turn whenever the engine stopped before
// completing. Observer side effects are best-effort: a failure here
// must not fail the run, so errors are swallowed.
type archiveObserver struct {
	agent.BaseObserver

	store    *sessions.Store
	sink     corememory.TurnSink
	settings commitSettings

	mu       sync.Mutex
	requests map[string]*agent.Request // by run id
}

var _ agent.Observer = (*archiveObserver)(nil)

func (o *archiveObserver) OnRunStart(_ context.Context, id agent.Identity, req *agent.Request) {
	o.mu.Lock()
	o.requests[id.RunID] = req
	o.mu.Unlock()
}

func (o *archiveObserver) OnRunEnd(ctx context.Context, id agent.Identity, res *agent.Result) {
	// Archive only turns that were neither completed nor accepted by a
	// Referee: the opencraft.commit committer owns every Committed
	// turn, whatever its status, so the two paths never write the same
	// turn twice. (A Referee can AcceptOutput an interrupted/failed
	// turn, which flips Committed and routes it to the committer.)
	if res == nil || res.Committed || res.Status == agent.StatusCompleted {
		return
	}
	o.mu.Lock()
	req := o.requests[id.RunID]
	delete(o.requests, id.RunID)
	o.mu.Unlock()
	if req == nil {
		return
	}

	// Like the committer, archive the full conversation the turn
	// actually exchanged (request + assistant/tool messages, excluding
	// the world-state context sections) so an interrupted turn keeps
	// its intermediate tool activity for /resume and memory.
	conversation := conversationFromResult(req, res)
	if len(conversation) == 0 {
		return
	}
	if err := o.store.AppendTurn(ctx, id.ConversationID, conversation); err != nil {
		return
	}
	// Memory folding needs at least one produced message; a turn that
	// stopped before any output is already covered by the request
	// being archived above.
	if len(conversation) <= 1 {
		return
	}
	_ = o.sink.CommitTurn(ctx, corememory.Turn{
		Scope:          o.settings.scopeFor(id),
		ConversationID: id.ConversationID,
		IdempotencyKey: res.RunID,
		Messages:       conversation,
	})
}
