-- Full-text search over archived conversation messages
-- (session_search). The index holds the prompt projection of each
-- message — its text parts plus rendered tool_call / tool_result
-- lines — rendered in Go by the store, never the stored JSON: a
-- snippet must read like the conversation, not like a payload.
--
-- Rows mirror archive_messages one-to-one, keyed by that row's id
-- (INSERT ... rowid = archive_messages.id), so a search joins the
-- conversation, turn and message rows without a second lookup and a
-- conversation delete drops its index rows by conversation_id.
--
-- tokenize = 'trigram' is deliberate. unicode61 tokenizes a run of CJK
-- as one token, so substring recall inside Chinese text — the common
-- case for a workspace like this one — would never match. Trigram
-- indexes every three-character window, so any substring of three or
-- more characters matches, at the price of a scan fallback in the
-- query path for shorter queries (see store.SearchMessages). The
-- index is only as large as the (bounded) indexed text.
--
-- The table is created here, empty; rows for messages written before
-- this migration are backfilled by the Go step recorded as version 17
-- (compat/searchindex.go + the store's BackfillSearchIndex), because
-- rendering the projection is Go's job.
CREATE VIRTUAL TABLE IF NOT EXISTS message_fts USING fts5(
	conversation_id UNINDEXED,
	role UNINDEXED,
	at UNINDEXED,
	text,
	tokenize = 'trigram'
);
