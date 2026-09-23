package host

import (
	"context"
	"errors"
	"strings"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/delegation/kanban"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/message"
	runtimecore "github.com/GizClaw/flowcraft/core/runtime"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions/state"
	"github.com/GizClaw/opencraft/internal/capabilities/subagents"
)

// reflowWatch is this Host's subscription to delegation board events.
// One watcher belongs to one runtime generation; a reload replaces it.
type reflowWatch struct {
	sub    event.Subscription
	cancel context.CancelFunc
}

// Close stops the watcher.
func (w *reflowWatch) Close() {
	if w == nil {
		return
	}
	if w.cancel != nil {
		w.cancel()
	}
	if w.sub != nil {
		if err := w.sub.Close(); err != nil {
			telemetry.Warn(context.Background(),
				"host: close delegation reflow subscription failed",
				otellog.String("error", err.Error()))
		}
	}
}

// attachReflow subscribes this Host to the runtime's delegation board
// and routes finished async delegations back into the conversation
// that asked for them.
//
// Why the Host owns it: the write is an archive append (the store is
// the single source of truth for conversations), it needs the
// per-workspace store, and the UI refresh is the same signal a
// finished turn emits. A delegation worker finishing while the parent
// is idle would otherwise only be discoverable by polling
// delegation_status from the parent's next turn.
//
// Failure to attach is not fatal: the delegation still runs and the
// parent can still poll, so an assembly without a board simply has no
// reflow.
func (h *Host) attachReflow(ctx context.Context, rt *runtimecore.Runtime) {
	if h == nil || rt == nil {
		return
	}
	value, ok := rt.Resource("events")
	if !ok {
		return
	}
	bus, ok := value.(event.Bus)
	if !ok || bus == nil {
		return
	}
	// The watcher owns its loop context: it must outlive the assembly
	// call (a delegation may finish minutes later) and stop when the
	// generation is replaced or the Host closes.
	watchCtx, cancel := context.WithCancel(context.Background())
	sub, err := bus.Subscribe(watchCtx, kanban.PatternAll())
	if err != nil {
		cancel()
		telemetry.WarnErr(ctx, "host: subscribe delegation reflow failed", err,
			otellog.String("workspace", h.workDir))
		return
	}
	watch := &reflowWatch{sub: sub, cancel: cancel}
	h.reflowMu.Lock()
	previous := h.reflow
	h.reflow = watch
	h.reflowMu.Unlock()
	previous.Close()
	go h.reflowLoop(watchCtx, sub.C())
}

// detachReflow stops the current watcher. Called when the Host closes.
func (h *Host) detachReflow() {
	h.reflowMu.Lock()
	watch := h.reflow
	h.reflow = nil
	h.reflowMu.Unlock()
	watch.Close()
}

func (h *Host) reflowLoop(ctx context.Context, events <-chan event.Envelope) {
	for {
		select {
		case env, ok := <-events:
			if !ok {
				return
			}
			var ev kanban.CardEvent
			if err := env.Decode(&ev); err != nil {
				telemetry.Warn(ctx,
					"host: decode delegation event for reflow failed",
					otellog.String("subject", string(env.Subject)),
					otellog.String("error", err.Error()))
				continue
			}
			result, ok := subagents.ParseCardEvent(ev)
			if !ok {
				continue
			}
			h.reflowDelegation(ctx, result)
		case <-ctx.Done():
			return
		}
	}
}

// reflowDelegation appends one note turn to the conversation the
// delegation was bound to.
//
// Every guard here protects the archive rather than the note: a
// conversation this store does not own is refused (a target names a
// conversation, it does not create one), the ephemeral "ctx-"
// subagent conversations never receive notes, and the note's run key
// is checked first so a redelivered event — a restart re-reading the
// board, a backend publishing the terminal transition twice — cannot
// append the same result a second time.
func (h *Host) reflowDelegation(ctx context.Context, result subagents.Result) {
	store := h.SessionsStore()
	if store == nil {
		return
	}
	conversationID := result.ConversationID
	if strings.HasPrefix(conversationID, subagentConversationPrefix) {
		return
	}
	if !store.Exists(conversationID) {
		telemetry.Warn(ctx, "host: drop delegation reflow for unknown conversation",
			otellog.String("conversation.id", conversationID),
			otellog.String("card.id", result.CardID))
		return
	}
	runKey := result.Key()
	switch _, err := store.TurnByRunID(ctx, conversationID, runKey); {
	case err == nil:
		return
	case !errors.Is(err, state.ErrNotFound):
		telemetry.WarnErr(ctx, "host: look up delegation reflow note failed", err,
			otellog.String("conversation.id", conversationID),
			otellog.String("card.id", result.CardID))
		return
	}
	note := message.NewTextMessage(message.RoleUser, result.Note())
	// The note's author and fields are written with it: this turn
	// exists only as an archive row, and every reader — the title
	// fallback, the transcript card — goes by the kind and the payload
	// rather than by re-reading the note's prose.
	origin := sessions.TurnOrigin{Kind: subagents.KindDelegationNote}
	if payload, err := result.Payload().Encode(); err != nil {
		// Fields of a struct of strings: this cannot fail in a way the
		// note could survive without. The kind still keeps the row out
		// of title derivation.
		telemetry.WarnErr(ctx,
			"host: encode delegation note payload failed", err,
			otellog.String("card.id", result.CardID))
	} else {
		origin.Payload = payload
	}
	if err := store.AppendTurnWithOriginAndRunID(
		ctx, conversationID, runKey, origin, []message.Message{note},
	); err != nil {
		telemetry.WarnErr(ctx, "host: append delegation reflow note failed", err,
			otellog.String("conversation.id", conversationID),
			otellog.String("card.id", result.CardID))
		return
	}
	// The note is a finished turn, not a running one: recording the
	// terminal status here keeps recovery and the UI from reading it
	// as a turn that never settled.
	if err := store.RecordTurnEnd(
		conversationID, runKey, result.At,
		string(agent.StatusCompleted), "", "", "", "", "",
	); err != nil {
		telemetry.WarnErr(ctx, "host: record delegation reflow turn end failed", err,
			otellog.String("conversation.id", conversationID))
	}
	telemetry.Info(ctx, "host: delegated result routed to conversation",
		otellog.String("conversation.id", conversationID),
		otellog.String("card.id", result.CardID),
		otellog.String("subagent", result.Target),
		otellog.String("status", string(result.Status)))
	// Same signal a finished turn emits, so an open conversation
	// reloads its turns and a closed one updates its sidebar entry.
	h.notifySessionUpdated(ctx, conversationID)
}

// subagentConversationPrefix marks the ephemeral conversation ids
// delegated runs execute under. Their contexts are never archived, so
// they never receive notes either.
const subagentConversationPrefix = "ctx-"
