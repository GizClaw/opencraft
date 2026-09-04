CREATE TABLE IF NOT EXISTS turns (
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

CREATE INDEX IF NOT EXISTS idx_turns_thread
	ON turns(thread_id, seq);

CREATE INDEX IF NOT EXISTS idx_turns_run
	ON turns(run_id);
