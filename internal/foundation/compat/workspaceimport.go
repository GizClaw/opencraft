package compat

import (
	"context"
	"time"

	"github.com/GizClaw/flowcraft/core/message"
)

// WorkspaceImport is one pre-SQLite session as the migration reads it:
// the legacy metadata document, the archived turns, and the per-session
// state documents, all already parsed and normalized.
//
// It is deliberately a description of the *legacy* artifact rather than
// a copy of the current session rows: the importer that writes it lives
// with the store, so a schema change there never has to be mirrored
// here, and a change to the old format is a change to this file only.
type WorkspaceImport struct {
	ID           string
	Title        string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	TurnCount    int
	MessageCount int
	UsageJSON    []byte
	ImportSource string
	ImportReady  bool
	Turns        []WorkspaceImportTurn
	// State carries the per-session state documents keyed by name.
	State map[string][]byte
}

// WorkspaceImportTurn is one archived turn of a legacy session.
type WorkspaceImportTurn struct {
	RunID         string
	At            time.Time
	RequestedAt   time.Time
	StartedAt     time.Time
	FinishedAt    time.Time
	ArtifactsJSON []byte
	Messages      []WorkspaceImportMessage
}

// WorkspaceImportMessage is one archived message of a legacy turn.
type WorkspaceImportMessage struct {
	Role    string
	Content message.Content
}

// WorkspaceImporter is the store-side port the workspace migration
// writes through. The store owns its rows and its transaction
// boundaries; this package owns which old shapes exist and when they are
// considered imported.
type WorkspaceImporter interface {
	// State reads one per-conversation state document. found=false means
	// the row does not exist.
	State(
		ctx context.Context, conversationID, name string,
	) (data []byte, found bool, err error)
	// SetState writes one per-conversation state document.
	SetState(
		ctx context.Context, conversationID, name string, data []byte,
	) error
	// ImportConversation writes one legacy session: its row, every turn
	// with its messages, and the per-session state documents, in one
	// transaction.
	ImportConversation(ctx context.Context, data WorkspaceImport) error
	// BackfillSearchIndex indexes every archived message the full-text
	// index (migration 016) does not hold yet. The store owns the rows,
	// the indexed text projection and the transaction boundaries; the
	// migration layer owns when this runs and that it is recorded once.
	BackfillSearchIndex(ctx context.Context) error
}
