package state_test

import (
	"context"
	"testing"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions/state"
	"github.com/GizClaw/opencraft/internal/orchestration/migrations"
)

func openState(t *testing.T, path string) *state.Store {
	t.Helper()
	s, err := state.Open(path)
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := migrations.WorkspaceSchema(context.Background(), s.Handle()); err != nil {
		t.Fatalf("migrate state: %v", err)
	}
	return s
}
