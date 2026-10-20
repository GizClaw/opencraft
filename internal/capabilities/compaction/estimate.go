// Package compaction owns the graph compaction node's Go half.
//
// The node's policy stays in the graph asset
// (foundation/config/assets/graphs/nodes/compact.js): when to fold, which
// slice of the conversation to fold, and how the result is written back.
// That is the part a deployment tunes through the node's config and a
// custom graph replaces wholesale, so it belongs to the graph.
//
// Its measurement is not policy. "How many prompt tokens does this
// channel cost" is the same rule the memory hooks, the context meter and
// the fold tool already use, and it lives in
// foundation/utils/summarytext. The node used to re-implement it in JS: a
// per-character CJK scan through goja, ~80x the native cost — 62 ms per
// sizing pass on a 230 KB channel and 171 ms on a 560 KB one, and the
// node sizes twice per round. It also needed a cross-language fixture to
// hold two copies of one rule together. This package exposes the Go rule
// to the node instead:
//
//	occompact.estimate(from) -> tokens of MainChannel[from:]
//
// The binding rides the same script surface as the world node's merge
// (see worldstate.ScriptBindingsImpl), so a graph consumes both through
// one deploy-resource dependency.
package compaction

import (
	"fmt"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/agent/bindings"
	"github.com/GizClaw/flowcraft/core/errdefs"

	"github.com/GizClaw/opencraft/internal/foundation/utils/summarytext"
)

// EstimateChannelTokens reports the prompt footprint, in tokens, of the
// MainChannel from index from onward — the measurement the compaction
// node budgets a fold against.
//
// The messages are read as a view: the estimate walks the board's own
// messages, so nothing is copied and nothing crosses a script bridge. An
// index outside the channel is a harness bug rather than a user input —
// the node only ever asks about slices it just read from the same
// channel — so it fails loudly instead of estimating the wrong slice.
func EstimateChannelTokens(board *agent.Board, from int) (int, error) {
	if board == nil {
		return 0, fmt.Errorf("compaction: estimate: board is nil")
	}
	msgs := board.ChannelView(agent.MainChannel)
	if from < 0 || from > len(msgs) {
		return 0, errdefs.Internalf(
			"compaction: estimate: index %d is outside the %d-message channel",
			from, len(msgs))
	}
	return summarytext.EstimateTokens(msgs[from:]), nil
}

// Bridge binds the "occompact" global: the sizing call the compaction
// node makes every round.
type Bridge struct{}

var _ bindings.Provider = Bridge{}

// Bind implements [bindings.Provider].
func (Bridge) Bind(inv bindings.Invocation) ([]bindings.Binding, error) {
	board := inv.Board
	return []bindings.Binding{{
		Name: "occompact",
		Value: map[string]any{
			// estimate is the node's only Go call: it replaces the
			// mirrored renderer and character scan, not the fold
			// decision.
			"estimate": func(from int) (int, error) {
				return EstimateChannelTokens(board, from)
			},
		},
	}}, nil
}
