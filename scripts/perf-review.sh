#!/bin/sh
# perf-review reads the renderer probe's samples back out of the local
# metric store (~/.opencraft/user.db, table metric_samples) and prints
# them the way a regression review needs them: one row per sample window,
# a per-route summary, the ground checks that say whether the numbers are
# usable at all, and the slowest windows to look at first.
#
# Why a script: the probe writes a dozen series per report, so answering
# "did rendering get cheaper, and where did the stalls land" means
# pivoting the same window into columns and grouping by the labels — a
# query nobody should rewrite by hand every time. The series semantics it
# relies on (window_ms, dropped_gaps, long_frames_*) are documented in
# docs/turn-latency-hardening-plan.md §项 3.
#
# Usage: scripts/perf-review.sh [hours]     (default 24, 0 = everything)
#   OPENCRAFT_USER_DB=/path/to/user.db overrides the store location.
#
# The store is opened read-only: reviewing must not touch a live app's
# database, and user.db may well be open by the running shell.
set -eu

HOURS="${1:-24}"
case "$HOURS" in
'' | *[!0-9]*)
	echo "usage: $0 [hours]" >&2
	exit 2
	;;
esac

DB="${OPENCRAFT_USER_DB:-$HOME/.opencraft/user.db}"
if [ ! -r "$DB" ]; then
	echo "perf-review: no metric store at $DB (OPENCRAFT_USER_DB overrides)" >&2
	exit 1
fi

# since is the review window's floor in epoch ms (the store's ts unit).
# HOURS=0 means "everything".
if [ "$HOURS" -eq 0 ]; then
	since=0
else
	since=$(($(date +%s) * 1000 - HOURS * 3600000))
fi

# One report carries a batch of samples sharing one timestamp; pivoting
# them gives the per-window view. Only timestamps that have window_ms are
# considered: that series arrived together with the window semantics, so
# its presence separates current-build reports from older rows (which
# measured a different thing and would read as empty windows here).
sqlite3 -header -column "file:$DB?mode=ro" <<SQL
-- A series an older build did not report is missing from that row, not
-- zero: printing '-' keeps the column readable without claiming a
-- measurement that never happened.
.nullvalue '-'
CREATE TEMP VIEW win AS
  SELECT ts, name, value, attrs,
         COALESCE(json_extract(attrs, '\$.route'), '') AS route
  FROM metric_samples
  WHERE name LIKE 'frontend.%'
    AND ts >= MAX((SELECT MIN(ts) FROM metric_samples WHERE name = 'frontend.window_ms'), ${since});

CREATE TEMP VIEW report AS
  SELECT ts,
         MAX(route) AS route,
         MAX(CASE WHEN name = 'frontend.window_ms'       THEN value END) AS win_ms,
         MAX(CASE WHEN name = 'frontend.frames'          THEN value END) AS frames,
         MAX(CASE WHEN name = 'frontend.frame_max'       THEN value END) AS frame_max,
         MAX(CASE WHEN name = 'frontend.long_frames_50'  THEN value END) AS l50,
         MAX(CASE WHEN name = 'frontend.long_frames_100' THEN value END) AS l100,
         MAX(CASE WHEN name = 'frontend.long_frames_200' THEN value END) AS l200,
         MAX(CASE WHEN name = 'frontend.dropped_gaps'    THEN value END) AS dropped,
         MAX(CASE WHEN name = 'frontend.flush_count'     THEN value END) AS flush,
         MAX(CASE WHEN name = 'frontend.flush_commit_p95' THEN value END) AS commit95,
         MAX(CASE WHEN name = 'frontend.flush_md_p95'    THEN value END) AS md95,
         MAX(CASE WHEN name = 'frontend.interaction_max' THEN value END) AS imax,
         MAX(CASE WHEN name = 'frontend.interaction_max'
                  THEN json_extract(attrs, '$.interaction') END) AS who,
         MAX(CASE WHEN name = 'frontend.dom_nodes'       THEN value END) AS dom,
         MAX(CASE WHEN name = 'frontend.mounted_rows'    THEN value END) AS mounted,
         MAX(CASE WHEN name = 'frontend.conv_messages'   THEN value END) AS loaded
  FROM win
  WHERE ts IN (SELECT ts FROM win WHERE name = 'frontend.window_ms')
  GROUP BY ts;

.print '== reports (one row per sample window)'
SELECT datetime(ts / 1000, 'unixepoch', 'localtime') AS t, CASE WHEN route = '' THEN '-' ELSE route END AS route,
       win_ms, frames, frame_max, l50, l100, l200, dropped, flush,
       commit95, md95, imax, COALESCE(who, '-') AS who, dom, mounted, loaded
FROM report ORDER BY ts;

.print
.print '== by route'
SELECT CASE WHEN route = '' THEN '-' ELSE route END AS route, COUNT(*) AS reports,
       ROUND(SUM(win_ms) / 60000.0, 1) AS minutes, SUM(frames) AS frames,
       MAX(frame_max) AS frame_max,
       SUM(l50) AS l50, SUM(l100) AS l100, SUM(l200) AS l200,
       SUM(dropped) AS dropped, SUM(flush) AS flushes,
       MAX(commit95) AS commit95, MAX(md95) AS md95, MAX(imax) AS interaction_max
FROM report GROUP BY route ORDER BY SUM(frames) DESC;

.print
.print '== slowest interactions'
SELECT datetime(ts / 1000, 'unixepoch', 'localtime') AS t, route,
       value AS ms, COALESCE(json_extract(attrs, '$.interaction'), '') AS interaction
FROM win WHERE name = 'frontend.interaction_max' ORDER BY value DESC LIMIT 10;

.print
.print '== checks (frame_max_over_1s and empty_windows should be 0; the rest are informational)'
SELECT (SELECT COUNT(*) FROM report WHERE frame_max > 1000) AS frame_max_over_1s,
       (SELECT COUNT(*) FROM report WHERE frames = 0) AS empty_windows,
       (SELECT COUNT(*) FROM report WHERE dropped > 0) AS suspension_windows,
       (SELECT COUNT(*) FROM report WHERE win_ms > 45000) AS hidden_stretches,
       (SELECT COUNT(*) FROM report) AS reports,
       ROUND((SELECT SUM(win_ms) FROM report) / 60000.0, 1) AS minutes;
SELECT datetime(ts / 1000, 'unixepoch', 'localtime') AS t, route, win_ms, frames, frame_max, dropped
FROM report WHERE frame_max > 1000 OR frames = 0 ORDER BY ts DESC LIMIT 10;

.print
.print '== slowest windows'
SELECT datetime(ts / 1000, 'unixepoch', 'localtime') AS t, route, frame_max, l200, l100, l50, frames, flush, dom, mounted
FROM report ORDER BY frame_max DESC LIMIT 10;
SQL
