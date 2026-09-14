package state

import (
	"context"
	"errors"
	"fmt"

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
	return i.store.SetConversationState(ctx, conversationID, name, data)
}

// ImportConversation writes one legacy session: its row, every archived
// turn with its messages, the per-session state documents, and finally
// the metadata the legacy schema carried (title, usage, import origin).
// The order mirrors the pre-migration importer so an interrupted run
// resumes from the same shape.
func (i workspaceImporter) ImportConversation(
	ctx context.Context, data compat.WorkspaceImport,
) error {
	conv := Conversation{
		ID:           data.ID,
		Title:        data.Title,
		CreatedAt:    data.CreatedAt,
		UpdatedAt:    data.UpdatedAt,
		TurnCount:    data.TurnCount,
		MessageCount: data.MessageCount,
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
