-- The prompt form of a row is computed once, at write time, instead of on every
-- read: the replay used to decode content_json, clone the message and re-render
-- its tool activity for every row, every time the transcript was projected.
--
-- prompt_state is '' for rows written before this migration; the reader then
-- projects them the old way (and a one-time backfill fills them in), so an
-- upgraded workspace is never left with rows that read as "nothing to show".
ALTER TABLE archive_messages ADD COLUMN prompt_json TEXT NOT NULL DEFAULT '';
ALTER TABLE archive_messages ADD COLUMN prompt_state TEXT NOT NULL DEFAULT '';
