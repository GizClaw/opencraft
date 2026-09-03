package state

import (
	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// Migrations returns every workspace-DB migration owned by the
// sessions/state package. Later capabilities register their own
// migrations on the same handle.
func Migrations() []db.Migration {
	return []db.Migration{
		{Version: 1, Name: "threads", Statements: []string{
			`CREATE TABLE IF NOT EXISTS threads (
				id TEXT PRIMARY KEY,
				agent_id TEXT NOT NULL,
				context_id TEXT NOT NULL,
				title TEXT,
				parent_thread_id TEXT,
				status TEXT NOT NULL DEFAULT 'active',
				metadata TEXT NOT NULL DEFAULT '{}',
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_threads_agent ON threads(agent_id, context_id);
			CREATE INDEX IF NOT EXISTS idx_threads_status ON threads(status);`,
		}},
		{Version: 2, Name: "turns", Statements: []string{
			`CREATE TABLE IF NOT EXISTS turns (
				id TEXT PRIMARY KEY,
				thread_id TEXT NOT NULL REFERENCES threads(id),
				run_id TEXT,
				seq INTEGER NOT NULL,
				status TEXT NOT NULL,
				model TEXT,
				started_at TEXT NOT NULL,
				finished_at TEXT,
				metadata TEXT NOT NULL DEFAULT '{}'
			);
			CREATE INDEX IF NOT EXISTS idx_turns_thread ON turns(thread_id, seq);
			CREATE INDEX IF NOT EXISTS idx_turns_run ON turns(run_id);`,
		}},
		{Version: 3, Name: "items", Statements: []string{
			`CREATE TABLE IF NOT EXISTS items (
				id TEXT PRIMARY KEY,
				thread_id TEXT NOT NULL REFERENCES threads(id),
				turn_id TEXT NOT NULL REFERENCES turns(id),
				seq INTEGER NOT NULL,
				item_type TEXT NOT NULL,
				role TEXT,
				payload TEXT NOT NULL,
				created_at TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_items_thread_seq ON items(thread_id, seq);
			CREATE INDEX IF NOT EXISTS idx_items_turn ON items(turn_id);
			CREATE INDEX IF NOT EXISTS idx_items_type ON items(thread_id, item_type);`,
		}},
		{Version: 5, Name: "agent_checkpoints", Statements: []string{
			`CREATE TABLE IF NOT EXISTS agent_checkpoints (
				exec_id  TEXT PRIMARY KEY,
				data     TEXT NOT NULL,
				revision INTEGER NOT NULL DEFAULT 1,
				saved_at TEXT NOT NULL
			);`,
		}},
		{Version: 6, Name: "session_settings", Statements: []string{
			`CREATE TABLE IF NOT EXISTS session_settings (
				context_id TEXT PRIMARY KEY,
				think_level TEXT NOT NULL,
				updated_at TEXT NOT NULL
			);`,
		}},
		{Version: 7, Name: "session_settings_model", Statements: []string{
			`ALTER TABLE session_settings ADD COLUMN model TEXT NOT NULL DEFAULT ''`,
		}},
		{Version: 8, Name: "session_settings_mode", Statements: []string{
			`ALTER TABLE session_settings ADD COLUMN mode TEXT NOT NULL DEFAULT 'workspace'`,
		}},
		{Version: 9, Name: "conversations", Statements: []string{
			`CREATE TABLE IF NOT EXISTS conversations (
				id TEXT PRIMARY KEY,
				title TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				turn_count INTEGER NOT NULL DEFAULT 0,
				message_count INTEGER NOT NULL DEFAULT 0,
				usage_json TEXT NOT NULL DEFAULT '{}',
				import_source TEXT NOT NULL DEFAULT '',
				import_ready INTEGER NOT NULL DEFAULT 0
			)`,
		}},
		{Version: 10, Name: "archive_turns", Statements: []string{
			`CREATE TABLE IF NOT EXISTS archive_turns (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				conversation_id TEXT NOT NULL REFERENCES conversations(id),
				seq INTEGER NOT NULL,
				run_id TEXT,
				at TEXT NOT NULL,
				requested_at TEXT NOT NULL DEFAULT '',
				started_at TEXT NOT NULL DEFAULT '',
				finished_at TEXT NOT NULL DEFAULT '',
				artifacts_json TEXT NOT NULL DEFAULT '[]',
				UNIQUE(conversation_id, seq)
			);
			CREATE UNIQUE INDEX IF NOT EXISTS idx_archive_turns_run
				ON archive_turns(conversation_id, run_id) WHERE run_id IS NOT NULL;
			CREATE INDEX IF NOT EXISTS idx_archive_turns_conv
				ON archive_turns(conversation_id, seq)`,
		}},
		{Version: 11, Name: "archive_messages", Statements: []string{
			`CREATE TABLE IF NOT EXISTS archive_messages (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				conversation_id TEXT NOT NULL,
				turn_id INTEGER NOT NULL,
				seq INTEGER NOT NULL,
				role TEXT NOT NULL,
				content_json TEXT NOT NULL,
				created_at TEXT NOT NULL,
				UNIQUE(conversation_id, seq)
			);
			CREATE INDEX IF NOT EXISTS idx_archive_messages_conv
				ON archive_messages(conversation_id, seq)`,
		}},
		{Version: 12, Name: "conversation_state", Statements: []string{
			`CREATE TABLE IF NOT EXISTS conversation_state (
				conversation_id TEXT NOT NULL,
				name TEXT NOT NULL,
				value_json TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				PRIMARY KEY(conversation_id, name)
			)`,
		}},
		{Version: 14, Name: "drop_legacy_thread_tables", Statements: []string{
			`DROP TABLE IF EXISTS items;
			 DROP TABLE IF EXISTS turns;
			 DROP TABLE IF EXISTS threads`,
		}},
	}
}
