package migrations

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

func TestWorkspaceAndUserMigrationSets(t *testing.T) {
	ctx := context.Background()
	ws, err := db.OpenWithOptions(
		filepath.Join(t.TempDir(), "session.db"),
		db.OpenOptions{ForeignKeys: false})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ws.Close() }()
	if err := Workspace(ctx, ws); err != nil {
		t.Fatal(err)
	}
	if err := Workspace(ctx, ws); err != nil {
		t.Fatalf("workspace migrations must be idempotent: %v", err)
	}

	user, err := db.Open(filepath.Join(t.TempDir(), "user.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = user.Close() }()
	if err := User(ctx, user); err != nil {
		t.Fatal(err)
	}
	if err := User(ctx, user); err != nil {
		t.Fatalf("user migrations must be idempotent: %v", err)
	}
}
