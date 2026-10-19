#!/usr/bin/env python3
"""rounds-review reports where a turn's model rounds go, out of the traces
the app already writes.

A "round" is one model invocation: the engine reports them as
model_usage.calls, and every additional round is a full-context request
whose only product may be one more tool call. The comparison that matters
is rounds per turn against another harness doing the same work, so the
number has to come from a stable definition and be re-measurable after
every change to the tool surface or the prompt.

Two sources, because neither answers alone:

  * user.db model_usage (calls, tokens) joined with each workspace's
    session.db conversations.turn_count - the authoritative rounds/turn.
    It survives rollout rotation, which the raw JSONL does not.
  * sessions/<conversation>/rollout.jsonl - the per-round shape: which
    tools the rounds went to, how big the results were and how often they
    were excerpted, whether reads came in windows, how often a file was
    re-read after an edit, how much of exec is shell plumbing.

The rollout is append-only JSONL of thread/turn/item events
(internal/capabilities/rollout); a turn is turn.started .. turn.completed
or turn.failed. Text results are not retained here - only their length and
three flags - so a corpus of a few hundred MB stays cheap to scan.

Usage: scripts/rounds-review.py [--since YYYY-MM-DD] [--until YYYY-MM-DD]
                               [--home ~/.opencraft] [--json]

  --since/--until filter turns by their start time. They apply to the
  rollout half only: session.db and user.db hold conversation totals
  without per-turn timestamps, so rounds/turn is always all-time for the
  conversations that exist. Run the script on a corpus that predates a
  change, then again after it, and compare the two reports.

Both stores are opened read-only: reviewing must not touch a live app's
databases, and they may well be open by the running shell.
"""

import argparse
import collections
import glob
import json
import os
import sqlite3
import sys
from datetime import datetime, timezone

# MarkerPrefix of core/tool/middleware's truncate: a result past the cap
# keeps its head and tail and carries this pointer to the full output in
# the tool cache. Its presence is how a report counts "excerpted".
EXCERPT_MARKER = "\u2026[truncated; full output: "

# Result-size bands are the knobs the cap has had and the one below it:
# 8k was the cap before the 2026-10 change, 24k is the cap now, and 32k
# is result_limit's hard bound. A pile of results sitting exactly on a
# band edge is the model paying for the cap in extra rounds.
SIZE_BANDS = (
    (0, 2_000, "<2k"),
    (2_000, 6_000, "2-6k"),
    (6_000, 8_000, "6-8k"),
    (8_000, 24_000, "8-24k"),
    (24_000, 32_000, "24-32k"),
    (32_000, 1 << 62, ">=32k"),
)

# A re-read counts when it happens within this many calls of the edit: a
# read 30 calls later is the model working again, not the edit failing to
# hand its context back.
REREAD_WINDOW = 4


def size_band(n):
    for lo, hi, name in SIZE_BANDS:
        if lo <= n < hi:
            return name
    return "?"


def parse_when(s, end_of_day):
    """parse_when accepts YYYY-MM-DD (local day) or RFC3339."""
    s = s.strip()
    try:
        if len(s) == 10:
            d = datetime.strptime(s, "%Y-%m-%d").replace(tzinfo=timezone.utc)
            if end_of_day:
                d = d.replace(hour=23, minute=59, second=59)
            return d
        return datetime.fromisoformat(s.replace("Z", "+00:00"))
    except ValueError:
        raise SystemExit(f"rounds-review: bad date {s!r} (want YYYY-MM-DD or RFC3339)")


def event_time(s):
    if not s:
        return None
    try:
        return datetime.fromisoformat(s.replace("Z", "+00:00"))
    except ValueError:
        return None


def patch_files(text):
    """patch_files pulls the paths out of an apply_patch body."""
    out = set()
    if not isinstance(text, str):
        return out
    for marker in ("*** Update File: ", "*** Add File: ", "*** Delete File: "):
        for chunk in text.split(marker)[1:]:
            name = chunk.split("\n", 1)[0].strip()
            if name:
                out.add(os.path.basename(name))
    return out


def arg_files(tool, args):
    """arg_files is the set of files one call touches, by basename."""
    if not isinstance(args, dict):
        return set()
    if tool == "apply_patch":
        return patch_files(args.get("patch") or args.get("input") or "")
    if tool in ("read_file", "write_file", "list_dir", "glob", "grep"):
        path = args.get("path") or args.get("file_path") or ""
        return {os.path.basename(path)} if path else set()
    return set()


