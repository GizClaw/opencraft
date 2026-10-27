-- The append-only half of a run's crash-recovery checkpoint: one row per
-- completed wave, applied on top of the run's base record. Before this,
-- every wave rewrote the whole board (O(history) per boundary), which is
-- what made a long turn's checkpoint bytes grow without bound.
--
-- A run's record is `agent_checkpoints` (the base) plus these in `seq`
-- order; agent.CheckpointDelta.ApplyTo folds each one onto the base. A
-- gap in the chain is corruption, not a resumable state, so a load that
-- finds one reports it as unavailable and the host starts a fresh run.
CREATE TABLE IF NOT EXISTS agent_checkpoint_deltas (
	exec_id  TEXT NOT NULL,
	seq      INTEGER NOT NULL,
	data     TEXT NOT NULL,
	saved_at TEXT NOT NULL,
	PRIMARY KEY (exec_id, seq)
);
