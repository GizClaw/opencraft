package memory

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions/state"
	"github.com/GizClaw/opencraft/internal/foundation/compat"
	"github.com/GizClaw/opencraft/internal/foundation/db"
)

func newMigratedSessions(root string, window int) (*sessions.Store, error) {
	store, err := sessions.New(root, window)
	if err != nil {
		return nil, err
	}
	if err := compat.Workspace(context.Background(), store.Database(), root, state.Importer(store.Database())); err != nil {
		_ = store.CloseDB()
		return nil, err
	}
	if err := compat.BackfillSearchIndex(context.Background(), store.Database(), state.Importer(store.Database())); err != nil {
		_ = store.CloseDB()
		return nil, err
	}
	return store, nil
}

// storedMemoryRow is one legacy memory_items row: exactly the pair (role,
// canonical parts) the pre-transcript read path served the model.
type storedMemoryRow struct {
	Role    message.Role
	Content message.Content
}

// loadStoredMemoryRows reads the legacy memory_items rows of a conversation
// in seq order. Increment A still maintains them as the second copy of the
// conversation; TestProjectionMatchesStoredMemoryRows compares the
// transcript projection against them row by row, and this helper goes away
// with the table in increment B.
func loadStoredMemoryRows(
	t *testing.T, handle *db.DB, conversationID string,
) []storedMemoryRow {
	t.Helper()
	rows, err := handle.SQLDB().QueryContext(context.Background(),
		`SELECT role, payload FROM memory_items WHERE thread_id = ? ORDER BY seq`,
		conversationID)
	if err != nil {
		t.Fatalf("load stored memory rows: %v", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close stored memory rows: %v", err)
		}
	}()
	var out []storedMemoryRow
	for rows.Next() {
		var role, payload string
		if err := rows.Scan(&role, &payload); err != nil {
			t.Fatalf("scan stored memory row: %v", err)
		}
		var content message.Content
		if err := json.Unmarshal([]byte(payload), &content); err != nil {
			t.Fatalf("decode stored memory row: %v", err)
		}
		out = append(out, storedMemoryRow{
			Role:    message.Role(role),
			Content: content,
		})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("stored memory rows: %v", err)
	}
	return out
}