class Turn:
    __slots__ = ("start", "status", "calls", "texts", "reasoning")

    def __init__(self, start):
        self.start = start
        self.status = "open"
        self.calls = []      # [(tool, file-set, call_id, repeat-key)]
        self.texts = 0
        self.reasoning = 0


def read_rollout(path, stats, since, until):
    """read_rollout returns the turns of one rollout that fall in the window."""
    turns = []
    cur = None
    pending = {}   # call_id -> index into cur.calls
    with open(path, encoding="utf-8", errors="replace") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                o = json.loads(line)
            except json.JSONDecodeError:
                stats["bad_lines"] += 1
                continue
            kind = o.get("type")
            if kind == "turn.started":
                cur = Turn(event_time(o.get("time")))
            elif kind in ("turn.completed", "turn.failed"):
                if cur is not None:
                    cur.status = kind.split(".")[1]
                    turns.append(cur)
                    cur = None
            elif cur is None:
                continue
            elif kind == "item.tool_call":
                tool = o.get("tool") or "?"
                args = o.get("arguments")
                if isinstance(args, str):
                    try:
                        args = json.loads(args)
                    except json.JSONDecodeError:
                        args = {}
                if not isinstance(args, dict):
                    args = {}
                key = tool + "\u0000" + json.dumps(args, sort_keys=True, ensure_ascii=False)
                cid = o.get("call_id") or ""
                pending[cid] = len(cur.calls)
                cur.calls.append((tool, arg_files(tool, args), cid, key, args))
            elif kind == "item.tool_result":
                cid = o.get("call_id") or ""
                idx = pending.pop(cid, None)
                if idx is None:
                    # A compaction writes its summary through the same
                    # item.tool_result shape under a synthetic call id
                    # (compact-N): it is the transcript folding itself,
                    # not a call whose call was lost.
                    if cid.startswith("compact-"):
                        stats["compactions"] += 1
                    else:
                        stats["orphan_results"] += 1
                    continue
                tool, files, cid, key, args = cur.calls[idx]
                content = o.get("content")
                text = content if isinstance(content, str) else json.dumps(content, ensure_ascii=False)
                low = text.lower()
                result = {
                    "len": len(text),
                    "excerpt": EXCERPT_MARKER in text,
                    "error": bool(o.get("is_error")),
                    "timeout": "timed out" in low or "deadline exceeded" in low,
                }
                cur.calls[idx] = (tool, files, cid, key, args, result)
            elif kind == "item.assistant_message":
                cur.texts += 1
            elif kind == "item.reasoning":
                cur.reasoning += 1
    if cur is not None:
        stats["unfinished_turns"] += 1
        turns.append(cur)
    kept = []
    for t in turns:
        if since and t.start and t.start < since:
            continue
        if until and t.start and t.start > until:
            continue
        kept.append(t)
    return kept


def pct(values, p):
    if not values:
        return 0.0
    ordered = sorted(values)
    idx = min(len(ordered) - 1, max(0, int(round((p / 100.0) * (len(ordered) - 1)))))
    return ordered[idx]


