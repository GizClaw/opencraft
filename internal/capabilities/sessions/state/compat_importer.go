package state

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/foundation/compat"
	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// Importer adapts this store to the compatibility layer's workspace
// import port: the migration package decides which legacy sessions exist
// and what they contain, and this store — which owns the rows and the
// transaction boundaries — writes them.
func Importer(handle *db.DB) compat.WorkspaceImporter {
	return workspaceImporter{store: Attach(handle)}
}

type workspaceImporter struct {
	store *Store
}

var _ compat.WorkspaceImporter = workspaceImporter{}

// State reads one per-conversation state document.
func (i workspaceImporter) State(
	ctx context.Context, conversationID, name string,
) ([]byte, bool, error) {
	data, err := i.store.GetConversationState(ctx, conversationID, name)
	switch {
	case err == nil:
		return data, true, nil
	case errors.Is(err, ErrNotFound):
		return nil, false, nil
	default:
		return nil, false, err
	}
}

// SetState writes one per-conversation state document.
func (i workspaceImporter) SetState(
	ctx context.Context, conversationID, name string, data []byte,
) error {
	return mapRetiredError(i.store.SetConversationState(
		ctx, conversationID, name, data))
}

// BackfillSearchIndex indexes the archived messages a database written
// before the full-text index (migration 016) holds.
func (i workspaceImporter) BackfillSearchIndex(ctx context.Context) error {
	return i.store.BackfillSearchIndex(ctx)
}

// ImportConversation writes one legacy session: its row, every archived
// turn with its messages, the per-session state documents, and finally
// the metadata the legacy schema carried (title, usage, import origin).
// The order mirrors the pre-migration importer so an interrupted run
// resumes from the same shape.
func (i workspaceImporter) ImportConversation(
	ctx context.Context, data compat.WorkspaceImport,
) error {
	if err := i.importConversation(ctx, data); err != nil {
		return mapRetiredError(err)
	}
	return nil
}

// mapRetiredError maps this store's retired-id refusal to the
// compatibility layer's own answer, so the walker can skip a session
// the user deleted without depending on the store's error vocabulary.
func mapRetiredError(err error) error {
	if errors.Is(err, ErrRetired) {
		return fmt.Errorf("%w: %w", compat.ErrConversationRetired, err)
	}
	return err
}

// firstImportedTitle is the title for one adopted session: the legacy
// metadata's title when it carried one, otherwise the first line a
// person wrote in the transcript. Legacy metadata often had no title at
// all, and a conversation without one shows up unnamed in the sidebar
// even though its transcript plainly says what it is about. The rule
// itself is FirstTitle's, so an adopted session is named exactly like a
// freshly imported one.
func firstImportedTitle(data compat.WorkspaceImport) string {
	if title := strings.TrimSpace(data.Title); title != "" {
		return title
	}
	for _, turn := range data.Turns {
		msgs := make([]message.Message, 0, len(turn.Messages))
		for _, m := range turn.Messages {
			msgs = append(msgs, message.Message{
				Role:    message.Role(m.Role),
				Content: m.Content,
			})
		}
		if title := FirstTitle(msgs); title != "" {
			return title
		}
	}
	return ""
}

func (i workspaceImporter) importConversation(
	ctx context.Context, data compat.WorkspaceImport,
) error {
	// The legacy metadata's own counts are not seeded: the turns
	// written below are the fact, and CommitConversationTurn counts
	// each one as it writes it. Seeding the row with the metadata's
	// tally counted every turn twice (the meta's number plus the
	// per-turn increment), and the final UpsertConversation re-reads
	// the row, so the archive's own count is what survives.
	conv := Conversation{
		ID:           data.ID,
		Title:        firstImportedTitle(data),
		CreatedAt:    data.CreatedAt,
		UpdatedAt:    data.UpdatedAt,
		UsageJSON:    data.UsageJSON,
		ImportSource: data.ImportSource,
		ImportReady:  data.ImportReady,
	}
	if err := i.store.EnsureConversation(ctx, conv); err != nil {
		return err
	}
	for _, turn := range data.Turns {
		msgs := make([]ArchiveMessage, 0, len(turn.Messages))
		for _, m := range turn.Messages {
			msgs = append(msgs, ArchiveMessage{
				Role:    m.Role,
				Content: m.Content,
			})
		}
		if err := i.store.CommitConversationTurn(ctx, conv, ArchiveTurn{
			RunID:         turn.RunID,
			At:            turn.At,
			RequestedAt:   turn.RequestedAt,
			StartedAt:     turn.StartedAt,
			FinishedAt:    turn.FinishedAt,
			ArtifactsJSON: turn.ArtifactsJSON,
		}, msgs); err != nil {
			return err
		}
	}
	for name, doc := range data.State {
		if err := i.store.SetConversationState(ctx, data.ID, name, doc); err != nil {
			return err
		}
	}
	fresh, err := i.store.Conversation(ctx, data.ID)
	if err != nil {
		return fmt.Errorf("state: read imported conversation: %w", err)
	}
	if data.Title != "" {
		fresh.Title = data.Title
	}
	if len(data.UsageJSON) > 0 && string(data.UsageJSON) != "{}" {
		fresh.UsageJSON = data.UsageJSON
	}
	if data.ImportSource != "" {
		fresh.ImportSource = data.ImportSource
		fresh.ImportReady = data.ImportReady
	}
	return i.store.UpsertConversation(ctx, fresh)
}
