package migrations

import (
	"embed"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

//go:embed sql/workspace/*.sql
var workspaceSQL embed.FS

// workspaceMigrations discovers every workspace SQL file. SQL lives
// in sql/workspace so it can be reviewed and edited without touching
// Go code. Numeric prefixes preserve the migration versions already
// recorded by official v0.1.0 databases; WorkspaceData still imports
// the pre-SQLite JSON archives from those databases.
func workspaceMigrations() []db.Migration {
	return discoverMigrations(workspaceSQL, "sql/workspace")
}
