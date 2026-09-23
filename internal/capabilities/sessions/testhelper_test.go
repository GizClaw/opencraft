package sessions

import (
	"context"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions/state"
	"github.com/GizClaw/opencraft/internal/foundation/compat"
)

// newMigratedStore opens a sessions.Store exactly like the workspace
// host does: open the handle, run the centralized workspace migration
// (schema plus legacy JSON import), then use the store.
func newMigratedStore(root string, window int) (*Store, error) {
	store, err := New(root, window)
	if err != nil {
		return nil, err
	}
	if err := compat.Workspace(context.Background(), store.Database(), root, state.Importer(store.Database())); err != nil {
		_ = store.CloseDB()
		return nil, err
	}
	if err := compat.BackfillSearchIndex(
		context.Background(), store.Database(), state.Importer(store.Database()),
	); err != nil {
		_ = store.CloseDB()
		return nil, err
	}
	return store, nil
}
