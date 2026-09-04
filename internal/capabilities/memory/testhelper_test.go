package memory

import (
	"context"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/orchestration/migrations"
)

func newMigratedSessions(root string, window int) (*sessions.Store, error) {
	store, err := sessions.New(root, window)
	if err != nil {
		return nil, err
	}
	if err := migrations.Workspace(context.Background(), store.Database(), root); err != nil {
		_ = store.CloseDB()
		return nil, err
	}
	return store, nil
}
