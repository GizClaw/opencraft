#!/usr/bin/env python3
"""mem-fixture builds the deterministic heavy session the desktop memory
review runs against (docs/memory-review.md, section "scenario").

The real workspaces no longer hold a screenshot-heavy conversation, so the
scenario needs a synthetic one whose bytes are known in advance and stable
across runs: generate once, import once, and every before/after reading
compares the same input.

It exercises exactly the three read paths the fixes bound:

  * inline view_image frames - 30 turns x 2 frames, far past the store's
    16 MiB media budget, so eviction must shed the oldest and the cards
    must fall back to the file on disk;
  * long tool results - three >256 KiB text results per turn and one 3 MB
    result in a recent turn, past both the per-result cap and the 16 MiB
    text budget;
  * user attachments - screenshot files referenced by path, re-read on
    mount by AttachmentImage;
  * a page with real vector/raster content for the PDF streaming path
    (docs/mem-fixture.pdf) and shots/ for the clipboard step.

Output layout (default root ~/Workspace/mem-fixture):

  ~/Workspace/mem-fixture/            the workspace to open in the app
    README.md                         the fixed-action checklist
    shots/shot-01.png ... shot-12.png 1600x1000 screenshots (paste two)
    docs/mem-fixture.pdf              12 pages, each a full-page image
  ~/Workspace/mem-fixture.bundle.json the import bundle (kept outside the
                                      workspace so the 70 MB file is not in
                                      the file tree)

The bundle imports through the app's own Sidebar > Import session flow.
Everything is seeded: same bytes, same sizes, same turn layout on every
machine. Re-running with a new stamp produces a NEW session (the import
source doubles as the dedupe key), so before/after runs never fight over
one conversation.

Usage:
  python3 scripts/mem-fixture.py                 # default root + seed
  python3 scripts/mem-fixture.py --root DIR      # somewhere else
  python3 scripts/mem-fixture.py --stamp 1       # force a fresh source id
"""

from __future__ import annotations

import argparse
import base64
import json
import random
import struct
import zlib
from datetime import datetime, timedelta, timezone
from pathlib import Path

# Turn layout: 30 heavy screenshot turns, then light turns, one turn with
# a single 3 MB result (the per-result cap must visibly trim it), and a
# plain closing exchange.
HEAVY_TURNS = 30
TOTAL_TURNS = 36
SHOT_COUNT = 12
SHOT_W, SHOT_H = 1600, 1000
PDF_W, PDF_H = 900, 563


# ---------------------------------------------------------------- PNG


def _chunk(tag: bytes, data: bytes) -> bytes:
    crc = zlib.crc32(tag + data) & 0xFFFFFFFF
    return struct.pack(">I", len(data)) + tag + data + struct.pack(">I", crc)


def png_bytes(w: int, h: int, rgb: bytes) -> bytes:
    stride = w * 3
    raw = bytearray()
    for y in range(h):
        raw.append(0)  # filter: none
        raw += rgb[y * stride : (y + 1) * stride]
    return (
        b"\x89PNG\r\n\x1a\n"
        + _chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0))
        + _chunk(b"IDAT", zlib.compress(bytes(raw), 6))
        + _chunk(b"IEND", b"")
    )


# ------------------------------------------------------------- shots

