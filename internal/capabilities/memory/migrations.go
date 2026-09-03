package memory

import "github.com/GizClaw/opencraft/internal/foundation/db"

// Migrations owns the memory tables on the shared workspace DB.
// Versions start above the sessions/state migration range so both
// owners can register on the same schema_migrations table.
func Migrations() []db.Migration {
	return []db.Migration{
		{Version: 1001, Name: "memory_items", Statements: []string{
			`CREATE TABLE IF NOT EXISTS memory_items (
				id TEXT PRIMARY KEY,
				thread_id TEXT NOT NULL,
				turn_id TEXT NOT NULL,
				seq INTEGER NOT NULL,
				item_type TEXT NOT NULL,
				role TEXT,
				payload TEXT NOT NULL,
				created_at TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_memory_items_thread_seq
				ON memory_items(thread_id, seq)`,
		}},
		{Version: 1002, Name: "summary_nodes", Statements: []string{
			`CREATE TABLE IF NOT EXISTS summary_nodes (
				id TEXT PRIMARY KEY,
				thread_id TEXT NOT NULL,
				level INTEGER NOT NULL DEFAULT 0,
				parent_ids TEXT NOT NULL DEFAULT '[]',
				source_ids TEXT NOT NULL DEFAULT '[]',
				summary TEXT NOT NULL,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				metadata TEXT NOT NULL DEFAULT '{}'
			);
			CREATE INDEX IF NOT EXISTS idx_summary_thread_level
				ON summary_nodes(thread_id, level)`,
		}},
	}
}