def scan_rollouts(home, since, until):
    paths = sorted(glob.glob(os.path.join(home, "workspaces", "*", "sessions", "*", "rollout.jsonl")))
    stats = collections.defaultdict(int)
    turns = []
    for path in paths:
        turns.extend(read_rollout(path, stats, since, until))

    tools = collections.Counter()
    mutating = ("apply_patch", "write_file")
    no_write_turns = 0
    before_first_write = []
    after_last_write = []
    verify_calls = 0
    tool_errors = collections.Counter()
    tool_excerpts = collections.Counter()
    result_sizes = collections.defaultdict(list)
    read_windowed = read_whole = 0
    window_limits = collections.Counter()
    edit_events = 0
    rereads = 0
    repeat_calls = 0
    exec_total = exec_chained = exec_multiline = exec_sleep = 0
    timeout_results = 0
    for turn in turns:
        seen = collections.Counter()
        for call in turn.calls:
            tool = call[0]
            key = call[3]
            args = call[4]
            tools[tool] += 1
            seen[key] += 1
            if seen[key] > 1:
                repeat_calls += 1
            if tool == "read_file":
                if isinstance(args.get("offset"), (int, float)) or isinstance(args.get("limit"), (int, float)):
                    read_windowed += 1
                    window_limits[int(args.get("limit") or 0)] += 1
                else:
                    read_whole += 1
            elif tool == "exec_command":
                exec_total += 1
                cmd = args.get("command") or ""
                if isinstance(cmd, str):
                    if "\n" in cmd:
                        exec_multiline += 1
                    if any(ch in cmd for ch in ("&&", ";", "|", "$(", "`")) or "\n" in cmd:
                        exec_chained += 1
                    if "sleep " in cmd:
                        exec_sleep += 1
            if len(call) > 5:
                res = call[5]
                result_sizes[tool].append(res["len"])
                if res["excerpt"]:
                    tool_excerpts[tool] += 1
                if res["error"]:
                    tool_errors[tool] += 1
                if res["timeout"]:
                    timeout_results += 1
        writes = [i for i, call in enumerate(turn.calls) if call[0] in mutating]
        if not writes:
            no_write_turns += 1
        else:
            before_first_write.append(writes[0])
            after_last_write.append(len(turn.calls) - 1 - writes[-1])
        for i, call in enumerate(turn.calls):
            tool, files = call[0], call[1]
            if tool == "exec_command":
                cmd = (call[4] or {}).get("command") or ""
                if isinstance(cmd, str) and any(
                    probe in cmd
                    for probe in ("go test", "go build", "go vet", "npm test", "npm run",
                                  "make ", "pytest", "cargo ", "tsc", "golangci-lint",
                                  "staticcheck", "vitest", "playwright")
                ):
                    verify_calls += 1
            if tool not in mutating or not files:
                continue
            edit_events += 1
            for later in turn.calls[i + 1 : i + 1 + REREAD_WINDOW]:
                if later[0] == "read_file" and (later[1] & files):
                    rereads += 1
                    break

    return {
        "files": len(paths),
        "turns": len(turns),
        "bad_lines": stats["bad_lines"],
        "unfinished_turns": stats["unfinished_turns"],
        "orphan_results": stats["orphan_results"],
        "tools": tools,
        "tool_errors": tool_errors,
        "tool_excerpts": tool_excerpts,
        "result_sizes": result_sizes,
        "read_windowed": read_windowed,
        "read_whole": read_whole,
        "window_limits": window_limits,
        "edit_events": edit_events,
        "rereads": rereads,
        "repeat_calls": repeat_calls,
        "exec_total": exec_total,
        "exec_chained": exec_chained,
        "exec_multiline": exec_multiline,
        "exec_sleep": exec_sleep,
        "verify_calls": verify_calls,
        "timeout_results": timeout_results,
        "no_write_turns": no_write_turns,
        "before_first_write": before_first_write,
        "after_last_write": after_last_write,
        "compactions": stats["compactions"],
    }


def scan_rounds(home):
    """scan_rounds joins model calls (user.db) with turns (session.db)."""
    per_conv = []
    user_db = os.path.join(home, "user.db")
    if not os.path.exists(user_db):
        return None
    con = sqlite3.connect(f"file:{user_db}?mode=ro", uri=True)
    try:
        usage = collections.defaultdict(lambda: [0, 0, 0])  # calls, total, output
        for sid, calls, total, out in con.execute(
            "SELECT session_id, SUM(calls), SUM(total_tokens), SUM(output_tokens)"
            " FROM model_usage GROUP BY session_id"
        ):
            usage[sid] = [calls or 0, total or 0, out or 0]
    except sqlite3.Error as err:
        print(f"rounds-review: user.db has no usable model_usage ({err})", file=sys.stderr)
        return None
    finally:
        con.close()

    for sessions_db in sorted(glob.glob(os.path.join(home, "workspaces", "*", "sessions", "session.db"))):
        try:
            con = sqlite3.connect(f"file:{sessions_db}?mode=ro", uri=True)
            rows = con.execute(
                "SELECT id, turn_count FROM conversations WHERE turn_count > 0"
            ).fetchall()
            con.close()
        except sqlite3.Error:
            continue
        for sid, turns in rows:
            calls, total, out = usage.get(sid, [0, 0, 0])
            if calls <= 0:
                continue
            per_conv.append((sid, turns, calls, total, out))
    return per_conv


