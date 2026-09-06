package worldstate

import "context"

// instructionGroupSpec is one layer of the model-facing instruction
// registry. Groups render in fixed order ahead of the per-turn session
// sections; a group returns zero sections when its mode/personality is
// not active. Keep the list small: base rules, optional mode rules,
// optional personality rules — in that order.
type instructionGroupSpec struct {
	// ID identifies the layer for tests and diagnostics.
	ID string
	// Owner names the package responsible for the group's templates.
	Owner string
	// Render returns the group's sections for the current state.
	Render func(ctx context.Context, s *Service) []Section
}

// instructionGroupOrder is the registry consumed by
// Service.instructionSections. It intentionally contains no session
// state: environment/permissions/git/plan/memory/skills/AGENTS.md stay
// in RenderToBoard, which reads live session state every turn.
var instructionGroupOrder = []instructionGroupSpec{
	{
		ID:    "base",
		Owner: "worldstate",
		Render: func(ctx context.Context, s *Service) []Section {
			return renderFragments(s, baseFragmentOrder)
		},
	},
	{
		ID:    "mode",
		Owner: "worldstate",
		Render: func(ctx context.Context, s *Service) []Section {
			return s.modeSections(ctx)
		},
	},
	{
		ID:    "personality",
		Owner: "worldstate",
		Render: func(ctx context.Context, s *Service) []Section {
			return s.personalitySections(ctx)
		},
	},
}
