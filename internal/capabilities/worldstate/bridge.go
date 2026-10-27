package worldstate

import (
	"context"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/agent/bindings"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/capabilities/compaction"
)

// ScriptBindingsImpl is the impl name of this package's script surface:
// the standard surface plus the harness globals this graph's nodes call
// into Go for ("ocworld", "occompact").
const ScriptBindingsImpl = "opencraft"

// WorldBridge binds the "ocworld" global: the world node's half of the
// harness, exposed to the graph as one call instead of a board
// round-trip. See Merge for why the channel work lives in Go.
type WorldBridge struct{}

var _ bindings.Provider = WorldBridge{}

// Bind implements [bindings.Provider].
func (WorldBridge) Bind(inv bindings.Invocation) ([]bindings.Binding, error) {
	board := inv.Board
	return []bindings.Binding{{
		Name: "ocworld",
		Value: map[string]any{
			// merge renders this turn's world state onto the
			// MainChannel and reports what it prepended, so a script
			// can observe the split without reading the channel back.
			"merge": func() (map[string]any, error) {
				counts, err := Merge(board)
				if err != nil {
					return nil, err
				}
				return map[string]any{
					"sections": counts.Sections,
					"history":  counts.History,
				}, nil
			},
			// markFold records a fold the compact node just applied as a
			// fact about the transcript (worldstate/fold.go): the channel
			// range it replaced, in transcript rows. The model-visible
			// history is the summary plus the rows outside that range, and
			// recording it is what lets a later turn replay the condensed
			// conversation instead of replaying the transcript and folding
			// it again.
			// foldSeq answers "which transcript rows are behind this
			// channel range": the compact node counts channel positions,
			// the store addresses rows by seq. Naming a range is what
			// keeps the fold from shipping the conversation to the tool
			// as an argument.
			"foldSeq": func(start, end float64) (map[string]any, error) {
				from, to, err := foldStash.seqRange(
					board, sectionsCount(board), int(start), int(end))
				if err != nil {
					return nil, err
				}
				return map[string]any{"from": from, "to": to}, nil
			},
			"markFold": func(start, end float64, summary string) error {
				if err := recordFold(
					inv.Context, board, conversationID(inv),
					sectionsCount(board), int(start), int(end), summary,
				); err != nil {
					telemetry.WarnErr(inv.Context,
						"worldstate: record the durable fold failed", err)
					return err
				}
				return nil
			},
		},
	}}, nil
}

// conversationID reports the run's conversation from the ambient run identity
// (the compact node runs inside a turn, so it is always there).
func conversationID(inv bindings.Invocation) string {
	if info, ok := agent.RunInfoFromContext(inv.Context); ok {
		return info.ConversationID
	}
	return ""
}

// sectionsCount reports how many messages the world merge prepended, which is
// where the replayed history starts on the channel.
func sectionsCount(board *agent.Board) int {
	value, ok := board.Var(BoardVarSectionsCount)
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

// scriptBindingsFactory extends the standard script surface with the
// harness globals — WorldBridge's "ocworld" and the compaction node's
// "occompact" (capabilities/compaction) — as the
// "agent.ScriptBindings/opencraft" resource:
//
//	resources:
//	  script.standard:
//	    kind: agent.ScriptBindings
//	    impl: standard
//	    deps: {tools: tools, workspace: ws}
//	  script.world:
//	    kind: agent.ScriptBindings
//	    impl: opencraft
//	    deps: {standard: script.standard}
//
// A graph engine consumes one under its optional "script_bindings" dep
// and then hands it to every script node of the graph. Chaining rather
// than rebuilding the surface keeps the standard resource the single
// place the bridges and their policy are configured; this resource adds
// a global and nothing else.
type scriptBindingsFactory struct{}

var _ resource.Factory[bindings.Provider] = scriptBindingsFactory{}

var depScriptStandard = resource.Require[bindings.Provider]("standard")

// Spec implements [resource.Factory].
func (scriptBindingsFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: bindings.ResourceKind,
		Impl: ScriptBindingsImpl,
		Deps: []resource.Requirement{depScriptStandard.Spec()},
	}
}

// New implements [resource.Factory].
func (scriptBindingsFactory) New(_ context.Context, in resource.Input) (bindings.Provider, error) {
	standard, err := depScriptStandard.Get(in)
	if err != nil {
		return nil, err
	}
	return bindings.Chain(standard, WorldBridge{}, compaction.Bridge{}), nil
}
