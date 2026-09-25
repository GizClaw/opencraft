package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

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
//
// A document write never creates the conversation it belongs to: the
// row is seeded by whoever starts or imports the conversation
// (Store.Create, SeedStartTitle, Store.Import), and documents are
// allowed to exist without it in exactly one case — the settings of a
// draft the user is composing, which have no run yet. Recreating the
// row here instead (which is what this used to do) is how a late writer
// brought a deleted chat back as an empty entry. A retired id is
// refused by the store below (state.ErrRetired).
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
	return s.db.SetConversationState(context.Background(), id, name, data)
}

// ReadState loads a JSON document from conversation_state. A missing
// document returns os.ErrNotExist; a document that exists but does not
// decode returns a *state.CorruptDocumentError naming the row. Both mean
// "no document" to a reader that falls back to a default — use
// ReadStateStrict where that fallback must not be silent.
func (s *Store) ReadState(id, name string, v any) error {
	if err := requireID(id); err != nil {
		return err
	}
	if err := requireStateName(name); err != nil {
		return err
	}
	raw, err := s.db.GetConversationState(context.Background(), id, name)
	if errors.Is(err, state.ErrNotFound) {
		return os.ErrNotExist
	}
	if err != nil {
		return err
	}
	return state.DecodeDocument(id, name, string(raw), v)
}

// ReadStateStrict reads one document exactly like ReadState and reports
// the single failure a fallback would otherwise hide: a document that
// exists but cannot be decoded is logged with its conversation and name
// before the error (a *state.CorruptDocumentError) is returned.
//
// The split is deliberate, and it is about who loses what when a
// document breaks. Documents whose fallback is cosmetic — a custom
// conversation title, replaced by the conversations.title column — are
// read with ReadState. Documents whose fallback silently disables a
// feature (the plan the model is working through, skill activations,
// the usage anchor, the compaction artifact) are read with
// ReadStateStrict, so "the feature quietly stopped working" is
// something the telemetry shows. The read itself is identical; only a
// document that was present and unreadable is reported, never one that
// was never stored or a call the store refused.
func (s *Store) ReadStateStrict(id, name string, v any) error {
	err := s.ReadState(id, name, v)
	var corrupt *state.CorruptDocumentError
	if errors.As(err, &corrupt) {
		// The document API carries no context of its own (state is
		// read from tools that have none), so the warning names the
		// row instead of a span.
		telemetry.WarnErr(context.Background(),
			"sessions: conversation state document is unreadable", err,
			otellog.String("conversation.id", id),
			otellog.String("document", name))
	}
	return err
}
