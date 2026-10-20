package worldstate

import (
	"encoding/json"
	"fmt"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// Board vars RenderTurn writes and Merge reads. The tail block stays
// config.BoardVarTailBlock (the graph asset and the compaction node
// speak that name); these four are the world state's own.
const (
	// BoardVarSections carries the JSON world-state sections: the
	// cache-stable prefix the model sees before the conversation.
	BoardVarSections = "world.sections"
	// BoardVarHistory carries the JSON full-history replay, which sits
	// after the sections and before the turn's own messages.
	BoardVarHistory = "world.history"
	// BoardVarSectionsCount is how many messages Merge prepended from
	// BoardVarSections.
	BoardVarSectionsCount = "world.sections.count"
	// BoardVarHistoryCount is how many replayed messages sit between
	// the sections and the turn's own messages. Lifecycle hooks locate
	// the user's turn message on the channel as
	// sections.count + history.count.
	BoardVarHistoryCount = "world.history.count"
)

// MergeCounts is what one merge prepended.
type MergeCounts struct {
	// Sections is the number of world-state section messages.
	Sections int
	// History is the number of replayed history messages.
	History int
}

// Merge replays the rendered world state onto the MainChannel: the
// sections first, then the replayed history, then whatever the channel
// already held (the restored conversation and this turn's message).
//
// It is the Go half of the graph's world node and deliberately not a
// script: reading the board's channel through the script bridge costs a
// JSON projection per message in each direction, and the world node
// reads and rewrites the *whole* transcript every turn. At 2 MB of
// conversation that was ~70 ms of time-to-first-token per turn, at 8 MB
// ~280 ms, for a value the engine board already holds in Go.
//
// The per-turn tail block (plan, skills, extras) is appended to the
// turn's own message rather than becoming one of its own: a provider
// prompt cache only reuses the longest common prefix of the previous
// request, so a block that changes on nearly every turn must not sit in
// front of the conversation. See tailblock.go for the framing, and only
// fall back to a message of its own when the channel's last message is
// not the user's (a custom engine seeding its own board) — injecting
// context onto someone else's turn would misattribute it.
func Merge(board *agent.Board) (MergeCounts, error) {
	var counts MergeCounts
	if board == nil {
		return counts, fmt.Errorf("worldstate: merge: board is nil")
	}
	sections, err := decodeVar[[]Section](board, BoardVarSections)
	if err != nil {
		return counts, err
	}
	history, err := decodeVar[[]message.Message](board, BoardVarHistory)
	if err != nil {
		return counts, err
	}

	rest := board.ChannelView(agent.MainChannel)
	out := make([]message.Message, 0,
		len(sections)+len(history)+len(rest)+1)
	for _, section := range sections {
		out = append(out, message.Message{
			Role:    section.Role,
			Content: section.Content,
		})
	}
	counts.Sections = len(sections)
	// Written before the channel so a reader of the board never sees a
	// merged channel with counts from a previous turn.
	board.SetVar(BoardVarSectionsCount, counts.Sections)
	out = append(out, history...)
	counts.History = len(history)
	board.SetVar(BoardVarHistoryCount, counts.History)

	tail := board.GetVarString(config.BoardVarTailBlock)
	var ownMessage bool
	// The tail rides the turn's own message. Replacing the slice copy's
	// last element keeps the board's own message untouched, and
	// SetChannel clones below.
	if tail != "" {
		if last := len(rest) - 1; last >= 0 && rest[last].Role == message.RoleUser {
			msg := rest[last]
			parts := make([]message.Part, 0, len(msg.Content.Parts)+1)
			parts = append(parts, msg.Content.Parts...)
			parts = append(parts, message.TextPart{Text: tail})
			msg.Content = message.Content{Parts: parts}
			rest[last] = msg
		} else {
			ownMessage = true
		}
	}
	out = append(out, rest...)
	if ownMessage {
		out = append(out, message.NewTextMessage(message.RoleUser, tail))
	}
	board.SetChannel(agent.MainChannel, out)
	return counts, nil
}

// decodeVar decodes one JSON board var, treating a missing var as the
// empty value: a turn without replayed history writes none, and the
// node has always read both vars as "absent means none".
func decodeVar[T any](board *agent.Board, name string) (T, error) {
	var value T
	raw := board.GetVarString(name)
	if raw == "" {
		return value, nil
	}
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return value, errdefs.Internalf(
			"worldstate: merge: board var %q: %v", name, err)
	}
	return value, nil
}
