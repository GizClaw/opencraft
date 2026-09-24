#!/bin/sh
# mem-review joins the two halves of a desktop memory investigation out of
# the local metric store (~/.opencraft/user.db, table metric_samples):
#
#   proc.mem.family_total / proc.mem.footprint   what the app costs the
#     machine, one sample a minute, from the Go host. The platform's
#     renderers (WKWebView's on macOS, and the media services around it)
#     are most of the number and are invisible to the Go heap.
#   frontend.store_media_bytes / store_text_bytes / loaded_convs /
#   dom_images / conv_messages / mounted_rows    the renderer's own
#     account of what it holds, reported by the probe every 30s while it
#     is switched on (Diagnostics > DEV tools).
#
# A footprint that climbs while the store series stay flat is not the
# transcript: it is the renderer's own retention (streaming churn, layout
# and decoded-image caches), which is what the probe's frame and flush
# series are for. A footprint that climbs with store_media_bytes is the
# payload — inline screenshots, view_image frames — and the store's
# per-conversation budgets are where that is bounded.
#
# The numbers are per-minute maxima (the renderers are sampled at one
# instant and several of them share a role), so read the shape, not the
# second decimal.
#
# Usage: scripts/mem-review.sh [hours]     (default 6, 0 = everything)
#   OPENCRAFT_USER_DB=/path/to/user.db overrides the store location.
#
# The store is opened read-only: reviewing must not touch a live app's
# database, and user.db may well be open by the running shell.
set -eu

HOURS="${1:-6}"
case "$HOURS" in
'' | *[!0-9]*)
	echo "usage: $0 [hours]" >&2
	exit 2
	;;
esac

DB="${OPENCRAFT_USER_DB:-$HOME/.opencraft/user.db}"
if [ ! -r "$DB" ]; then
	echo "mem-review: no metric store at $DB (OPENCRAFT_USER_DB overrides)" >&2
	exit 1
fi

# since is the review window's floor in epoch ms (the store's ts unit).
# HOURS=0 means "everything".
if [ "$HOURS" -eq 0 ]; then
	since=0
else
	since=$(($(date +%s) * 1000 - HOURS * 3600000))
fi

sqlite3 -header -column "file:$DB?mode=ro" <<SQL
-- A series a build does not report is missing from that row, not zero:
-- printing '-' keeps the column readable without claiming a measurement
-- that never happened.
.nullvalue '-'
CREATE TEMP VIEW win AS
  SELECT ts, name, value, attrs
  FROM metric_samples
  WHERE ts >= ${since}
    AND (name IN ('proc.mem.family_total', 'proc.mem.footprint')
      OR name LIKE 'frontend.%');

-- One row per minute: the family total, the renderers, the Go process,
-- and the store's own gauges. The renderers share one role from outside,
-- so the window list is reported as a sum, a maximum and a count: the
-- maximum is the window being read (the pet is a fraction of it), the sum
-- is what the platform charges for both.
CREATE TEMP VIEW per_min AS
  SELECT strftime('%Y-%m-%d %H:%M', ts / 1000, 'unixepoch', 'localtime') AS m,
         ROUND(MAX(CASE WHEN name = 'proc.mem.family_total' THEN value END) / 1048576.0, 1) AS fam_mb,
         COUNT(DISTINCT CASE WHEN name = 'proc.mem.footprint'
                              AND json_extract(attrs, '\$.role') = 'webcontent'
                             THEN json_extract(attrs, '\$.pid') END) AS web_n,
         ROUND(SUM(CASE WHEN name = 'proc.mem.footprint'
                         AND json_extract(attrs, '\$.role') = 'webcontent'
                        THEN value END) / 1048576.0, 1) AS web_sum_mb,
         ROUND(MAX(CASE WHEN name = 'proc.mem.footprint'
                         AND json_extract(attrs, '\$.role') = 'webcontent'
                        THEN value END) / 1048576.0, 1) AS web_mb,
         ROUND(SUM(CASE WHEN name = 'proc.mem.footprint'
                         AND json_extract(attrs, '\$.role') = 'child'
                        THEN value END) / 1048576.0, 1) AS child_sum_mb,
         ROUND(SUM(CASE WHEN name = 'proc.mem.footprint'
                         AND json_extract(attrs, '\$.role') = 'self'
                        THEN value END) / 1048576.0, 1) AS self_mb,
         ROUND(MAX(CASE WHEN name = 'frontend.store_media_bytes' THEN value END) / 1048576.0, 1) AS media_mb,
         ROUND(MAX(CASE WHEN name = 'frontend.store_text_bytes' THEN value END) / 1048576.0, 1) AS text_mb,
         MAX(CASE WHEN name = 'frontend.loaded_convs' THEN value END) AS convs,
         MAX(CASE WHEN name = 'frontend.dom_images' THEN value END) AS imgs,
         MAX(CASE WHEN name = 'frontend.conv_messages' THEN value END) AS msgs,
         MAX(CASE WHEN name = 'frontend.mounted_rows' THEN value END) AS mounted
  FROM win
  WHERE name IN ('proc.mem.family_total', 'proc.mem.footprint')
     OR name IN ('frontend.store_media_bytes', 'frontend.store_text_bytes',
                 'frontend.loaded_convs', 'frontend.dom_images',
                 'frontend.conv_messages', 'frontend.mounted_rows')
  GROUP BY m;

