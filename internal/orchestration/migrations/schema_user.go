package migrations

import (
	"embed"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

//go:embed sql/user/*.sql
var userSQL embed.FS

// userMigrations discovers every user-database SQL file. SQL lives in
// sql/user; usage and automations share one sequential migration list.
func userMigrations() []db.Migration {
	return discoverMigrations(userSQL, "sql/user")
}
