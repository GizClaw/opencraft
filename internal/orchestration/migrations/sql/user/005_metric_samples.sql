-- Local metric time series shown in the desktop Diagnostics panel.
-- Values are plain REAL samples keyed by metric name and wall-clock
-- timestamp; attrs carries a stable JSON object (e.g. {"status":"completed"})
-- so the UI can split one metric into series without extra tables.
CREATE TABLE metric_samples (
    id    INTEGER PRIMARY KEY AUTOINCREMENT,
    name  TEXT NOT NULL,
    ts    INTEGER NOT NULL,
    value REAL NOT NULL,
    attrs TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX metric_samples_name_ts ON metric_samples(name, ts);
