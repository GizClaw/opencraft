CREATE TABLE IF NOT EXISTS items (
	id TEXT PRIMARY KEY,
	thread_id TEXT NOT NULL REFERENCES threads(id),
	turn_id TEXT NOT NULL REFERENCES turns(id),
	seq INTEGER NOT NULL,
	item_type TEXT NOT NULL,
	role TEXT,
	payload TEXT NOT NULL,
	created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_items_thread_seq
	ON items(thread_id, seq);

CREATE INDEX IF NOT EXISTS idx_items_turn
	ON items(turn_id);

CREATE INDEX IF NOT EXISTS idx_items_type
	ON items(thread_id, item_type);
