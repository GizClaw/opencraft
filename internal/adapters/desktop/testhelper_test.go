package desktop

import (
	"context"
	"path/filepath"
	"testing"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/db"
	"github.com/GizClaw/opencraft/internal/orchestration/migrations"
)

func openMigratedSessions(t *testing.T, root string, window int) (*ocsessions.Store, error) {
	t.Helper()
	store, err := ocsessions.New(root, window)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := migrations.Workspace(context.Background(), store.Database(), root); err != nil {
		return nil, err
	}
	return store, nil
}

func openUserDB(t *testing.T) *db.DB {
	t.Helper()
	handle, err := db.Open(filepath.Join(t.TempDir(), "user.db"))
	if err != nil {
		t.Fatalf("open user db: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := migrations.User(context.Background(), handle); err != nil {
		t.Fatalf("migrate user db: %v", err)
	}
	return handle
}
