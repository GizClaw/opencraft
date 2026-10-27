-- The full-text index is rebuilt here to shed two costs at once.
--
-- 1. Its indexed text was bounded at 4000 runes per message. A trigram index
--    costs about three times the text it covers, and 64k messages of tool-heavy
--    conversation turned that into 356 MB of index data for material a search
--    never needs in full: the head of a large tool result already carries the
--    truncation pointer and the excerpt above it, and a message matches on its
--    first sentences or not at all. The bound is 1200 runes now.
--
-- 2. Dropping and recreating the table discards the oversized rows; the Go step
--    recorded as version 29 re-indexes the archive under the new bound (the
--    table is empty, so every row is missing and gets written again).
DROP TABLE IF EXISTS message_fts;

CREATE VIRTUAL TABLE message_fts USING fts5(
	conversation_id UNINDEXED,
	role UNINDEXED,
	at UNINDEXED,
	text,
	tokenize = 'trigram'
);
