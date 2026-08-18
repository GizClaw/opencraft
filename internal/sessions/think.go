package sessions

import (
	"context"

	"github.com/GizClaw/flowcraft/core/errdefs"
)

// ThinkLevel is the per-session model reasoning effort. It mirrors
// flowcraft's inference.ReasoningEffort enum; the graph's
// ${board.think_level} reference consumes it verbatim.
type ThinkLevel string

const (
	// ThinkLow is the minimal reasoning effort.
	ThinkLow ThinkLevel = "low"
	// ThinkMedium is the default reasoning effort.
	ThinkMedium ThinkLevel = "medium"
	// ThinkHigh is the maximal reasoning effort.
	ThinkHigh ThinkLevel = "high"
)

// Valid reports whether the level is one of the supported values.
func (l ThinkLevel) Valid() bool {
	switch l {
	case ThinkLow, ThinkMedium, ThinkHigh:
		return true
	default:
		return false
	}
}

// SetThink persists the reasoning effort for the session in the
// SQLite session store (session_settings table).
func (s *Store) SetThink(id string, level ThinkLevel) error {
	if !level.Valid() {
		return errdefs.Validationf(
			"sessions: unknown think level %q", level)
	}
	return s.db.SetThinkLevel(context.Background(), id, string(level))
}

// Think returns the persisted reasoning effort for the session,
// defaulting to medium when the session has no stored level.
func (s *Store) Think(id string) (ThinkLevel, error) {
	level, err := s.db.ThinkLevel(context.Background(), id)
	if err != nil {
		return ThinkMedium, err
	}
	switch ThinkLevel(level) {
	case ThinkLow, ThinkMedium, ThinkHigh:
		return ThinkLevel(level), nil
	default:
		return ThinkMedium, nil
	}
}
