// Package sessionstore opens migrated workspace session stores for
// tests. It exists so capability and adapter tests use the same
// centralized migration entry point as production instead of duplicating
// schema setup.
package sessionstore

import (
	"context"
	"testing"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/orchestration/migrations"
)

// Open opens one sessions.Store at root and runs the centralized
// workspace migration (schema plus legacy JSON import). The returned
// store is closed automatically when the test finishes.
func Open(t *testing.T, root string, window int) (*sessions.Store, error) {
	t.Helper()
	store, err := sessions.New(root, window)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() {
		if err := store.CloseDB(); err != nil {
			t.Errorf("sessionstore: close store: %v", err)
		}
	})
	if err := migrations.Workspace(context.Background(), store.Database(), root); err != nil {
		return nil, err
	}
	return store, nil
}
