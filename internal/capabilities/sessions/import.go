package sessions

import (
	"context"
	"encoding/json"
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
	Title  string `json:"title"`
	Source string `json:"source"`
	// Usage is the optional cumulative token accounting recorded by the
	// source client (e.g. Codex token_count totals). Import keeps the
	// bundle write path usage-agnostic: Store.Import never writes it,
	// and the Host persists it once per fresh import so live-turn
	// AddUsage semantics stay the single write path.
	Usage *Usage       `json:"usage,omitempty"`
	Turns []ImportTurn `json:"turns"`
}

// ImportTurn is one archived turn of an imported conversation.
type ImportTurn struct {
	At time.Time `json:"at"`
	// RequestedAt/StartedAt/FinishedAt are optional turn timestamps
	// exported by OpenCraft or recorded by a source client. Each one
	// falls back to At when absent, so legacy and plugin-written
	// bundles (which only carry At and optionally FinishedAt) import
	// exactly as before while OpenCraft bundles round-trip losslessly.
	RequestedAt *time.Time        `json:"requested_at,omitempty"`
	StartedAt   *time.Time        `json:"started_at,omitempty"`
	FinishedAt  *time.Time        `json:"finished_at,omitempty"`
	Messages    []message.Message `json:"messages"`
	// Kind and Payload carry a turn the app itself wrote (a delegation
	// note): round-tripping them keeps the transcript card and keeps
	// the note out of title derivation here, exactly as in a store the
	// note was written to directly. Bundles from before this existed
	// carry neither, and a bundle naming a kind this build does not
	// know imports it untouched — the fields are the writer's, not the
	// importer's.
	Kind    string          `json:"kind,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Import writes a new conversation from a neutral request into SQLite
// and returns the generated s-xxx id. The same Source maps to the same
// session id.
//
// The call is the whole import: the transcript it writes is the
// conversation, so the conversation is ready — visible in the list,
// eligible for "already imported" — as soon as it returns. A failure
// leaves the row unready and therefore invisible (List skips it), and
// the next import of the same source replaces it.
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
		if err := s.removeLocked(ctx, existing.ID); err != nil {
			return "", err
		}
	}

	var archives []struct {
		At        time.Time
		Requested time.Time
		Started   time.Time
		Finished  time.Time
		Messages  []message.Message
		Kind      string
		Payload   []byte
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
		} else {
			at = at.UTC()
		}
		requested := at
		started := at
		finished := at
		if turn.RequestedAt != nil && !turn.RequestedAt.IsZero() {
			requested = turn.RequestedAt.UTC()
		}
		if turn.StartedAt != nil && !turn.StartedAt.IsZero() {
			started = turn.StartedAt.UTC()
		}
		if turn.FinishedAt != nil && !turn.FinishedAt.IsZero() {
			finished = turn.FinishedAt.UTC()
		}
		archives = append(archives, struct {
			At        time.Time
			Requested time.Time
			Started   time.Time
			Finished  time.Time
			Messages  []message.Message
			Kind      string
			Payload   []byte
		}{At: at, Requested: requested, Started: started,
			Finished: finished, Messages: msgs,
			Kind: turn.Kind, Payload: turn.Payload})
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
			// A turn the app wrote — a delegation note — says nothing
			// about what the imported conversation is, so the title
			// falls to the first turn a person wrote.
			if arch.Kind != "" {
				continue
			}
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
			RequestedAt: arch.Requested,
			StartedAt:   arch.Started,
			FinishedAt:  arch.Finished,
			Kind:        arch.Kind,
			PayloadJSON: arch.Payload,
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
		ImportReady:  true,
	}
	if err := s.db.UpsertConversation(ctx, conv); err != nil {
		telemetry.WarnErr(ctx,
			"sessions: rollback failed import metadata failed",
			s.removeLocked(ctx, id))
		return "", err
	}
	return id, nil
}

// ImportReady reports whether an imported session is complete: its
// transcript was written, so it is listed and counts as already
// imported for its source.
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
