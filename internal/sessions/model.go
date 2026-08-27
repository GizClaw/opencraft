package sessions

import (
	"context"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
)

// Model returns the persisted per-session model hint for the
// conversation ("provider/name"), or "" when the session has no stored
// choice and the default routing policy applies. The value is consumed
// by the graph's ${board.model} inference node reference, so it must
// stay in the same "provider/name" shape the router hint expects.
func (s *Store) Model(id string) (string, error) {
	if s == nil || s.db == nil {
		return "", nil
	}
	model, err := s.db.Model(context.Background(), id)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(model), nil
}

// SetModel persists the per-session model hint for the conversation.
// An empty value resets the session to the default routing policy.
func (s *Store) SetModel(id, model string) error {
	if !strings.HasPrefix(id, "s-") {
		return errdefs.Validationf("sessions: invalid session id %q", id)
	}
	model = strings.TrimSpace(model)
	return s.db.SetModel(context.Background(), id, model)
}
