CREATE TABLE IF NOT EXISTS model_usage_hourly (
	model        TEXT NOT NULL,
	hour         TEXT NOT NULL,
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	cache_read_tokens INTEGER NOT NULL DEFAULT 0,
	reasoning_tokens INTEGER NOT NULL DEFAULT 0,
	latency_ms INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (model, hour)
);