# shot_rgb paints a deterministic "screenshot": a chrome bar, a sidebar, a
# content area of text-like bars, a chart block and one static-noise
# rectangle. The noise is the size knob -- PNG of static is ~raw bytes, so
# its area decides the file size and with it the loaded media total.
def shot_rgb(index: int, w: int, h: int) -> bytes:
    rnd = random.Random(f"mem-fixture-shot-{index}")
    buf = bytearray(w * h * 3)
    stride = w * 3

    # Background gradient.
    for y in range(h):
        t = y / max(1, h - 1)
        v = int(246 - 40 * t)
        buf[y * stride : (y + 1) * stride] = bytes((v, v - 2, v + 6)) * w

    def rect(x0: int, y0: int, x1: int, y1: int, color: tuple[int, int, int]) -> None:
        x0, x1 = max(0, x0), min(w, x1)
        y0, y1 = max(0, y0), min(h, y1)
        if x0 >= x1 or y0 >= y1:
            return
        row = bytes(color) * (x1 - x0)
        for y in range(y0, y1):
            buf[y * stride + x0 * 3 : y * stride + x1 * 3] = row

    # Window chrome with traffic lights.
    rect(0, 0, w, 54, (34, 38, 46))
    for i, c in enumerate(((238, 106, 94), (245, 191, 79), (98, 197, 84))):
        rect(24 + i * 26, 20, 38 + i * 26, 34, c)
    rect(160, 20, 160 + 420, 36, (58, 64, 76))

    # Sidebar with a list of rows, one highlighted.
    rect(0, 54, 300, h, (238, 240, 244))
    rect(20, 78, 280, 108, (214, 222, 240))
    for i in range(14):
        y = 132 + i * 34
        width = 150 + (index * 17 + i * 43) % 110
        rect(28, y, 28 + width, y + 12, (188, 194, 206))

    # Content: a header line, then wrapped text bars with paragraph gaps.
    rect(340, 92, 340 + 520, 116, (176, 182, 196))
    y = 156
    while y < h - 120:
        width = int(w - 420 - rnd.randrange(0, 260))
        rect(340, y, 340 + width, y + 10, (196, 200, 212))
        y += 26
        if rnd.random() < 0.18:
            y += 18

    # A chart block with a few bars.
    rect(340, h - 240, w - 90, h - 90, (250, 250, 252))
    for i in range(9):
        bx = 380 + i * 68
        bh = 30 + (index * 31 + i * 47) % 110
        rect(bx, h - 120 - bh, bx + 40, h - 120, (120 + i * 8, 150, 220 - i * 6))

    # Static-noise rectangle: the incompressible payload, sized per index.
    area = 108_000 + (index * 7919) % 42_000  # ~108k..150k px
    nw = 400 + (index * 13) % 90
    nh = area // nw
    nx = 620 + (index * 37) % 300
    ny = 170 + (index * 53) % 380
    for y in range(ny, min(h, ny + nh)):
        buf[y * stride + nx * 3 : y * stride + min(w, nx + nw) * 3] = rnd.randbytes(
            (min(w, nx + nw) - nx) * 3
        )

    return bytes(buf)


def downsample(rgb: bytes, w: int, h: int, tw: int, th: int) -> bytes:
    out = bytearray(tw * th * 3)
    for y in range(th):
        sy = y * h // th
        row = rgb[sy * w * 3 : (sy + 1) * w * 3]
        base = y * tw * 3
        for x in range(tw):
            sx = x * w // tw
            out[base + x * 3 : base + x * 3 + 3] = row[sx * 3 : sx * 3 + 3]
    return bytes(out)


# --------------------------------------------------------------- PDF


