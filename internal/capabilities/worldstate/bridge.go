package worldstate

import (
	"context"

	"github.com/GizClaw/flowcraft/core/agent/bindings"
	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/foundation/utils/resourcedep"
)

// ScriptBindingsImpl is the impl name of this package's script surface:
// the standard surface plus the "ocworld" global.
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
		},
	}}, nil
}

// scriptBindingsFactory extends the standard script surface with
// WorldBridge, as the "agent.ScriptBindings/opencraft" resource:
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

var _ resource.Factory = scriptBindingsFactory{}

// Spec implements [resource.Factory].
func (scriptBindingsFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: bindings.ResourceKind,
		Impl: ScriptBindingsImpl,
		Deps: []resource.DepSpec{
			{Name: "standard", Type: bindings.ResourceKind, Required: true},
		},
	}
}

// New implements [resource.Factory].
func (scriptBindingsFactory) New(_ context.Context, in resource.Input) (any, error) {
	standard, err := resourcedep.Required[bindings.Provider](
		in, "worldstate", "standard")
	if err != nil {
		return nil, err
	}
	return bindings.Chain(standard, WorldBridge{}), nil
}
