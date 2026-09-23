-- User-level long-term memory: stable facts the assistant carries
-- across every workspace and conversation (preferences, environment,
-- conventions). Rows are written only through the settings page, the
-- remember tool (after a user confirmation) or a post-turn review
-- suggestion the user accepted — never silently.
--
-- scope='global' rows apply everywhere, scope='workspace' rows only to
-- the workspace named in `workspace`. dedupe_key is the normalized
-- text: restating a fact updates the row in place instead of growing a
-- twin. `stale` retires a fact without deleting it (the page shows
-- retired facts behind a toggle).
CREATE TABLE IF NOT EXISTS user_memory (
    id                  TEXT PRIMARY KEY,
    kind                TEXT NOT NULL DEFAULT 'fact',
    scope               TEXT NOT NULL,
    workspace           TEXT NOT NULL DEFAULT '',
    text                TEXT NOT NULL,
    dedupe_key          TEXT NOT NULL,
    source_conversation TEXT NOT NULL DEFAULT '',
    source_run          TEXT NOT NULL DEFAULT '',
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL,
    stale               INTEGER NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX IF NOT EXISTS user_memory_dedupe
    ON user_memory(scope, workspace, dedupe_key);

CREATE INDEX IF NOT EXISTS user_memory_scope
    ON user_memory(stale, scope, workspace, updated_at);
