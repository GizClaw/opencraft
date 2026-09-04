package automations

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/db"
	"github.com/GizClaw/opencraft/internal/orchestration/migrations"
)

func newUserStore(t *testing.T) (*db.DB, *Store) {
	t.Helper()
	handle, err := db.Open(filepath.Join(t.TempDir(), "user.db"))
	if err != nil {
		t.Fatalf("open user db: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := migrations.User(context.Background(), handle); err != nil {
		t.Fatalf("migrate user db: %v", err)
	}
	store, err := Attach(handle)
	if err != nil {
		t.Fatalf("attach automation store: %v", err)
	}
	return handle, store
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	_, store := newUserStore(t)
	return store
}
