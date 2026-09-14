CREATE TABLE IF NOT EXISTS agent_checkpoints (
	exec_id  TEXT PRIMARY KEY,
	data     TEXT NOT NULL,
	revision INTEGER NOT NULL DEFAULT 1,
	saved_at TEXT NOT NULL
);
