// Package migrations is the single owner of every OpenCraft SQLite
// schema and legacy-data migration. It defines the complete versioned
// migration sets for workspace session.db and user user.db, executes
// them through foundation/db, and runs the idempotent legacy
// compatibility steps that older app versions need.
package migrations

import (
	"context"
	"fmt"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// Workspace migrates one workspace session.db and then imports legacy
// JSON transcripts found under root. root is the workspace sessions
// directory (the parent of each s-* session folder).
func Workspace(ctx context.Context, handle *db.DB, root string) error {
	if err := WorkspaceSchema(ctx, handle); err != nil {
		return err
	}
	return WorkspaceData(ctx, root, handle)
}

// WorkspaceSchema applies only the versioned workspace schema
// migrations. Workspace normally runs WorkspaceData afterwards.
func WorkspaceSchema(ctx context.Context, handle *db.DB) error {
	if handle == nil {
		return fmt.Errorf("migrations: nil workspace database")
	}
	if err := handle.Migrate(ctx, workspaceMigrations()); err != nil {
		return fmt.Errorf("migrations: workspace schema: %w", err)
	}
	return nil
}

// User migrates the user-level user.db shared by usage and
// automations, then applies user-level legacy compatibility steps.
func User(ctx context.Context, handle *db.DB) error {
	if handle == nil {
		return fmt.Errorf("migrations: nil user database")
	}
	if err := handle.Migrate(ctx, userMigrations()); err != nil {
		return fmt.Errorf("migrations: user schema: %w", err)
	}
	if err := migrateUserLegacy(ctx, handle); err != nil {
		return fmt.Errorf("migrations: user legacy: %w", err)
	}
	return nil
}
