package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/telemetry"
)

// SessionSettingsName is the conversation_state document a session's own
// settings live in: the reasoning effort, the model hint and the sandbox
// permission mode. It replaced the session_settings table (workspace
// migration 019), so a session's state is now one row of the single
// per-conversation key/value table instead of a table of its own holding
// the same three columns beside it.
const SessionSettingsName = "settings"

// sessionSettings is that document. An absent key means "nothing
// stored", which readers treat exactly like an empty value.
type sessionSettings struct {
	ThinkLevel string `json:"think_level,omitempty"`
	Model      string `json:"model,omitempty"`
	Mode       string `json:"mode,omitempty"`
}

// SetThinkLevel upserts the per-session reasoning effort
// (low | medium | high).
func (s *Store) SetThinkLevel(ctx context.Context, contextID, level string) error {
	if strings.TrimSpace(level) == "" {
		return errdefs.Validation(
			errors.New("state: think level is required"))
	}
	return s.updateSessionSettings(ctx, contextID, func(doc *sessionSettings) {
		doc.ThinkLevel = level
	})
}

// ThinkLevel returns the persisted reasoning effort for a session.
// A conversation with no settings returns "", letting the caller apply
// its default.
func (s *Store) ThinkLevel(ctx context.Context, contextID string) (string, error) {
	doc, err := s.sessionSettings(ctx, contextID)
	if err != nil {
		return "", err
	}
	return doc.ThinkLevel, nil
}

// SetModel upserts the per-session model hint ("provider/name", or an
// empty string for the default routing policy).
func (s *Store) SetModel(ctx context.Context, contextID, model string) error {
	return s.updateSessionSettings(ctx, contextID, func(doc *sessionSettings) {
		doc.Model = model
	})
}

// Model returns the persisted model hint for a session. A conversation
// that never set one returns "", meaning the default routing policy
// applies.
func (s *Store) Model(ctx context.Context, contextID string) (string, error) {
	doc, err := s.sessionSettings(ctx, contextID)
	if err != nil {
		return "", err
	}
	return doc.Model, nil
}

// SetMode upserts the per-session sandbox permission mode.
func (s *Store) SetMode(ctx context.Context, contextID, mode string) error {
	if strings.TrimSpace(mode) == "" {
		return errdefs.Validation(errors.New("state: mode is required"))
	}
	return s.updateSessionSettings(ctx, contextID, func(doc *sessionSettings) {
		doc.Mode = mode
	})
}

// Mode returns the persisted sandbox permission mode for a session.
// A conversation with no settings returns "", letting the caller apply
// its default.
func (s *Store) Mode(ctx context.Context, contextID string) (string, error) {
	doc, err := s.sessionSettings(ctx, contextID)
	if err != nil {
		return "", err
	}
	return doc.Mode, nil
}

// RemoveSettings deletes one session's settings document. Removing an
// unknown session is a no-op.
func (s *Store) RemoveSettings(ctx context.Context, contextID string) error {
	if strings.TrimSpace(contextID) == "" {
		return errdefs.Validation(
			errors.New("state: session context_id is required"))
	}
	if _, err := s.db.SQLDB().ExecContext(ctx,
		`DELETE FROM conversation_state
		 WHERE conversation_id = ? AND name = ?`,
		contextID, SessionSettingsName); err != nil {
		return fmt.Errorf("state: remove settings %s: %w", contextID, err)
	}
	return nil
}

// sessionSettings reads one session's settings document. A missing
// document is the zero value, not an error: every reader treats "never
// set" and "set to empty" the same way.
func (s *Store) sessionSettings(
	ctx context.Context, contextID string,
) (sessionSettings, error) {
	if strings.TrimSpace(contextID) == "" {
		return sessionSettings{}, errdefs.Validation(
			errors.New("state: session context_id is required"))
	}
	var raw string
	err := s.db.SQLDB().QueryRowContext(ctx,
		`SELECT value_json FROM conversation_state
		 WHERE conversation_id = ? AND name = ?`,
		contextID, SessionSettingsName).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionSettings{}, nil
	}
	if err != nil {
		return sessionSettings{}, fmt.Errorf(
			"state: read session settings %s: %w", contextID, err)
	}
	return decodeSessionSettings(contextID, raw)
}

// updateSessionSettings applies mutate to a session's settings document
// inside one transaction. The settings share a single document, so a
// read-modify-write is the only way to change one key without clobbering
// the others, and holding the transaction is what keeps two writers
// that touch different keys (a model change and a permission change
// from the UI) from losing one of them.
func (s *Store) updateSessionSettings(
	ctx context.Context,
	contextID string,
	mutate func(*sessionSettings),
) error {
	if strings.TrimSpace(contextID) == "" {
		return errdefs.Validation(
			errors.New("state: session context_id is required"))
	}
	tx, err := s.db.SQLDB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("state: begin settings update %s: %w", contextID, err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			telemetry.WarnErr(ctx, "state: rollback settings update failed", err)
		}
	}()
	var raw string
	exists := true
	err = tx.QueryRowContext(ctx,
		`SELECT value_json FROM conversation_state
		 WHERE conversation_id = ? AND name = ?`,
		contextID, SessionSettingsName).Scan(&raw)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		exists = false
	case err != nil:
		return fmt.Errorf(
			"state: read session settings %s: %w", contextID, err)
	}
	doc := sessionSettings{}
	if exists {
		if doc, err = decodeSessionSettings(contextID, raw); err != nil {
			return err
		}
	}
	mutate(&doc)
	if !exists && doc == (sessionSettings{}) {
		// Nothing was stored and nothing was set: leave the absence
		// alone rather than storing a document that says nothing.
		return nil
	}
	payload, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("state: encode session settings %s: %w", contextID, err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if exists {
		_, err = tx.ExecContext(ctx,
			`UPDATE conversation_state SET value_json = ?, updated_at = ?
			 WHERE conversation_id = ? AND name = ?`,
			string(payload), now, contextID, SessionSettingsName)
	} else {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO conversation_state(
				conversation_id, name, value_json, updated_at
			) VALUES (?, ?, ?, ?)`,
			contextID, SessionSettingsName, string(payload), now)
	}
	if err != nil {
		return fmt.Errorf("state: write session settings %s: %w", contextID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("state: commit session settings %s: %w", contextID, err)
	}
	return nil
}

// decodeSessionSettings decodes one stored document. A document written
// by a newer build may carry keys this one does not know; they are
// ignored, exactly as an unknown key of any other state document is. A
// document that does not decode at all comes back as the same
// *CorruptDocumentError every other document read produces, so a reader
// that reports it names the row ("settings" of which conversation)
// rather than a bare JSON position.
func decodeSessionSettings(contextID, raw string) (sessionSettings, error) {
	var doc sessionSettings
	if strings.TrimSpace(raw) == "" {
		return doc, nil
	}
	if err := DecodeDocument(contextID, SessionSettingsName, raw, &doc); err != nil {
		return doc, err
	}
	return doc, nil
}
