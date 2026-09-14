CREATE TABLE IF NOT EXISTS conversations (
	id TEXT PRIMARY KEY,
	title TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	turn_count INTEGER NOT NULL DEFAULT 0,
	message_count INTEGER NOT NULL DEFAULT 0,
	usage_json TEXT NOT NULL DEFAULT '{}',
	import_source TEXT NOT NULL DEFAULT '',
	import_ready INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS archive_turns (
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
	ON archive_turns(conversation_id, seq);

CREATE TABLE IF NOT EXISTS archive_messages (
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
	ON archive_messages(conversation_id, seq);

CREATE TABLE IF NOT EXISTS conversation_state (
	conversation_id TEXT NOT NULL,
	name TEXT NOT NULL,
	value_json TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY(conversation_id, name)
);

CREATE TABLE IF NOT EXISTS memory_items (
	id TEXT PRIMARY KEY,
	thread_id TEXT NOT NULL,
	turn_id TEXT NOT NULL,
	seq INTEGER NOT NULL,
	item_type TEXT NOT NULL,
	role TEXT,
	payload TEXT NOT NULL,
	created_at TEXT NOT NULL
);

INSERT OR IGNORE INTO memory_items (
	id, thread_id, turn_id, seq, item_type, role, payload, created_at
)
SELECT id, thread_id, turn_id, seq, item_type, role, payload, created_at
FROM items;

CREATE INDEX IF NOT EXISTS idx_memory_items_thread_seq
	ON memory_items(thread_id, seq);

DROP TABLE IF EXISTS items;
DROP TABLE IF EXISTS turns;
DROP TABLE IF EXISTS threads;
