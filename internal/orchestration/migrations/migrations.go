// Package migrations centralizes which migrations apply to each
// physical SQLite database. Definitions stay with the owning
// capability; execution order and membership live here.
package migrations

import (
	"context"

	"github.com/GizClaw/opencraft/internal/capabilities/automations"
	"github.com/GizClaw/opencraft/internal/capabilities/memory"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions/state"
	"github.com/GizClaw/opencraft/internal/capabilities/usage"
	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// Workspace migrates one workspace session.db with every registered
// owner's schema.
func Workspace(ctx context.Context, handle *db.DB) error {
	migrations := append([]db.Migration(nil), state.Migrations()...)
	migrations = append(migrations, memory.Migrations()...)
	return handle.Migrate(ctx, migrations)
}

// User migrates the user-level user.db shared by usage and
// automations.
func User(ctx context.Context, handle *db.DB) error {
	migrations := append([]db.Migration(nil), usage.Migrations()...)
	migrations = append(migrations, automations.Migrations()...)
	return handle.Migrate(ctx, migrations)
}