.print '== per minute (MB; web_mb is the biggest renderer, web_n how many, child_sum_mb every sandbox child together)'
SELECT m AS minute, fam_mb, web_n, web_sum_mb, web_mb, self_mb, child_sum_mb,
       media_mb, text_mb, convs, imgs, msgs, mounted
FROM per_min ORDER BY m;

.print
.print '== per hour (the family total, the renderers, and the store gauges)'
SELECT substr(m, 1, 13) AS hour,
       ROUND(MIN(fam_mb), 1) AS fam_min, ROUND(AVG(fam_mb), 1) AS fam_avg,
       ROUND(MAX(fam_mb), 1) AS fam_max,
       ROUND(AVG(web_mb), 1) AS web_avg, ROUND(MAX(web_sum_mb), 1) AS web_sum_max,
       ROUND(MAX(media_mb), 1) AS media_max, ROUND(MAX(text_mb), 1) AS text_max,
       MAX(convs) AS convs, MAX(imgs) AS imgs, MAX(msgs) AS msgs
FROM per_min WHERE fam_mb IS NOT NULL GROUP BY hour ORDER BY hour;

.print
.print '== first and last sample of the window'
SELECT (SELECT fam_mb FROM per_min WHERE fam_mb IS NOT NULL ORDER BY m LIMIT 1) AS fam_first_mb,
       (SELECT fam_mb FROM per_min WHERE fam_mb IS NOT NULL ORDER BY m DESC LIMIT 1) AS fam_last_mb,
       (SELECT web_mb FROM per_min WHERE web_mb IS NOT NULL ORDER BY m LIMIT 1) AS web_first_mb,
       (SELECT web_mb FROM per_min WHERE web_mb IS NOT NULL ORDER BY m DESC LIMIT 1) AS web_last_mb,
       (SELECT COUNT(*) FROM per_min WHERE fam_mb IS NOT NULL) AS minutes,
       (SELECT MIN(m) FROM per_min) AS from_minute, (SELECT MAX(m) FROM per_min) AS to_minute;

.print
.print '== checks (store_gauge_minutes 0 = this build does not report the store series yet)'
SELECT (SELECT COUNT(*) FROM per_min WHERE media_mb IS NOT NULL) AS store_gauge_minutes,
       (SELECT COUNT(*) FROM per_min WHERE fam_mb IS NOT NULL) AS footprint_minutes,
       (SELECT ROUND(MAX(fam_mb - prev), 1) FROM (
          SELECT fam_mb, LAG(fam_mb) OVER (ORDER BY m) AS prev FROM per_min
        ) WHERE prev IS NOT NULL) AS biggest_jump_mb,
       (SELECT COUNT(*) FROM per_min WHERE web_n > 1) AS minutes_with_two_renderers;

.print
.print '== biggest single-minute jumps'
SELECT m AS minute, prev AS from_mb, fam_mb AS to_mb, ROUND(fam_mb - prev, 1) AS delta_mb,
       media_mb, text_mb, msgs
FROM (SELECT fam_mb, media_mb, text_mb, msgs, m,
             LAG(fam_mb) OVER (ORDER BY m) AS prev FROM per_min)
WHERE prev IS NOT NULL ORDER BY (fam_mb - prev) DESC LIMIT 10;
SQL
