package automations

import "github.com/GizClaw/opencraft/internal/foundation/db"

// Migrations owns the automation tables on the shared user database.
func Migrations() []db.Migration {
	return []db.Migration{
		{Version: 1, Name: "automations", Statements: []string{
			`CREATE TABLE IF NOT EXISTS automations (
				id          TEXT PRIMARY KEY,
				name        TEXT NOT NULL,
				prompt      TEXT NOT NULL,
				schedule    TEXT NOT NULL,
				workspace   TEXT NOT NULL,
				mode        TEXT NOT NULL DEFAULT 'workspace',
				model       TEXT NOT NULL DEFAULT '',
				think       TEXT NOT NULL DEFAULT '',
				conversation_id TEXT NOT NULL DEFAULT '',
				notify      TEXT NOT NULL DEFAULT 'always',
				enabled     INTEGER NOT NULL DEFAULT 1,
				created_at  TEXT NOT NULL,
				updated_at  TEXT NOT NULL,
				last_run_at TEXT NOT NULL DEFAULT '',
				last_status TEXT NOT NULL DEFAULT '',
				next_run_at TEXT NOT NULL DEFAULT ''
			)`,
		}},
		{Version: 2, Name: "automation_runs", Statements: []string{
			`CREATE TABLE IF NOT EXISTS automation_runs (
				id              TEXT PRIMARY KEY,
				task_id         TEXT NOT NULL,
				at              TEXT NOT NULL,
				status          TEXT NOT NULL,
				error           TEXT NOT NULL DEFAULT '',
				conversation_id TEXT NOT NULL DEFAULT '',
				run_id          TEXT NOT NULL DEFAULT '',
				duration_ms     INTEGER NOT NULL DEFAULT 0,
				summary         TEXT NOT NULL DEFAULT ''
			);
			CREATE INDEX IF NOT EXISTS idx_automation_runs_task
				ON automation_runs(task_id, at DESC)`,
		}},
	}
}
