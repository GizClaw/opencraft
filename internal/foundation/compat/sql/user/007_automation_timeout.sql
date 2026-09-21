-- Per-task run bound for unattended automations ("15m", "2h"; empty
-- means the default). A run that outlives it is cancelled and its
-- record says so, instead of holding a concurrency slot.
ALTER TABLE automations ADD COLUMN timeout TEXT NOT NULL DEFAULT '';

-- Rows saved before the column existed were not unbounded: they ran
-- under the assistant graph's policy.run_timeout (one hour in the
-- seeded graph), which is the only bound they ever promised. Seed them
-- with that bound so an upgrade does not shorten a run nobody asked to
-- shorten; the 15-minute default stays the answer for tasks saved
-- without one. The value is the shipped graph's derived bound, the best
-- available reading of "what this row was allowed to do".
UPDATE automations SET timeout = '60m' WHERE timeout = '';
