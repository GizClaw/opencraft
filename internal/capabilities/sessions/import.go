package sessions

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions/state"
)

const (
	maxImportSourceBytes  = 512
	maxImportTitleBytes   = 1024
	maxImportTurnCount    = 10000
	maxImportMessageCount = 200000
)

// ImportRequest is the neutral session-import payload.
type ImportRequest struct {
	Title  string       `json:"title"`
	Source string       `json:"source"`
	Turns  []ImportTurn `json:"turns"`
}

// ImportTurn is one archived turn of an imported conversation.
type ImportTurn struct {
	At       time.Time         `json:"at"`
	Messages []message.Message `json:"messages"`
}

// Import writes a new conversation from a neutral request into SQLite
// and returns the generated s-xxx id. The same Source maps to the same
// session id. Memory seeding is deliberately not performed here.
func (s *Store) Import(ctx context.Context, req ImportRequest) (string, error) {
	if err := validateImportRequest(req); err != nil {
		return "", err
	}
	source := strings.TrimSpace(req.Source)

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, found, err := s.db.ConversationByImportSource(ctx, source); err != nil {
		return "", err
	} else if found {
		if existing.ImportReady {
			return existing.ID, nil
		}
		if _, active := s.importPending[existing.ID]; active {
			return existing.ID, nil
		}
		if err := s.removeLocked(ctx, existing.ID); err != nil {
			return "", err
		}
	}

	var archives []struct {
		At       time.Time
		Messages []message.Message
	}
	now := time.Now().UTC()
	for _, turn := range req.Turns {
		for _, m := range turn.Messages {
			if len(m.Content.Parts) > 0 {
				if err := m.Validate(); err != nil {
					return "", errdefs.Validationf(
						"sessions: import message: %w", err)
				}
			}
		}
		msgs := filterArchive(turn.Messages)
		if len(msgs) == 0 {
			continue
		}
		at := turn.At
		if at.IsZero() {
			at = now
		} else if !at.Equal(at.UTC()) {
			at = at.UTC()
		}
		archives = append(archives, struct {
			At       time.Time
			Messages []message.Message
		}{At: at, Messages: msgs})
	}
	if len(archives) == 0 {
		return "", errdefs.Validationf(
			"sessions: import contains no archiveable messages")
	}

	id := NewID()
	dir := s.dir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	telemetry.WarnErr(ctx, "sessions: secure import dir failed",
		os.Chmod(dir, 0o700))

	title := strings.TrimSpace(req.Title)
	if title == "" {
		for _, arch := range archives {
			title = firstArchiveTitle(arch.Messages)
			if title != "" {
				break
			}
		}
	}
	if title == "" {
		title = "(imported)"
	}
	createdAt := archives[0].At
	updatedAt := archives[len(archives)-1].At
	conv := state.Conversation{
		ID:           id,
		Title:        title,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
		ImportSource: source,
	}
	if err := s.db.EnsureConversation(ctx, conv); err != nil {
		telemetry.WarnErr(ctx, "sessions: clean up failed import dir failed",
			os.RemoveAll(dir))
		return "", err
	}
	messageCount := 0
	for i, arch := range archives {
		turnMsgs := make([]state.ArchiveMessage, 0, len(arch.Messages))
		for _, m := range arch.Messages {
			turnMsgs = append(turnMsgs, state.ArchiveMessage{
				Role:    string(m.Role),
				Content: m.Content,
			})
		}
		messageCount += len(turnMsgs)
		if err := s.db.CommitConversationTurn(ctx, conv, state.ArchiveTurn{
			RunID:       fmt.Sprintf("import-%d", i),
			At:          arch.At,
			RequestedAt: arch.At,
			StartedAt:   arch.At,
			FinishedAt:  arch.At,
		}, turnMsgs); err != nil {
			telemetry.WarnErr(ctx,
				"sessions: rollback failed import turn failed",
				s.removeLocked(ctx, id))
			return "", err
		}
	}
	conv = state.Conversation{
		ID:           id,
		Title:        title,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
		TurnCount:    len(archives),
		MessageCount: messageCount,
		ImportSource: source,
	}
	if err := s.db.UpsertConversation(ctx, conv); err != nil {
		telemetry.WarnErr(ctx,
			"sessions: rollback failed import metadata failed",
			s.removeLocked(ctx, id))
		return "", err
	}
	s.importPending[id] = source
	return id, nil
}

// CompleteImport marks an imported session as memory-seeded and
// visible in the session list.
func (s *Store) CompleteImport(ctx context.Context, id string) error {
	if err := requireID(id); err != nil {
		return err
	}
	c, err := s.db.Conversation(ctx, id)
	if err != nil {
		return err
	}
	if c.ImportSource == "" {
		return errdefs.Validationf(
			"sessions: session %s is not an import", id)
	}
	if c.ImportReady {
		return nil
	}
	s.mu.Lock()
	delete(s.importPending, id)
	s.mu.Unlock()
	c.ImportReady = true
	c.UpdatedAt = time.Now().UTC()
	return s.db.UpsertConversation(ctx, c)
}

// ImportReady reports whether an imported session has completed its
// memory seed.
func (s *Store) ImportReady(ctx context.Context, id string) (bool, error) {
	if err := requireID(id); err != nil {
		return false, err
	}
	c, err := s.db.Conversation(ctx, id)
	if err == state.ErrNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return c.ImportSource != "" && c.ImportReady, nil
}

// ImportedBySources returns the import-ready conversation id for each
// of the given import sources that exists in this workspace store. It
// powers plugin UIs that want to show "already imported" state next to
// a Codex rollout before the user imports it again.
func (s *Store) ImportedBySources(
	ctx context.Context, sources []string,
) (map[string]string, error) {
	return s.db.ImportReadyBySources(ctx, sources)
}

// AbortImport rolls back an import that failed before CompleteImport.
func (s *Store) AbortImport(ctx context.Context, id string) error {
	return s.Remove(ctx, id)
}

func validateImportRequest(req ImportRequest) error {
	source := strings.TrimSpace(req.Source)
	if source == "" {
		return errdefs.Validationf("sessions: import source is required")
	}
	if len(source) > maxImportSourceBytes {
		return errdefs.Validationf(
			"sessions: import source exceeds %d bytes", maxImportSourceBytes)
	}
	if strings.ContainsRune(source, '\x00') {
		return errdefs.Validationf("sessions: import source must not contain NUL")
	}
	if len(req.Title) > maxImportTitleBytes {
		return errdefs.Validationf(
			"sessions: import title exceeds %d bytes", maxImportTitleBytes)
	}
	if len(req.Turns) == 0 {
		return errdefs.Validationf("sessions: import turns are required")
	}
	if len(req.Turns) > maxImportTurnCount {
		return errdefs.Validationf(
			"sessions: import exceeds %d turns", maxImportTurnCount)
	}
	total := 0
	for i, turn := range req.Turns {
		total += len(turn.Messages)
		if total > maxImportMessageCount {
			return errdefs.Validationf(
				"sessions: import exceeds %d messages", maxImportMessageCount)
		}
		for _, m := range turn.Messages {
			if m.Role == "" && len(m.Content.Parts) == 0 {
				continue
			}
			if m.Role == "" {
				return errdefs.Validationf(
					"sessions: import turn %d has a message without role", i+1)
			}
		}
	}
	if total == 0 {
		return errdefs.Validationf("sessions: import has no messages")
	}
	return nil
}
