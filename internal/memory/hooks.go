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
		// The conversation is everything the turn actually exchanged:
		// the user request, every assistant reply (including tool-call
		// rounds), and the tool results — but never the world-state
		// context sections the graph prepends to MainChannel.
		conversation := conversationFromResult(req, res)
		if len(conversation) == 0 {
			return nil
		}
		// Full text/reasoning history goes to the project session
		// store; memory summarization stays in the state DB.
		if err := store.AppendTurn(ctx, req.ContextID, conversation); err != nil {
			return err
		}
		return sink.CommitTurn(ctx, corememory.Turn{
			Scope:          settings.scopeFor(id),
			ConversationID: req.ContextID,
			IdempotencyKey: res.RunID,
			Messages:       conversation,
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

// conversationFromResult extracts the turn's conversation messages from
// the final board: everything after the world-state section prefix,
// rendered into text-bearing form so tool calls and results survive
// both the text-only session archive and the memory raw window.
// Compaction summaries appended by the compact graph node are filtered
// out: they are derived context, not conversation. When the board or
// the section boundary marker is unavailable (custom graphs, tests,
// non-graph engines), it falls back to the request plus the result's
// trailing assistant messages.
func conversationFromResult(req *agent.Request, res *agent.Result) []message.Message {
	var msgs []message.Message
	if res != nil && res.LastBoard != nil {
		channel := res.LastBoard.Channel(agent.MainChannel)
		if n, ok := sectionCount(res.LastBoard); ok && n >= 0 && n <= len(channel) {
			msgs = channel[n:]
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
	return strings.HasPrefix(m.Content.Text(), compact.SummaryPrefix+"\n")
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