def build_pdf(pages: list[tuple[bytes, int, int, str]]) -> bytes:
    """pages: (rgb, width, height, caption). One image per page."""
    n = len(pages)
    catalog_id, pages_id, font_id = 1, 2, 3
    page_ids = [4 + 3 * i for i in range(n)]
    count = 3 + 3 * n

    out = bytearray(b"%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
    offsets: dict[int, int] = {}

    def put(oid: int, body: bytes) -> None:
        offsets[oid] = len(out)
        out.extend(f"{oid} 0 obj\n".encode())
        out.extend(body)
        out.extend(b"\nendobj\n")

    put(catalog_id, f"<< /Type /Catalog /Pages {pages_id} 0 R >>".encode())
    kids = " ".join(f"{pid} 0 R" for pid in page_ids)
    put(pages_id, f"<< /Type /Pages /Kids [{kids}] /Count {n} >>".encode())
    put(font_id, b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")

    for i, (rgb, w, h, caption) in enumerate(pages):
        pid = page_ids[i]
        content_id = pid + 1
        image_id = pid + 2
        stream = zlib.compress(rgb, 6)
        content = (
            f"q {w} 0 0 {h} 36 80 cm /Im0 Do Q\n"
            f"BT /F1 13 Tf 36 28 Td ({caption}) Tj ET"
        ).encode()
        put(
            pid,
            (
                f"<< /Type /Page /Parent {pages_id} 0 R "
                f"/MediaBox [0 0 972 680] "
                f"/Resources << /XObject << /Im0 {image_id} 0 R >> "
                f"/Font << /F1 {font_id} 0 R >> /ProcSet [/PDF /Text /ImageC] >> "
                f"/Contents {content_id} 0 R >>"
            ).encode(),
        )
        put(
            content_id,
            b"<< /Length %d >>\nstream\n" % len(content) + content + b"\nendstream",
        )
        put(
            image_id,
            (
                f"<< /Type /XObject /Subtype /Image /Width {w} /Height {h} "
                f"/ColorSpace /DeviceRGB /BitsPerComponent 8 "
                f"/Filter /FlateDecode /Length {len(stream)} >>\nstream\n"
            ).encode()
            + stream
            + b"\nendstream",
        )

    xref_at = len(out)
    out.extend(f"xref\n0 {count + 1}\n".encode())
    out.extend(b"0000000000 65535 f \n")
    for oid in range(1, count + 1):
        out.extend(f"{offsets[oid]:010d} 00000 n \n".encode())
    out.extend(
        (
            f"trailer\n<< /Size {count + 1} /Root {catalog_id} 0 R >>\n"
            f"startxref\n{xref_at}\n%%EOF\n"
        ).encode()
    )
    return bytes(out)


# -------------------------------------------------------- bundle text


def log_text(rnd: random.Random, size: int, failing: bool) -> str:
    lines: list[str] = []
    total = 0
    n = 0
    while total < size:
        n += 1
        if rnd.random() < 0.72:
            line = f"--- PASS: TestCase{n:05d} ({rnd.random() * 0.4:.2f}s)\n"
        elif rnd.random() < 0.5:
            line = f"ok  \tgithub.com/GizClaw/opencraft/internal/pkg{n % 40} \t{rnd.random() * 2:.3f}s\n"
        else:
            line = f"    pkg{n % 40}_test.go:{rnd.randrange(20, 900)}: step {n} done\n"
        lines.append(line)
        total += len(line)
    if failing:
        lines.append("--- FAIL: TestTurnsSinceReadsTheTranscriptTail (0.02s)\n")
        lines.append("    store_test.go:1268: turns[2].Seq = 9, want 10\n")
        lines.append("FAIL\n")
        lines.append(
            "FAIL\tgithub.com/GizClaw/opencraft/internal/capabilities/sessions\t0.532s\n"
        )
    text = "".join(lines)
    return text[:size] + ("\n" if not text[:size].endswith("\n") else "")


def dump_text(rnd: random.Random, size: int, name: str) -> str:
    lines: list[str] = [f"// {name}\n// lines 1..N, generated for the memory fixture\n\n"]
    total = len(lines[0])
    n = 0
    while total < size:
        n += 1
        if n % 12 == 0:
            line = f"\nfunc (s *Store) Method{n:04d}(ctx context.Context) error {{\n"
            line += f"\treturn s.run(ctx, {n})\n}}\n"
        elif rnd.random() < 0.4:
            line = f"// {n:05d}: the archive read walks the transcript tail and folds nothing here.\n"
        else:
            line = (
                f"\tcase {n:05d}:\n\t\tvalue += {n * 7 % 997}\n"
                if n % 5 == 0
                else f"\tif err := step{n:05d}(ctx); err != nil {{ return err }}\n"
            )
        lines.append(line)
        total += len(line)
    text = "".join(lines)
    return text[:size] + ("\n" if not text[:size].endswith("\n") else "")


# ------------------------------------------------------------- bundle

USER_ASKS = [
    "帮我看看这个测试为什么挂了，输出在下面",
    "读一下这个文件，讲讲 key 的生命周期是怎么走的",
    "截图里这个报错是什么意思？",
    "把 sessions 包最近改动的相关代码通读一遍，找找不一致的地方",
    "这些失败用例是本轮改出来的还是本来就有？",
    "帮我确认下 store 的写路径是不是只有一处收口",
    "看看这张图里的时序，是不是漏了一个边界",
    "把这两个文件对比一下，diff 里有没有行为变化",
    "为什么这里需要折叠？给个依据",
    "顺着这个调用链往下走，看看哪里会复制大对象",
]

SUMMARIES = [
    "看完了：失败在 tail 折叠后把 cursor 也折掉了，修在 read 侧。",
    "结论写在上面；不需要动投影规则。",
    "这里不是回归，是历史遗留，先记着不动。",
    "确认了：写路径只有 settleConversation 一处，别的都走它。",
    "diff 里只有命名变化，行为一致。",
    "问题在边界：steer 之后才追加，所以顺序断言要调整。",
    "这条链上有两次拷贝，但都不大；大对象在结果文本里。",
]

REASONINGS = [
    "The failure is in the sessions package; read the store and the test together before changing anything.",
    "Compare the two revisions first, then check whether the call sites still line up.",
    "The screenshot shows the boundary between tool result and steer; verify against the archive rows.",
    "Check the write path: every store mutation should pass one settle point.",
]


def build_bundle(
    seed: int,
    stamp: str,
    shots: list[Path],
    failing_turns: set[int],
    heavy_turns: int,
    total_turns: int,
) -> dict:
    now = datetime.now(timezone.utc).replace(microsecond=0)
    base = now - timedelta(minutes=17 * total_turns)

    def call(cid: str, name: str, args: dict) -> dict:
        return {"type": "tool_call", "call": {"id": cid, "name": name, "arguments": args}}

    def result(cid: str, parts: list[dict], is_error: bool = False) -> dict:
        return {
            "type": "tool_result",
            "result": {"call_id": cid, "content": {"parts": parts}, "is_error": is_error},
        }

    def text(t: str) -> dict:
        return {"type": "text", "text": t}

    def msg(role: str, parts: list[dict]) -> dict:
        return {"role": role, "content": {"parts": parts}}

    turns: list[dict] = []

    for i in range(1, total_turns + 1):
        at = base + timedelta(minutes=17 * i)
        rnd = random.Random(f"mem-fixture-turn-{seed}-{i}")
        messages: list[dict] = []
        ask = USER_ASKS[(i - 1) % len(USER_ASKS)]

        if i <= heavy_turns:
            shot_a = shots[(2 * i - 2) % len(shots)]
            shot_b = shots[(2 * i - 1) % len(shots)]
            user_parts = [text(ask)]
            if i % 3 == 0:
                att = shots[(i // 3 - 1) % len(shots)]
                user_parts.append(
                    {
                        "type": "image",
                        "source": {
                            "kind": "url",
                            "url": str(att),
                            "media_type": "image/png",
                        },
                    }
                )
            messages.append(msg("user", user_parts))

            c1, c2, c3 = f"call_h{i}_1", f"call_h{i}_2", f"call_h{i}_3"
            messages.append(
                msg(
                    "assistant",
                    [
                        {"type": "reasoning", "text": REASONINGS[i % len(REASONINGS)]},
                        call(c1, "exec_command", {"command": f"go test ./internal/pkg{i % 40}/... -count=1 2>&1 | tail -n 4000"}),
                    ],
                )
            )
            size = 260_000 + rnd.randrange(0, 270_000)
            messages.append(msg("tool", [result(c1, [text(log_text(rnd, size, i in failing_turns))])]))
            messages.append(
                msg(
                    "assistant",
                    [call(c2, "read_file", {"file_path": f"internal/pkg{i % 40}/store.go", "offset": 1, "limit": 2000})],
                )
            )
            size = 260_000 + rnd.randrange(0, 270_000)
            messages.append(msg("tool", [result(c2, [text(dump_text(rnd, size, f"internal/pkg{i % 40}/store.go"))])]))
            messages.append(
                msg(
                    "assistant",
                    [call(c3, "read_file", {"file_path": f"internal/pkg{i % 40}/projection.go"})],
                )
            )
            size = 260_000 + rnd.randrange(0, 270_000)
            messages.append(msg("tool", [result(c3, [text(dump_text(rnd, size, f"internal/pkg{i % 40}/projection.go"))])]))

            for cid, shot in ((f"call_h{i}_4", shot_a), (f"call_h{i}_5", shot_b)):
                encoded = shot.read_bytes()
                b64 = base64.b64encode(encoded).decode("ascii")
                messages.append(
                    msg("assistant", [call(cid, "view_image", {"path": str(shot)})])
                )
                messages.append(
                    msg(
                        "tool",
                        [
                            result(
                                cid,
                                [
                                    text(
                                        f"view_image: {shot} ({SHOT_W}x{SHOT_H}, {len(encoded)} bytes)"
                                    ),
                                    {
                                        "type": "image",
                                        "source": {
                                            "kind": "inline",
                                            "data": b64,
                                            "media_type": "image/png",
                                        },
                                    },
                                ],
                            )
                        ],
                    )
                )
            messages.append(msg("assistant", [text(SUMMARIES[i % len(SUMMARIES)])]))
        elif i < total_turns - 1:
            messages.append(msg("user", [text(ask)]))
            cid = f"call_l{i}"
            messages.append(
                msg("assistant", [call(cid, "exec_command", {"command": "git status --short && git diff --stat"})])
            )
            messages.append(
                msg(
                    "tool",
                    [result(cid, [text("internal/pkg/store.go | 12 ++++++------\n1 file changed, 6 insertions(+), 6 deletions(-)\n")])],
                )
            )
            messages.append(msg("assistant", [text("工作区是干净的，只有上一次的机械重写还没提交。")]))
        elif i == total_turns - 1:
            messages.append(msg("user", [text("最后跑一次全量测试，把输出留着；如果挂了我要看原因")]))
            cid = f"call_g{i}"
            messages.append(
                msg("assistant", [call(cid, "exec_command", {"command": "go test ./... -count=1 2>&1 | tail -n 20000"})])
            )
            rnd_big = random.Random(f"mem-fixture-big-{seed}")
            messages.append(msg("tool", [result(cid, [text(log_text(rnd_big, 3_000_000, True))])]))
            messages.append(
                msg("assistant", [text("全量跑完了：sessions 里那两个用例还是红的，其余都绿；红的原因是夹具里故意留的。")])
            )
        else:
            messages.append(msg("user", [text("好，先这样。")]))
            messages.append(msg("assistant", [text("收到。需要的时候叫我。")]))

        turns.append(
            {
                "at": at.isoformat().replace("+00:00", "Z"),
                "requested_at": at.isoformat().replace("+00:00", "Z"),
                "started_at": (at + timedelta(seconds=2)).isoformat().replace("+00:00", "Z"),
                "finished_at": (at + timedelta(seconds=90 + rnd.randrange(0, 240))).isoformat().replace("+00:00", "Z"),
                "messages": messages,
            }
        )

    return {
        "title": "内存复核夹具：截图与长输出（场景用）",
        "source": f"mem-fixture:{stamp}",
        "usage": {
            "input_tokens": 18_285_306,
            "output_tokens": 136_750,
            "total_tokens": 18_422_056,
            "cache_read_tokens": 17_842_816,
            "reasoning_tokens": 107_064,
            "latency_ms": 2_052_054,
            "calls": 172,
        },
        "turns": turns,
    }


# ------------------------------------------------------------- main

README = """# mem-fixture

Synthetic workspace for the desktop memory review (docs/memory-review.md, the
"scenario" section of the OpenCraft repo). Generated by
`python3 scripts/mem-fixture.py`; safe to delete and regenerate.

Contents:
  shots/shot-01.png ...  1600x1000 screenshots; step 4 pastes two of them
  docs/mem-fixture.pdf   12 pages; step 3 opens and scrolls it
  ../mem-fixture.bundle.json  import this through Sidebar > Import session

Run order:
  1. Enable the render probe (Settings > Diagnostics > DEV tools).
  2. Open this directory as the workspace, import the bundle, and restart
     the app for a clean baseline (do not touch it for the first 2 minutes).
  3. Fixed actions, about 15 minutes:
     a. The imported session is on screen: the newest turn's `go test` card
        shows a `[trimmed ...]` marker (256 KiB per-result cap). Page back
        through history three times, then return to the bottom.
     b. Send the low-token long-turn recipe (about 6 minutes of streaming):
        "依次执行 8 条命令，每条为 `sleep 40`（exec_command，串行，等上一条
        结束再发下一条），每条完成只回一行「第 N/8 条完成」；第 4 条之后用
        view_image 看一下 shots/shot-01.png 并回一行它的尺寸。"
     c. Open docs/mem-fixture.pdf, scroll 5 pages, close the tab; open it
        again, then switch to another file tab.
     d. Copy two screenshots in Finder and paste them into the composer.
     e. Switch to another session and back.
     f. Stop for 2 minutes, then read: scripts/mem-review.sh 1
  4. Acceptance: web_mb last-first <= 100 MB, media_mb <= 16, text_mb <= 16
     (the budgets are 16 MiB, so 16.0 is the line, not "around 16").
"""


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--root", default=str(Path.home() / "Workspace" / "mem-fixture"))
    ap.add_argument("--seed", type=int, default=20260924)
    ap.add_argument("--stamp", default=datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ"))
    ap.add_argument("--shots", type=int, default=SHOT_COUNT)
    ap.add_argument("--turns", type=int, default=HEAVY_TURNS,
                    help="heavy turns (the default 30 fills the 3 history pages)")
    args = ap.parse_args()

    heavy_turns = args.turns
    total_turns = args.turns + 6
    shot_count = args.shots

    root = Path(args.root).expanduser().resolve()
    shots_dir = root / "shots"
    docs_dir = root / "docs"
    shots_dir.mkdir(parents=True, exist_ok=True)
    docs_dir.mkdir(parents=True, exist_ok=True)

    shots: list[Path] = []
    shot_bytes: list[bytes] = []
    for i in range(1, shot_count + 1):
        path = shots_dir / f"shot-{i:02d}.png"
        png = png_bytes(SHOT_W, SHOT_H, shot_rgb(i, SHOT_W, SHOT_H))
        path.write_bytes(png)
        shots.append(path)
        shot_bytes.append(png)
        print(f"  shots/{path.name}: {len(png) // 1024} KiB")

    pages = []
    for i in range(shot_count):
        rgb = downsample(shot_bytes[i], SHOT_W, SHOT_H, PDF_W, PDF_H)
        pages.append((rgb, PDF_W, PDF_H, f"mem-fixture page {i + 1:02d}/{shot_count} - shots/shot-{i + 1:02d}.png"))
    pdf = build_pdf(pages)
    pdf_path = docs_dir / "mem-fixture.pdf"
    pdf_path.write_bytes(pdf)
    print(f"  docs/mem-fixture.pdf: {len(pdf) // 1024} KiB, {len(pages)} pages")

    failing = {i for i in range(2, heavy_turns + 1, 4)}
    bundle = build_bundle(args.seed, args.stamp, shots, failing, heavy_turns, total_turns)
    bundle_path = root.parent / "mem-fixture.bundle.json"
    with bundle_path.open("w", encoding="utf-8") as fh:
        json.dump(bundle, fh, ensure_ascii=False, separators=(",", ":"))

    inline = sum(
        len(rp["source"]["data"])
        for t in bundle["turns"]
        for m in t["messages"]
        for p in m["content"]["parts"]
        if p["type"] == "tool_result"
        for rp in p["result"]["content"]["parts"]
        if rp["type"] == "image"
    )
    big_text = sum(
        len(rp["text"])
        for t in bundle["turns"]
        for m in t["messages"]
        for p in m["content"]["parts"]
        if p["type"] == "tool_result"
        for rp in p["result"]["content"]["parts"]
        if rp["type"] == "text"
    )
    size = bundle_path.stat().st_size
    print(f"  bundle: {bundle_path} ({size / 1048576:.1f} MiB, {len(bundle['turns'])} turns)")
    print(f"  inline frames loaded when fully paged back: {inline / 1048576:.1f} MiB of base64 "
          f"(the 16 MiB media budget must shed the oldest)")
    print(f"  tool-result text: {big_text / 1048576:.1f} MiB raw "
          f"(the 256 KiB per-result cap and the 16 MiB text budget both engage)")
    (root / "README.md").write_text(README, encoding="utf-8")
    print(f"\nworkspace: {root}\nbundle:    {bundle_path}\nnow open it in the app (see README.md)")


if __name__ == "__main__":
    main()
