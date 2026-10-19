package worldstate

import (
	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/utils/summarytext"
)

// The compaction node runs before every model round and has to know how
// big the prompt is. Rendering the whole conversation for that on every
// round is the most expensive thing the graph does — a long, replayed
// transcript is megabytes of text turned into a token estimate, per round,
// to answer one question.
//
// Most of that number is already known here, at prepare time: the sections
// this hook renders and the transcript it replays are exactly the prefix
// the world node puts at the head of MainChannel. So the hook publishes the
// pair the node needs to skip it — the token estimate of that prefix
// (BoardVarPromptBaseTokens) and how many messages it spans
// (BoardVarPromptBaseLen) — and each round renders only what it appended
// after it (see compact.js, plainEstimateTokens).
//
// Both numbers describe the same prefix and must be written together: a
// token count whose length is stale sizes the node against the wrong
// messages. The pair is deleted, not left standing, when a turn has no such
// prefix (a custom graph, a memory provider that injects sections itself):
// a board can be reused (a resume restores it), and last turn's shape would
// be read as this turn's.
//
// A JS-side writer takes over mid-run in one case only: a fold rewrites the
// prefix the pair describes, so the compaction node invalidates the base and
// re-stamps it from a full measurement. Every turn starts here again.
func publishCompactionBase(
	board *agent.Board,
	sections []Section,
	historyTokens, historyCount int,
) {
	if board == nil {
		return
	}
	msgs := make([]message.Message, 0, len(sections))
	for _, sec := range sections {
		msgs = append(msgs, sec.Message)
	}
	tokens := summarytext.EstimateTokens(msgs) + historyTokens
	if tokens <= 0 {
		board.DeleteVar(config.BoardVarPromptBaseTokens)
		board.DeleteVar(config.BoardVarPromptBaseLen)
		return
	}
	board.SetVar(config.BoardVarPromptBaseTokens, tokens)
	board.SetVar(config.BoardVarPromptBaseLen, len(sections)+historyCount)
}
