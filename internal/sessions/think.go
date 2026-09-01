package sessions

import (
	"context"

	"github.com/GizClaw/flowcraft/core/errdefs"
)

// ThinkLevel is the per-session model reasoning effort. It mirrors
// flowcraft's inference.ReasoningEffort enum; the graph's
// ${board:think_level} reference consumes it verbatim.
type ThinkLevel string

const (
	// ThinkMinimal is the lowest reasoning effort.
	ThinkMinimal ThinkLevel = "minimal"
	// ThinkLow is a low reasoning effort.
	ThinkLow ThinkLevel = "low"
	// ThinkMedium is the default reasoning effort.
	ThinkMedium ThinkLevel = "medium"
	// ThinkHigh is a high reasoning effort.
	ThinkHigh ThinkLevel = "high"
	// ThinkXHigh is the highest reasoning effort.
	ThinkXHigh ThinkLevel = "xhigh"
)

// Valid reports whether the level is one of the supported values.
func (l ThinkLevel) Valid() bool {
	switch l {
	case ThinkMinimal, ThinkLow, ThinkMedium, ThinkHigh, ThinkXHigh:
		return true
	default:
		return false
	}
}

// SetThink persists the reasoning effort for the session in the
// SQLite session store (session_settings table).
func (s *Store) SetThink(ctx context.Context, id string, level ThinkLevel) error {
	if !level.Valid() {
		return errdefs.Validationf(
			"sessions: unknown think level %q", level)
	}
	return s.db.SetThinkLevel(ctx, id, string(level))
}

// Think returns the persisted reasoning effort for the session,
// defaulting to medium when the session has no stored level.
func (s *Store) Think(ctx context.Context, id string) (ThinkLevel, error) {
	level, err := s.db.ThinkLevel(ctx, id)
	if err != nil {
		return ThinkMedium, err
	}
	switch ThinkLevel(level) {
	case ThinkMinimal, ThinkLow, ThinkMedium, ThinkHigh, ThinkXHigh:
		return ThinkLevel(level), nil
	default:
		return ThinkMedium, nil
	}
}
