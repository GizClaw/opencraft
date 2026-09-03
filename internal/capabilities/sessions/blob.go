package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions/state"
)

// requireStateName rejects state document names that could escape the
// per-conversation key space.
func requireStateName(name string) error {
	if name == "" || name == "." || name == ".." {
		return errdefs.Validationf("sessions: invalid state name %q", name)
	}
	if strings.ContainsAny(name, "/"+string(os.PathSeparator)) {
		return errdefs.Validationf("sessions: invalid state name %q", name)
	}
	return nil
}

// WriteState atomically persists a JSON document in conversation_state.
func (s *Store) WriteState(id, name string, v any) error {
	if err := requireID(id); err != nil {
		return err
	}
	if err := requireStateName(name); err != nil {
		return err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := s.db.EnsureConversation(context.Background(), state.Conversation{
		ID: id,
	}); err != nil {
		return err
	}
	return s.db.SetConversationState(context.Background(), id, name, data)
}

// ReadState loads a JSON document from conversation_state. A missing
// document returns an error wrapping os.ErrNotExist.
func (s *Store) ReadState(id, name string, v any) error {
	if err := requireID(id); err != nil {
		return err
	}
	if err := requireStateName(name); err != nil {
		return err
	}
	data, err := s.db.GetConversationState(context.Background(), id, name)
	if errors.Is(err, state.ErrNotFound) {
		return os.ErrNotExist
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
