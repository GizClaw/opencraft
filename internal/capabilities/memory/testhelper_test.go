package memory

import (
	"context"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions/state"
	"github.com/GizClaw/opencraft/internal/foundation/compat"
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
