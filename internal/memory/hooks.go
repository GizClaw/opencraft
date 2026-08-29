package memory

import (
	"context"
	"strings"

	"github.com/GizClaw/flowcraft/core/agent"
	corememory "github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/sessions"
	"github.com/GizClaw/opencraft/internal/tools/compact"
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
		// Delegated subagent runs are ephemeral: flowcraft mints a fresh
		// "ctx-" ContextID per delegation and never persists them, while
		// the project session store only archives "s-" conversations.
		// Skipping here keeps a completed subagent run from failing at
		// commit time with "sessions: invalid session id".
		if !sessions.ValidID(req.ContextID) {
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
		// The project session store keeps the original parts (so
		// /resume re-renders tool activity like the live stream); the
		// memory raw window gets the text-bearing rendering.
		if err := store.AppendTurn(ctx, req.ContextID, raw); err != nil {
			return err
		}
		// Persisting a committed turn must survive a concurrent cancel
		// (e.g. the user interrupted right as the turn settled).
		return sink.CommitTurn(context.WithoutCancel(ctx), corememory.Turn{
			Scope:          settings.scopeFor(id),
			ConversationID: req.ContextID,
			IdempotencyKey: res.RunID,
			Messages:       renderConversation(raw),
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

// extractConversation pulls the turn's raw conversation messages from
// the final board: everything after the world-state section prefix.
// Compaction summaries appended by the compact graph node are filtered
// out: they are derived context, not conversation. When the board or
// the section boundary marker is unavailable (custom graphs, tests,
// non-graph engines), it falls back to the request plus the result's
// trailing assistant messages.
func extractConversation(req *agent.Request, res *agent.Result) []message.Message {
	var msgs []message.Message
	if res != nil && res.LastBoard != nil {
		channel := res.LastBoard.Channel(agent.MainChannel)
		if n, ok := sectionCount(res.LastBoard); ok && n >= 0 && n <= len(channel) {
			msgs = channel[n:]
			// The model-facing board carries the user's turn message
			// with inline media (the opencraft.media prepare hook
			// inlined URL sources before the LLM). The archive keeps
			// the URL form from the original request so attachments
			// stay compact and re-renderable on resume. The user
			// message sits right after the world sections and the
			// replayed history messages.
			if h, ok := historyCount(res.LastBoard); ok && h >= 0 && h < len(msgs) &&
				msgs[h].Role == message.RoleUser {
				restored := make([]message.Message, 0, len(msgs))
				restored = append(restored, msgs[:h]...)
				restored = append(restored, req.Message)
				restored = append(restored, msgs[h+1:]...)
				msgs = restored
			}
		}
	}
	if len(msgs) == 0 {
		msgs = make([]message.Message, 0, 1+len(res.Messages))
		msgs = append(msgs, req.Message)
		msgs = append(msgs, res.Messages...)
	}
	out := make([]message.Message, 0, len(msgs))
	for _, m := range msgs {
		if isSummaryMessage(m) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// renderConversation renders raw messages into text-bearing form, so
// tool calls and results survive the memory raw window (which only
// reads Content.Text()).
func renderConversation(msgs []message.Message) []message.Message {
	out := make([]message.Message, 0, len(msgs))
	for _, m := range msgs {
		if rendered := renderConversationMessage(m); rendered != nil {
			out = append(out, *rendered)
		}
	}
	return out
}

// isSummaryMessage reports whether m is a compaction summary injected
// by the compact graph node (a marked user message). Keeping them out
// of the archive and the memory raw window means the summary only
// shapes the current turn's context; cross-turn continuity stays with
// the memory assembly.
func isSummaryMessage(m message.Message) bool {
	return m.Role == message.RoleUser &&
		strings.HasPrefix(m.Content.Text(), compact.SummaryPrefix+"\n")
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

// renderConversationMessage makes one MainChannel message persistable:
// original parts are kept (so tool-role messages stay valid for the
// memory turn contract) and tool calls/results are appended as plain
// text. Without this the memory raw window (which stores
// Content.Text()) and the session archive (text/reasoning only) would
// silently drop the turn's intermediate tool activity.
func renderConversationMessage(m message.Message) *message.Message {
	out := m.Clone()
	if len(compact.ToolActivity(m)) == 0 {
		return &out
	}
	out.Content.Parts = append(out.Content.Parts,
		message.TextPart{Text: compact.RenderMessage(m)})
	return &out
}
