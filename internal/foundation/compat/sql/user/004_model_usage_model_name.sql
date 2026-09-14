-- Usage rows were keyed by "provider/model" strings. Statistics now
-- bucket by model name only, so legacy rows are re-keyed to the
-- portion after the first "/" (old rows always had the provider prefix)
-- and merged when several providers share one model name. The rebuild
-- also adds cache_write_tokens, calls and total_tokens, which the
-- original tables did not store. Legacy rows carry no provider-side
-- billed total, so total_tokens is seeded with input + output (the
-- same estimate the pre-rebuild UI used); new records store the
-- provider-reported TotalTokens directly.
ALTER TABLE model_usage RENAME TO model_usage_legacy;

CREATE TABLE model_usage (
	workspace_id TEXT NOT NULL,
	session_id   TEXT NOT NULL,
	model        TEXT NOT NULL,
	total_tokens INTEGER NOT NULL DEFAULT 0,
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	cache_read_tokens INTEGER NOT NULL DEFAULT 0,
	cache_write_tokens INTEGER NOT NULL DEFAULT 0,
	reasoning_tokens INTEGER NOT NULL DEFAULT 0,
	latency_ms INTEGER NOT NULL DEFAULT 0,
	calls INTEGER NOT NULL DEFAULT 0,
	updated_at TEXT NOT NULL,
	PRIMARY KEY (workspace_id, session_id, model)
);

INSERT INTO model_usage (
	workspace_id, session_id, model, total_tokens,
	input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
	reasoning_tokens, latency_ms, calls, updated_at
)
SELECT
	workspace_id,
	session_id,
	CASE WHEN instr(model, '/') > 0
	     THEN substr(model, instr(model, '/') + 1)
	     ELSE model END,
	SUM(input_tokens + output_tokens),
	SUM(input_tokens),
	SUM(output_tokens),
	SUM(cache_read_tokens),
	0,
	SUM(reasoning_tokens),
	SUM(latency_ms),
	0,
	MAX(updated_at)
FROM model_usage_legacy
GROUP BY workspace_id, session_id,
	CASE WHEN instr(model, '/') > 0
	     THEN substr(model, instr(model, '/') + 1)
	     ELSE model END;

DROP TABLE model_usage_legacy;

ALTER TABLE model_usage_hourly RENAME TO model_usage_hourly_legacy;

CREATE TABLE model_usage_hourly (
	model TEXT NOT NULL,
	hour TEXT NOT NULL,
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	cache_read_tokens INTEGER NOT NULL DEFAULT 0,
	cache_write_tokens INTEGER NOT NULL DEFAULT 0,
	reasoning_tokens INTEGER NOT NULL DEFAULT 0,
	latency_ms INTEGER NOT NULL DEFAULT 0,
	calls INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (model, hour)
);

INSERT INTO model_usage_hourly (
	model, hour,
	input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
	reasoning_tokens, latency_ms, calls
)
SELECT
	CASE WHEN instr(model, '/') > 0
	     THEN substr(model, instr(model, '/') + 1)
	     ELSE model END,
	hour,
	SUM(input_tokens),
	SUM(output_tokens),
	SUM(cache_read_tokens),
	0,
	SUM(reasoning_tokens),
	SUM(latency_ms),
	0
FROM model_usage_hourly_legacy
GROUP BY
	CASE WHEN instr(model, '/') > 0
	     THEN substr(model, instr(model, '/') + 1)
	     ELSE model END,
	hour;

DROP TABLE model_usage_hourly_legacy;
