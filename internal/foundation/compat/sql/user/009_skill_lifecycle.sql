-- Skill lifecycle: usage events behind the per-skill counters the
-- skills page shows, plus the curated state that must survive a
-- restart. Usage is append-only and pruned by the curator.
--
-- `skill_state` carries the user's decisions (pin / retire). Staleness
-- itself is derived from the usage events and the configured
-- thresholds, so the table only stores what the user decided.
--
-- `skill_archive` records one archived skill directory: the tar.gz
-- snapshot under the data root, the SKILL.md path it came from, and
-- whether it was restored. Archived skills are never deleted by the
-- curator, so a restore is always possible.
CREATE TABLE IF NOT EXISTS skill_usage (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT NOT NULL,
    scope           TEXT NOT NULL DEFAULT '',
    used_at         TEXT NOT NULL,
    run_id          TEXT NOT NULL DEFAULT '',
    conversation_id TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS skill_usage_name
    ON skill_usage(name, used_at);

CREATE TABLE IF NOT EXISTS skill_state (
    name       TEXT PRIMARY KEY,
    scope      TEXT NOT NULL DEFAULT '',
    pinned     INTEGER NOT NULL DEFAULT 0,
    -- Retiring a skill is a user decision, like pinning: the files stay
    -- where they are, the archive record stays restorable, and only the
    -- injected list / $mention activation drop the skill.
    retired    INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS skill_archive (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    scope        TEXT NOT NULL DEFAULT '',
    skill_path   TEXT NOT NULL,
    archive_path TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    restored_at  TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS skill_archive_name
    ON skill_archive(name, created_at);