def main():
    ap = argparse.ArgumentParser(description="report where a turn's model rounds go")
    ap.add_argument("--home", default=os.environ.get("OPENCRAFT_HOME", os.path.expanduser("~/.opencraft")))
    ap.add_argument("--since", help="YYYY-MM-DD or RFC3339 (rollout turns only)")
    ap.add_argument("--until", help="YYYY-MM-DD or RFC3339")
    ap.add_argument("--json", action="store_true", help="machine-readable output")
    args = ap.parse_args()

    since = parse_when(args.since, False) if args.since else None
    until = parse_when(args.until, True) if args.until else None
    home = os.path.expanduser(args.home)
    if not os.path.isdir(home):
        raise SystemExit(f"rounds-review: no OpenCraft home at {home}")

    roll = scan_rollouts(home, since, until)
    rounds = scan_rounds(home)

    windows_note = []
    if since:
        windows_note.append(f"since {since.date()}")
    if until:
        windows_note.append(f"until {until.date()}")

    if args.json:
        out = {
            "files": roll["files"],
            "turns": roll["turns"],
            "bad_lines": roll["bad_lines"],
            "unfinished_turns": roll["unfinished_turns"],
            "orphan_results": roll["orphan_results"],
            "tools": dict(roll["tools"]),
            "tool_results": {
                tool: {
                    "count": len(sizes),
                    "excerpted": roll["tool_excerpts"].get(tool, 0),
                    "errors": roll["tool_errors"].get(tool, 0),
                    "p50_chars": pct(sizes, 50),
                }
                for tool, sizes in roll["result_sizes"].items()
            },
            "reads": {
                "windowed": roll["read_windowed"],
                "whole": roll["read_whole"],
                "window_limits": dict(roll["window_limits"].most_common(10)),
            },
            "edits": {
                "events": roll["edit_events"],
                "reread_within": roll["rereads"],
                "repeat_calls": roll["repeat_calls"],
            },
            "exec": {
                "total": roll["exec_total"],
                "shell_wrapped": roll["exec_chained"],
                "multiline": roll["exec_multiline"],
                "sleep_polls": roll["exec_sleep"],
                "verify_calls": roll["verify_calls"],
                "timeouts": roll["timeout_results"],
            },
            "turn_shape": {
                "turns_without_write": roll["no_write_turns"],
                "calls_before_first_write_p50": pct(roll["before_first_write"], 50),
                "calls_after_last_write_p50": pct(roll["after_last_write"], 50),
            },
            "compactions": roll["compactions"],
        }
        if rounds:
            per_turn = [calls / turns for _, turns, calls, _, _ in rounds]
            out["rounds"] = {
                "conversations": len(rounds),
                "turns": sum(r[1] for r in rounds),
                "calls": sum(r[2] for r in rounds),
                "calls_per_turn_p50": pct(per_turn, 50),
                "calls_per_turn_p90": pct(per_turn, 90),
                "calls_per_turn_mean": sum(per_turn) / len(per_turn),
            }
        json.dump(out, sys.stdout, ensure_ascii=False, indent=2, sort_keys=True)
        print()
        return

    span = ""
    if windows_note:
        span = " (" + ", ".join(windows_note) + ")"
    print(f"== corpus{span}")
    print(f"rollouts {roll['files']}, turns {roll['turns']}, "
          f"unfinished {roll['unfinished_turns']}, bad lines {roll['bad_lines']}, "
          f"compactions {roll['compactions']}, results without a call {roll['orphan_results']}")
    print("turns here are the retained ones (rotation drops the oldest generation); "
          "rounds below come from the databases and cover every turn")

    print()
    print("== rounds (model calls per turn, all-time)")
    if not rounds:
        print("no joinable model_usage rows")
    else:
        per_turn = [calls / turns for _, turns, calls, _, _ in rounds]
        calls = sum(r[2] for r in rounds)
        turns = sum(r[1] for r in rounds)
        print(f"conversations {len(rounds)}, turns {turns}, calls {calls}, "
              f"calls/turn {calls / turns:.1f}")
        print(f"per conversation: p50 {pct(per_turn, 50):.0f}, p90 {pct(per_turn, 90):.0f}, "
              f"mean {sum(per_turn) / len(per_turn):.1f}, max {max(per_turn):.0f}")
        print("heaviest conversations:")
        print(f"  {'conversation':24s} {'turns':>5s} {'calls':>6s} {'calls/turn':>10s} "
              f"{'ktok/call':>9s} {'out/call':>8s}")
        for sid, t, c, total, out in sorted(rounds, key=lambda r: r[2], reverse=True)[:10]:
            print(f"  {sid:24s} {t:5d} {c:6d} {c / t:10.1f} "
                  f"{total / max(c, 1) / 1000:9.1f} {out / max(c, 1):8.0f}")

    total_calls = sum(roll["tools"].values())
    print()
    print(f"== tools ({total_calls} calls over {roll['turns']} turns, "
          f"{total_calls / max(roll['turns'], 1):.1f}/turn)")
    print(f"  {'tool':22s} {'calls':>7s} {'share':>6s} {'errors':>7s}")
    for tool, n in roll["tools"].most_common(16):
        errs = roll["tool_errors"].get(tool, 0)
        print(f"  {tool:22s} {n:7d} {100.0 * n / max(total_calls, 1):5.1f}% {errs:7d}")

    print()
    print("== tool results (cap changed 8k -> 24k; excerpted = the model saw head/tail only)")
    rows = sorted(roll["result_sizes"].items(), key=lambda kv: -len(kv[1]))[:10]
    print(f"  {'tool':22s} {'n':>7s} {'excerpted':>10s} {'p50':>7s} {'p90':>8s}  bands")
    for tool, sizes in rows:
        bands = collections.Counter(size_band(n) for n in sizes)
        band_txt = " ".join(f"{name}:{bands[name]}" for _, _, name in SIZE_BANDS if bands.get(name))
        print(f"  {tool:22s} {len(sizes):7d} {roll['tool_excerpts'].get(tool, 0):10d} "
              f"{pct(sizes, 50):7d} {pct(sizes, 90):8d}  {band_txt}")

    reads = roll["read_windowed"] + roll["read_whole"]
    print()
    print("== reads and edits")
    print(f"read_file {reads}: windowed {roll['read_windowed']} "
          f"({100.0 * roll['read_windowed'] / max(reads, 1):.0f}%), whole {roll['read_whole']}")
    limits = roll["window_limits"].most_common(6)
    if limits:
        print("  window limits: " + ", ".join(f"{k or 'offset-only'} x{v}" for k, v in limits))
    print(f"edits {roll['edit_events']}: re-read within {REREAD_WINDOW} calls "
          f"{roll['rereads']} ({100.0 * roll['rereads'] / max(roll['edit_events'], 1):.0f}%)")
    print(f"repeated calls (same tool + same args in one turn): {roll['repeat_calls']} "
          f"({100.0 * roll['repeat_calls'] / max(total_calls, 1):.1f}% of calls)")

    print()
    print("== turn shape (a turn is one user request; writes are apply_patch / write_file)")
    turns_n = max(roll["turns"], 1)
    print(f"turns without a write {roll['no_write_turns']} "
          f"({100.0 * roll['no_write_turns'] / turns_n:.0f}% of turns)")
    if roll["before_first_write"]:
        print(f"calls before the first write: p50 {pct(roll['before_first_write'], 50):.0f}, "
              f"p90 {pct(roll['before_first_write'], 90):.0f}")
        print(f"calls after the last write: p50 {pct(roll['after_last_write'], 50):.0f} "
              "(the verification tail)")

    print()
    print("== exec")
    if roll["exec_total"]:
        share = 100.0 * roll["exec_chained"] / roll["exec_total"]
        print(f"exec_command {roll['exec_total']}: shell-wrapped {roll['exec_chained']} ({share:.0f}%), "
              f"multiline {roll['exec_multiline']}, sleep-polls {roll['exec_sleep']}, "
              f"timeout results {roll['timeout_results']}, "
              f"test/build {roll['verify_calls']}")

    print()
    print("== checks (bad lines should be 0; a result without a call is a lost call line)")
    print(f"bad_lines {roll['bad_lines']}, orphan_results {roll['orphan_results']}, "
          f"unfinished_turns {roll['unfinished_turns']}, "
          f"results {sum(len(v) for v in roll['result_sizes'].values())} of {total_calls} calls "
          "(a call with no result was interrupted)")


if __name__ == "__main__":
    main()
