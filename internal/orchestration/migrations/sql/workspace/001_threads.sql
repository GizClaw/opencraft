CREATE TABLE IF NOT EXISTS threads (
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

CREATE INDEX IF NOT EXISTS idx_threads_agent
	ON threads(agent_id, context_id);

CREATE INDEX IF NOT EXISTS idx_threads_status
	ON threads(status);
