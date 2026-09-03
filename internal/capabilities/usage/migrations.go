package usage

import "github.com/GizClaw/opencraft/internal/foundation/db"

// Migrations owns the usage tables on the shared user database.
func Migrations() []db.Migration {
	return []db.Migration{
		{Version: 1, Name: "model_usage", Statements: []string{
			`CREATE TABLE IF NOT EXISTS model_usage (
				workspace_id TEXT NOT NULL,
				session_id   TEXT NOT NULL,
				model        TEXT NOT NULL,
				input_tokens INTEGER NOT NULL DEFAULT 0,
				output_tokens INTEGER NOT NULL DEFAULT 0,
				cache_read_tokens INTEGER NOT NULL DEFAULT 0,
				reasoning_tokens INTEGER NOT NULL DEFAULT 0,
				latency_ms INTEGER NOT NULL DEFAULT 0,
				updated_at TEXT NOT NULL,
				PRIMARY KEY (workspace_id, session_id, model)
			)`,
		}},
		{Version: 2, Name: "model_usage_hourly", Statements: []string{
			`CREATE TABLE IF NOT EXISTS model_usage_hourly (
				model        TEXT NOT NULL,
				hour         TEXT NOT NULL,
				input_tokens INTEGER NOT NULL DEFAULT 0,
				output_tokens INTEGER NOT NULL DEFAULT 0,
				cache_read_tokens INTEGER NOT NULL DEFAULT 0,
				reasoning_tokens INTEGER NOT NULL DEFAULT 0,
				latency_ms INTEGER NOT NULL DEFAULT 0,
				PRIMARY KEY (model, hour)
			)`,
		}},
	}
}
