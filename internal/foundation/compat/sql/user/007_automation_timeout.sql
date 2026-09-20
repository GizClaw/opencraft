-- Per-task run bound for unattended automations ("15m", "2h"; empty
-- means the default). A run that outlives it is cancelled and its
-- record says so, instead of holding a concurrency slot.
ALTER TABLE automations ADD COLUMN timeout TEXT NOT NULL DEFAULT '';
