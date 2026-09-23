-- Post-turn review suggestions: what a background review thinks is
-- worth remembering, waiting for the user's verdict. Nothing here is
-- applied automatically — accepting a row runs the same write path the
-- remember tool uses, and the row is kept as a record of what was
-- accepted or discarded.
CREATE TABLE IF NOT EXISTS review_suggestions (
    id                  TEXT PRIMARY KEY,
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL DEFAULT '',
    status              TEXT NOT NULL DEFAULT 'pending',
    kind                TEXT NOT NULL,
    payload_json        TEXT NOT NULL,
    reason              TEXT NOT NULL DEFAULT '',
    source_workspace    TEXT NOT NULL DEFAULT '',
    source_conversation TEXT NOT NULL DEFAULT '',
    source_run          TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS review_suggestions_status
    ON review_suggestions(status, created_at);
