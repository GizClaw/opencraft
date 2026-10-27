-- The bounded summary tree is gone. Folding belongs to the graph's compact
-- tool now, and the durable fold records the range it covered in
-- conversation_state (compact_fold), so nothing reads summary_nodes: a second
-- history with no reader is exactly what tempts a future writer, so the table
-- is dropped rather than left empty.
--
-- Irreversible by design: the content was derived from archive rows, and those
-- rows are still here.
DROP TABLE IF EXISTS summary_nodes;
