-- Turn-scoped message reads: ArchiveTurnByRun (every turn_end, every
-- recovery pass) and the paged reload of one turn both filter
-- archive_messages by turn_id. idx_archive_messages_conv cannot serve
-- that: its leading column is the conversation, so the lookup would
-- scan every message the conversation ever wrote. (turn_id, seq)
-- matches the reader, which orders by seq inside the one turn.
CREATE INDEX IF NOT EXISTS idx_archive_messages_turn
	ON archive_messages(turn_id, seq);
