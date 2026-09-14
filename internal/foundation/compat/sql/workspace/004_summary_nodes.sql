CREATE TABLE IF NOT EXISTS summary_nodes (
	id TEXT PRIMARY KEY,
	thread_id TEXT NOT NULL REFERENCES threads(id),
	level INTEGER NOT NULL DEFAULT 0,
	parent_ids TEXT NOT NULL DEFAULT '[]',
	source_ids TEXT NOT NULL DEFAULT '[]',
	summary TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	metadata TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_summary_thread_level
	ON summary_nodes(thread_id, level);
