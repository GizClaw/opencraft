#!/usr/bin/env python3
"""perf-baseline summarizes the renderer probe samples a baseline run wrote.

The numbers come from the probe the plan names (frontend/src/lib/perfProbe.ts),
collected by frontend/e2e/baseline.spec.ts: the fixed script runs the app in
Chromium, reports a window per phase, and writes the samples as JSON. This
script is the read side, the way scripts/perf-review.sh is the read side of
the desktop app's metric store — same columns, same question: did a change
make the renderer cheaper, and where.

Usage:
  scripts/perf-baseline.py test-results/baseline-*/perf-baseline-run*.json
  scripts/perf-baseline.py --compare before/ after/

Per phase the table prints one row per run plus the median across runs; the
columns are the ones the plan's §6.1 names, and the unit is in the header.
--compare prints the two sides' medians and the delta, which is what a
before/after review reads; a change smaller than the run-to-run spread is
noise, so the spread is printed too.
"""

import argparse
import glob
import json
import os
import statistics
import sys

# The columns the plan's §6.1 lists, in reading order: commit cost first (the
# flush's own frame), then jank, then interaction, then what the store holds.
COLUMNS = [
    ("window_ms", "window", "ms"),
    ("frames", "frames", "n"),
    ("flush_count", "flushes", "n"),
    ("flush_max", "flush_max", "ms"),
    ("flush_commit_p95", "commit_p95", "ms"),
    ("flush_commit_max", "commit_max", "ms"),
    ("frame_max", "frame_max", "ms"),
    ("dropped_gaps", "gaps", "n"),
    ("long_frames_100", "lf100", "n"),
    ("interaction_max", "inter", "ms"),
    ("dom_nodes", "nodes", "n"),
    ("mounted_rows", "rows", "n"),
    ("store_text_bytes", "text", "B"),
    ("store_media_bytes", "media", "B"),
    ("loaded_convs", "convs", "n"),
    ("dom_images", "images", "n"),
]


def load(paths):
    """load reads the run files and returns {phase: {column: [values per run]}}."""
    runs = []
    for pattern in paths:
        matched = sorted(glob.glob(pattern)) or [pattern]
        for path in matched:
            try:
                with open(path, encoding="utf-8") as fh:
                    runs.append((path, json.load(fh)))
            except (OSError, json.JSONDecodeError) as err:
                sys.exit(f"perf-baseline: cannot read {path}: {err}")
    if not runs:
        sys.exit("perf-baseline: no run files matched")
    phases = {}
    for _, run in runs:
        for phase, samples in run.get("phases", {}).items():
            by_name = {s["name"]: s["value"] for s in samples}
            bucket = phases.setdefault(phase, {})
            for column, _, _ in COLUMNS:
                if column in by_name:
                    bucket.setdefault(column, []).append(by_name[column])
    return runs, phases


def fmt(value):
    if value is None:
        return "-"
    if value >= 1000:
        return f"{value:.0f}"
    return f"{value:.1f}"


def med(values):
    # A series an older build did not report is missing from a window, not
    # zero: printing '-' keeps the column readable without claiming a
    # measurement that never happened.
    return statistics.median(values) if values else None


def print_table(phases):
    header = f"{'phase':<11}" + "".join(
        f"{label:>11}" for _, label, _ in COLUMNS
    )
    print(header)
    print(f"{'':<10}" + "".join(f"{unit:>11}" for _, _, unit in COLUMNS))
    for phase in phases:
        row = f"{phase:<11}" + "".join(
            f"{fmt(med(phases[phase].get(c, []))):>11}" for c, _, _ in COLUMNS
        )
        print(row)
    print()
    print(
        "flush_max, commit_p95 and commit_max are the page's ring (page "
        "lifetime); every other column is the phase's window.",
    )


def print_compare(before, after):
    header = (
        f"{'phase':<11}{'column':<18}{'before':>10}{'after':>10}"
        f"{'delta':>10}{'pct':>8}{'spread':>9}"
    )
    print(header)
    for phase in sorted(set(before) | set(after)):
        for column, label, _ in COLUMNS:
            b = before.get(phase, {}).get(column) or []
            a = after.get(phase, {}).get(column) or []
            bm, am = med(b), med(a)
            if bm is None or am is None:
                continue
            spread = max(max(b) - min(b), max(a) - min(a))
            delta = am - bm
            pct = f"{100 * delta / bm:+.1f}%" if bm else "-"
            # The spread is the honest floor: a delta inside it is noise, and
            # printing it next to the delta is what stops a review reading
            # run-to-run variance as a win.
            mark = "" if delta <= -spread or delta >= spread else " ~"
            print(
                f"{phase:<10}{label:<18}{fmt(bm):>10}{fmt(am):>10}"
                f"{fmt(delta):>10}{pct:>8}{fmt(spread):>8}{mark}"
            )


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("paths", nargs="*", help="run JSON files or globs")
    ap.add_argument(
        "--compare",
        nargs=2,
        metavar=("BEFORE", "AFTER"),
        help="two directories of run files to compare (medians)",
    )
    args = ap.parse_args()

    if args.compare:
        before_dir, after_dir = args.compare
        if not (os.path.isdir(before_dir) and os.path.isdir(after_dir)):
            sys.exit("perf-baseline: --compare takes two directories")
        before_runs, before = load([os.path.join(before_dir, "*.json")])
        after_runs, after = load([os.path.join(after_dir, "*.json")])
        print(f"before: {len(before_runs)} runs from {before_dir}")
        print(f"after:  {len(after_runs)} runs from {after_dir}")
        print()
        print_compare(before, after)
        print()
        print("~ = delta inside the run-to-run spread (noise)")
        return

    runs, phases = load(args.paths or ["test-results/**/perf-baseline-run*.json"])
    print(f"{len(runs)} runs")
    print()
    print_table(phases)


if __name__ == "__main__":
    main()
