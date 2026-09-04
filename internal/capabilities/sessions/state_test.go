package sessions

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
)

// TestStoreOwnsSQLite verifies the session store is the single storage
// owner: it opens session.db at <root>/session.db, exposes the SQLite
// store to memory, and serves agent checkpoints itself.
func TestStoreOwnsSQLite(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	store, err := newMigratedStore(root, 40)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	if _, err := os.Stat(filepath.Join(root, "session.db")); err != nil {
		t.Fatalf("session.db: %v", err)
	}
	if store.State() == nil {
		t.Fatal("State() must return the SQLite store")
	}

	// Checkpoints round-trip through the session store itself.
	ctx := context.Background()
	cp := agent.Checkpoint{
		ExecID:    "run-1",
		Steps:     []string{"s"},
		Iteration: 1,
		Board:     &agent.BoardSnapshot{Vars: map[string]any{"k": "v"}},
		Timestamp: time.Now().UTC(),
	}
	if err := store.Save(ctx, cp); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(ctx, "run-1")
	if err != nil || loaded == nil || loaded.ExecID != "run-1" {
		t.Fatalf("load = %+v, %v", loaded, err)
	}
	if err := store.Delete(ctx, "run-1"); err != nil {
		t.Fatal(err)
	}

	// A fresh store over the same root sees the same SQLite data.
	reopened, err := newMigratedStore(root, 40)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	if _, err := reopened.State().Load(ctx, "run-1"); err != nil {
		t.Fatalf("reopened load: %v", err)
	}
}
