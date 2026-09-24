package memory

import (
	"context"

	"github.com/GizClaw/flowcraft/core/agent"
	corememory "github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/ids"
	"github.com/GizClaw/opencraft/internal/foundation/utils/resourcedep"
	"github.com/GizClaw/opencraft/internal/foundation/utils/summarytext"
)

// foldSink is implemented by the production memory resource: it folds a
// conversation's transcript into its summary tree. The commit hook writes
// the turn to the session store and then folds, in that order — the fold
// reads the rows the archive just wrote.
type foldSink interface {
	FoldOnly(ctx context.Context, conversationID string) error
}

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

func (commitHookFactory) New(ctx context.Context, in resource.Input) (any, error) {
	sink, err := resourcedep.Required[corememory.TurnSink](in, "memory", "memory")
	if err != nil {
		return nil, err
	}
	store, err := resourcedep.Required[*sessions.Store](in, "memory", "sessions")
	if err != nil {
		return nil, err
	}
	settings, err := resource.DecodeTyped[commitSettings](ctx, in.Settings)
	if err != nil {
		return nil, err
	}
	return agent.CommitterFunc(func(
		ctx context.Context, id agent.Identity, req *agent.Request, res *agent.Result,
	) error {
		if len(res.Messages) == 0 {
			return nil
		}
		// Delegated subagent runs are ephemeral: flowcraft mints a
		// fresh "ctx-" ContextID per delegation (ids.ContextPrefix) and
		// never persists them, while the project session store only
		// archives "s-" conversations (ids.SessionPrefix). Skipping
		// here keeps a completed subagent run from failing at commit
		// time with "sessions: invalid session id".
		if !ids.IsSession(req.ContextID) {
			return nil
		}
		// The conversation is everything the turn actually exchanged:
		// the user request, every assistant reply (including tool-call
		// rounds), and the tool results — but never the world-state
		// context sections the graph prepends to MainChannel.
		raw := extractConversation(req, res)
		if len(raw) == 0 {
			return nil
		}
		persistCtx := context.WithoutCancel(ctx)
		if folder, ok := sink.(foldSink); ok {
			// Same rule as the archive observer: a Referee may accept a
			// canceled/interrupted run into the committer path, so the
			// store write must not inherit the run's cancellation.
			// The turn is written once, by the store, and the memory
			// side folds what was written: there is no second copy to
			// keep in step (see projection.go).
			if err := store.AppendTurnWithRunID(
				persistCtx, req.ContextID, res.RunID, raw,
			); err != nil {
				return err
			}
			return folder.FoldOnly(persistCtx, req.ContextID)
		}
		if err := store.AppendTurnWithRunID(
			persistCtx, req.ContextID, res.RunID, raw,
		); err != nil {
			return err
		}
		return sink.CommitTurn(persistCtx, corememory.Turn{
			Scope:          settings.scopeFor(id),
			ConversationID: req.ContextID,
			IdempotencyKey: res.RunID,
			Messages:       raw,
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

// worldSectionsCountVar is the board var the world node sets to the
// number of world-state context messages it prepends to MainChannel.
// Lifecycle hooks use it to tell the seeded conversation apart from
// injected context when persisting a turn.
const worldSectionsCountVar = "world.sections.count"

// extractConversation is ExtractTurnMessages over one finished result.
func extractConversation(req *agent.Request, res *agent.Result) []message.Message {
	var board *agent.Board
	var tail []message.Message
	if res != nil {
		board = res.LastBoard
		tail = res.Messages
	}
	return ExtractTurnMessages(board, req, tail)
}

// ExtractTurnMessages pulls the turn's raw conversation messages from
// the board it left behind: everything after the world-state section
// prefix. Compaction summaries appended by the compact graph node are
// filtered out: they are derived context, not conversation. When the
// board or the section boundary marker is unavailable (custom graphs,
// tests, non-graph engines, a checkpoint whose world node never ran),
// it falls back to the request plus tail.
//
// The messages are read through [agent.Board.ChannelView], so the
// result aliases board storage: it is read-only, and the persistence
// callers keep it that way (they clone before storing). Clone a
// message before editing it.
//
// req restores the turn's user message in its original (pre-inline)
// form, the shape the archive keeps for attachments. A nil req (a
// checkpoint written before the recovery annotation existed, or one
// over the size cap) keeps the board's own message and skips
// re-anchoring. Crash recovery passes a checkpoint board and the
// request decoded from the checkpoint annotation.
func ExtractTurnMessages(
	board *agent.Board,
	req *agent.Request,
	tail []message.Message,
) []message.Message {
	var msgs []message.Message
	if board != nil {
		// Both reads go through ChannelView: this function only slices
		// and filters, and every path that keeps the messages clones
		// first (filterArchive), so the board's storage is never
		// mutated through the view.
		channel := board.ChannelView(agent.MainChannel)
		if n, ok := sectionCount(board); ok && n >= 0 && n <= len(channel) {
			// The compact node moves the prefix it folds onto a side
			// channel before shrinking MainChannel for the model. That
			// channel holds the folded messages in order, so the union
			// with the remaining MainChannel restores the turn's full
			// conversation while the model only ever sees the compacted
			// view. The compact node protects the turn's user message
			// (world.compact.turn_start) so a fold that runs later can
			// leave it behind while newer rounds move to the side
			// channel: the message is then re-anchored in front.
			archived := board.ChannelView(config.CompactArchiveChannel)
			// Full-history replay prepends the persisted conversation
			// between the world sections and this turn's messages.
			// Those replayed messages are context, not new
			// conversation, so they must be dropped before persisting
			// the turn.
			h, hasHistory := historyCount(board)
			if !hasHistory || h < 0 {
				h = 0
			}
			joined := make([]message.Message, 0, len(archived)+len(channel)-n)
			joined = append(joined, archived...)
			joined = append(joined, channel[n:]...)
			if h > len(joined) {
				h = len(joined)
			}
			turn := joined[h:]
			// The model-facing board carries the user's turn
			// message with inline media (the opencraft.media
			// prepare hook inlined URL sources before the LLM).
			// The archive keeps the URL form from the original
			// request so attachments stay compact and
			// re-renderable on resume.
			askPos := -1
			if req != nil {
				if ts, ok := compactTurnStart(board); ok &&
					ts >= n && ts < len(channel) &&
					channel[ts].Role == message.RoleUser {
					askPos = len(archived) + (ts - n) - h
				}
				if askPos < 0 && len(turn) > 0 &&
					turn[0].Role == message.RoleUser {
					askPos = 0
				}
			}
			if req != nil && askPos >= 0 && askPos < len(turn) &&
				turn[askPos].Role == message.RoleUser {
				restored := make([]message.Message, 0, len(turn)+1)
				restored = append(restored, req.Message)
				for i, m := range turn {
					if i == askPos {
						continue
					}
					restored = append(restored, m)
				}
				msgs = restored
			} else {
				// No usable anchor (custom graph or an older
				// board): keep every turn message as exchanged.
				msgs = turn
			}
		}
	}
	if len(msgs) == 0 {
		msgs = make([]message.Message, 0, 1+len(tail))
		if req != nil {
			msgs = append(msgs, req.Message)
		}
		msgs = append(msgs, tail...)
	}
	out := make([]message.Message, 0, len(msgs))
	for _, m := range msgs {
		if isInjectedContext(m) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// isInjectedContext reports whether m is derived context injected by the
// harness rather than something the turn exchanged: a compaction summary,
// or a context notice telling the model that compaction is out of options.
// Keeping them out of the archive and the memory raw window means they only
// shape the turn they were written for; cross-turn continuity stays with
// the memory assembly.
func isInjectedContext(m message.Message) bool {
	if m.Role != message.RoleUser {
		return false
	}
	text := m.Content.Text()
	return summarytext.IsSummaryText(text) || summarytext.IsContextNotice(text)
}

// sectionCount reads the world node's prepend count off the board. The
// script bridge round-trips JS numbers as int64, so integer and float
// encodings are both accepted. ok reports whether the marker exists at
// all, so a zero count is honored when it is set.
func sectionCount(board *agent.Board) (int, bool) {
	if board == nil {
		return 0, false
	}
	v, ok := board.GetVar(worldSectionsCountVar)
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case float32:
		return int(n), true
	}
	return 0, false
}

// historyCount reads the world node's replayed-history count off the
// board. The user's turn message sits at world.sections.count +
// world.history.count on the MainChannel.
func historyCount(board *agent.Board) (int, bool) {
	if board == nil {
		return 0, false
	}
	v, ok := board.GetVar("world.history.count")
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case float32:
		return int(n), true
	}
	return 0, false
}

// compactTurnStart reads the compact node's index of the current
// turn's user message on the MainChannel. The node keeps that message
// out of every fold and moves the index down as folded messages leave
// the channel, so the archive can re-anchor the turn even when older
// rounds were folded after it.
func compactTurnStart(board *agent.Board) (int, bool) {
	if board == nil {
		return 0, false
	}
	v, ok := board.GetVar("world.compact.turn_start")
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case float32:
		return int(n), true
	}
	return 0, false
}
