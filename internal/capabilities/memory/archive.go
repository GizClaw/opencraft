package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/GizClaw/flowcraft/core/agent"
	corememory "github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/utils/resourcedep"
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

func (archiveObserverFactory) New(ctx context.Context, in resource.Input) (any, error) {
	sink, err := resourcedep.Required[corememory.TurnSink](in, "archive", "memory")
	if err != nil {
		return nil, err
	}
	store, err := resourcedep.Required[*sessions.Store](in, "archive", "sessions")
	if err != nil {
		return nil, err
	}
	settings, err := resource.DecodeTyped[commitSettings](ctx, in.Settings)
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
		telemetry.Warn(ctx, "memory: archive skipped run with no captured request",
			otellog.String("conversation", id.ConversationID),
			otellog.String("run", id.RunID),
			otellog.String("status", string(res.Status)))
		return
	}

	// Delegated subagent contexts are ephemeral ("ctx-" ids minted per
	// delegation) and never archived: the session store only accepts
	// "s-" conversation ids. Mirror the committer's guard so both paths
	// agree on what gets persisted.
	if !sessions.ValidID(id.ConversationID) {
		return
	}

	// Like the committer, archive the full conversation the turn
	// actually exchanged (request + assistant/tool messages, excluding
	// the world-state context sections) so an interrupted turn keeps
	// its intermediate tool activity for /resume and memory.
	raw := extractConversation(req, res)
	if len(raw) == 0 {
		telemetry.Warn(ctx, "memory: archive skipped run with no conversation",
			otellog.String("conversation", id.ConversationID),
			otellog.String("run", id.RunID),
			otellog.String("status", string(res.Status)))
		return
	}
	// Memory folding needs at least one produced message; a turn that
	// stopped before any output is already covered by the archive.
	// The run context is cancelled for interrupted/canceled turns; the
	// memory commit must still persist the partial output so the next
	// turn's context keeps the interrupted content. WithoutCancel keeps
	// the derived values but detaches the cancellation.
	persistCtx := context.WithoutCancel(ctx)
	// The write must outlive the run context: canceled turns deliver
	// OnRunEnd with an already-canceled ctx, and SQLite observes it on
	// the first query. Without WithoutCancel a stopped turn's whole
	// transcript is silently dropped.
	if err := commitArchivedTurn(
		persistCtx, o.store, o.sink, o.settings.scopeFor(id),
		id.ConversationID, id.RunID, raw,
	); err != nil {
		telemetry.WarnErr(ctx, "memory: archive turn failed", err,
			otellog.String("conversation", id.ConversationID),
			otellog.String("run", id.RunID),
			otellog.String("status", string(res.Status)))
	}
}

// RecoverTurn persists one turn crash recovery reconstructed from a run
// checkpoint (see orchestration/host/recover.go). The write is the same
// as the one the archive observer performs for an in-process
// interruption: archive rows and memory rows in a single transaction,
// then a memory fold. The archive's (conversation, run) uniqueness
// makes a second recovery of the same run a no-op.
//
// scope is only consulted by sinks that cannot join the archive
// transaction; the SQLite assembly ignores it. Recovery runs outside a
// deployment hook, so it passes the same scope convention imports use.
func RecoverTurn(
	ctx context.Context,
	store *sessions.Store,
	sink corememory.TurnSink,
	conversationID, runID string,
	msgs []message.Message,
) error {
	if store == nil {
		return errors.New("memory: recover turn without a session store")
	}
	if sink == nil {
		return errors.New("memory: recover turn without a memory sink")
	}
	return commitArchivedTurn(
		ctx, store, sink,
		corememory.Scope{RuntimeID: "opencraft"},
		conversationID, runID, msgs,
	)
}

// commitArchivedTurn writes one unfinished turn: the archive rows and
// the memory rows land in one transaction when the sink can join it,
// and the memory assembly folds afterwards. Sinks that cannot join fall
// back to appending memory after the archive write.
func commitArchivedTurn(
	ctx context.Context,
	store *sessions.Store,
	sink corememory.TurnSink,
	scope corememory.Scope,
	conversationID, runID string,
	msgs []message.Message,
) error {
	if atomic, ok := sink.(atomicTurnSink); ok {
		if err := store.AppendTurnWithRunIDAndHook(
			ctx, conversationID, runID, msgs,
			func(ctx context.Context, tx *sql.Tx) error {
				return atomic.AppendMessagesTx(
					ctx, tx, conversationID, runID,
					renderConversation(msgs),
				)
			},
		); err != nil {
			return err
		}
		if len(msgs) <= 1 {
			return nil
		}
		if err := atomic.FoldOnly(ctx, conversationID); err != nil {
			return fmt.Errorf("memory: fold after archive: %w", err)
		}
		return nil
	}
	if err := store.AppendTurnWithRunID(
		ctx, conversationID, runID, msgs,
	); err != nil {
		return err
	}
	if len(msgs) <= 1 {
		return nil
	}
	if err := sink.CommitTurn(ctx, corememory.Turn{
		Scope:          scope,
		ConversationID: conversationID,
		IdempotencyKey: runID,
		Messages:       renderConversation(msgs),
	}); err != nil {
		return fmt.Errorf("memory: commit after archive: %w", err)
	}
	return nil
}
