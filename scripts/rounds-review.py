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
databases, and they may well be open by the running shell. A WAL store
whose -shm is gone refuses a read-only open in place (SQLite answers
CANTOPEN instead of creating one), so those are snapshotted to a temp
copy first — dropping the whole workspace was how an earlier report lost
2100 rounds without saying so.

The rounds line covers the conversations that still exist, and the
coverage section says how much of the ledger that is: deleted
conversations, and model calls whose session id never was a conversation
(delegation, review runs), spend tokens too but have no turn count, so
they cannot enter a per-turn ratio. Read the two together before
treating calls/turn as the whole population.
"""

import argparse
import collections
import contextlib
import glob
import json
import os
import shutil
import sqlite3
import sys
import tempfile
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


# exec_command's argument name is "command"; "cmd" is its one alias,
# accepted so a model habit costs nothing (exec/command.go). A report
# that reads only "command" cannot see a quarter of the exec calls.
COMMAND_ALIASES = ("command", "cmd")

# The tool runs a command directly (no shell) only when every whitespace
# token is built from these characters -- directArgs/safeWord in
# exec/command.go, with POSIX as the platform (Windows additionally
# allows a backslash, which is an escape on POSIX shells).
DIRECT_SAFE = set(
    "abcdefghijklmnopqrstuvwxyz"
    "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
    "0123456789_./:+-"
)


def command_text(args):
    """command_text is the command line one exec call asked for."""
    if not isinstance(args, dict):
        return ""
    for name in COMMAND_ALIASES:
        value = args.get(name)
        if isinstance(value, str) and value.strip():
            return value.strip()
    return ""


def direct_argv(cmd):
    """direct_argv reports whether the tool would skip the shell.

    Mirrors directArgs: the token split has to round-trip exactly, and
    every token has to be a bare word. Anything else (quotes, pipes,
    quotes, redirects, &&, globs, $, tabs, repeated spaces) is the
    shell's business.
    """
    fields = cmd.split()
    if not fields or " ".join(fields) != cmd:
        return False
    return all(all(ch in DIRECT_SAFE for ch in f) for f in fields)


def size_band(n):
    for lo, hi, name in SIZE_BANDS:
        if lo <= n < hi:
            return name
    return "?"


def parse_when(s, end_of_day):
    """parse_when accepts YYYY-MM-DD (that UTC day) or RFC3339.

    A timestamp without an offset is read as UTC, which is what the
    rollouts carry: leaving it naive made the window comparison raise
    TypeError against their offset-aware times instead of filtering.
    """
    s = s.strip()
    try:
        if len(s) == 10:
            d = datetime.strptime(s, "%Y-%m-%d").replace(tzinfo=timezone.utc)
            if end_of_day:
                d = d.replace(hour=23, minute=59, second=59, microsecond=999999)
            return d
        d = datetime.fromisoformat(s.replace("Z", "+00:00"))
    except ValueError:
        raise SystemExit(f"rounds-review: bad date {s!r} (want YYYY-MM-DD or RFC3339)")
    return d if d.tzinfo else d.replace(tzinfo=timezone.utc)


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
                if cur is not None:
                    # A new turn opening over an unclosed one: the run
                    # was interrupted (a stop, a crash, a restart), so
                    # the turn is unfinished, not absent. Dropping it
                    # here silently lost its rounds and its calls.
                    stats["unfinished_turns"] += 1
                    turns.append(cur)
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
    exec_total = exec_shell = exec_direct = exec_cd = exec_multiline = exec_sleep = 0
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
                cmd = command_text(args)
                if direct_argv(cmd):
                    exec_direct += 1
                else:
                    exec_shell += 1
                if cmd:
                    if "\n" in cmd:
                        exec_multiline += 1
                    if cmd.startswith("cd ") and ("&&" in cmd or ";" in cmd):
                        exec_cd += 1
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
        "exec_shell": exec_shell,
        "exec_direct": exec_direct,
        "exec_cd": exec_cd,
        "exec_multiline": exec_multiline,
        "exec_sleep": exec_sleep,
        "verify_calls": verify_calls,
        "timeout_results": timeout_results,
        "no_write_turns": no_write_turns,
        "before_first_write": before_first_write,
        "after_last_write": after_last_write,
        "compactions": stats["compactions"],
    }


@contextlib.contextmanager
def open_readonly(path, copies):
    """open_readonly opens a store without touching it, and says how.

    In place is the normal answer. A WAL database with no -shm beside it
    is the exception: SQLite cannot attach to the shared-memory index
    without creating one, and a read-only connection may not, so it
    answers CANTOPEN rather than opening the file it was given — which is
    how one workspace's 2100 rounds once dropped out of a report in
    silence. The way through is a snapshot: copy the database and its WAL
    sidecars, then open the copy as immutable. Immutable is exact for a
    copy nothing else can write, and it is what lets SQLite read a WAL
    store with no index at all. paths is appended to when that happens,
    so the report can say how many stores were read as a copy.
    """
    try:
        con = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
        try:
            # connect() is lazy: the header, and so the -shm, is touched
            # on the first statement.
            con.execute("PRAGMA schema_version").fetchone()
        except sqlite3.Error:
            con.close()
            raise
    except sqlite3.Error:
        tmp = tempfile.mkdtemp(prefix="rounds-review-")
        try:
            name = os.path.basename(path)
            for suffix in ("", "-wal", "-shm"):
                if os.path.exists(path + suffix):
                    shutil.copy2(path + suffix, os.path.join(tmp, name + suffix))
            con = sqlite3.connect(
                f"file:{os.path.join(tmp, name)}?immutable=1", uri=True,
            )
        except BaseException:
            shutil.rmtree(tmp, ignore_errors=True)
            raise
        copies.append(path)
    else:
        tmp = None
    try:
        yield con
    finally:
        con.close()
        if tmp:
            shutil.rmtree(tmp, ignore_errors=True)


def scan_rounds(home):
    """scan_rounds joins model calls (user.db) with turns (session.db).

    Returns (per_conv, coverage). per_conv is what the round ratio is
    computed from; coverage says what the ledger holds beyond it, so the
    ratio is never read as the whole corpus.
    """
    user_db = os.path.join(home, "user.db")
    if not os.path.exists(user_db):
        return None, None
    copies = []
    usage = {}
    try:
        with open_readonly(user_db, copies) as con:
            for sid, calls, total, out in con.execute(
                "SELECT session_id, SUM(calls), SUM(total_tokens), SUM(output_tokens)"
                " FROM model_usage GROUP BY session_id"
            ):
                usage[sid] = (calls or 0, total or 0, out or 0)
    except sqlite3.Error as err:
        print(f"rounds-review: user.db has no usable model_usage ({err})", file=sys.stderr)
        return None, None

    per_conv = []
    stores = 0
    deleted = set()
    with_turns = 0
    for sessions_db in sorted(glob.glob(os.path.join(home, "workspaces", "*", "sessions", "session.db"))):
        try:
            with open_readonly(sessions_db, copies) as con:
                rows = con.execute(
                    "SELECT id, turn_count FROM conversations WHERE turn_count > 0"
                ).fetchall()
                try:
                    deleted.update(
                        x[0] for x in con.execute("SELECT id FROM deleted_conversations")
                    )
                except sqlite3.Error:
                    # Pre-migration stores have no tombstone table; every
                    # deletion they saw is simply absent below.
                    pass
        except sqlite3.Error as err:
            print(f"rounds-review: skipping {sessions_db}: {err}", file=sys.stderr)
            continue
        stores += 1
        with_turns += len(rows)
        for sid, turns in rows:
            calls, total, out = usage.get(sid, (0, 0, 0))
            if calls <= 0:
                continue
            per_conv.append((sid, turns, calls, total, out))

    joined = {row[0] for row in per_conv}
    calls_total = sum(v[0] for v in usage.values())
    calls_joined = sum(row[2] for row in per_conv)
    calls_deleted = sum(v[0] for k, v in usage.items() if k in deleted and k not in joined)
    coverage = {
        "stores": stores,
        "stores_snapshotted": len(copies),
        "calls_total": calls_total,
        "calls_in_conversations": calls_joined,
        "calls_in_deleted_conversations": calls_deleted,
        "calls_without_conversation": calls_total - calls_joined - calls_deleted,
        "conversations_with_turns": with_turns,
        "conversations_without_usage": with_turns - len(joined),
    }
    return per_conv, coverage


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
    rounds, coverage = scan_rounds(home)

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
                "direct": roll["exec_direct"],
                "shell_wrapped": roll["exec_shell"],
                "leading_cd": roll["exec_cd"],
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
            "coverage": coverage,
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
        if coverage:
            print()
            print("== coverage (what calls/turn cannot see: a ratio needs a turn count)")
            print(f"model calls {coverage['calls_total']}: "
                  f"in conversations {coverage['calls_in_conversations']}, "
                  f"in deleted conversations {coverage['calls_in_deleted_conversations']}, "
                  f"in sessions that are not conversations {coverage['calls_without_conversation']}")
            print(f"conversations with turns {coverage['conversations_with_turns']}, "
                  f"without usage rows {coverage['conversations_without_usage']}; "
                  f"stores read {coverage['stores']}"
                  + (f" ({coverage['stores_snapshotted']} from a snapshot)"
                     if coverage["stores_snapshotted"] else ""))

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
        share = 100.0 * roll["exec_shell"] / roll["exec_total"]
        cd_share = 100.0 * roll["exec_cd"] / roll["exec_total"]
        print(f"exec_command {roll['exec_total']}: through a shell {roll['exec_shell']} ({share:.0f}%), "
              f"direct {roll['exec_direct']}; "
              f"leading `cd ... &&` {roll['exec_cd']} ({cd_share:.0f}%), "
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
