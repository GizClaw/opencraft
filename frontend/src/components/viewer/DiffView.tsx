// DiffView renders git-style unified diffs shared by tool cards and
// the Git panel. Parsed PatchFileDTO rows come from lib/diff.ts.
//
// Two column layouts:
//   wrap=false (panels)  keeps one visual line per source line and
//                        scrolls horizontally; each row is sized to its
//                        longest line (w-max + min-w-full) so the
//                        add/delete tint covers the text that is
//                        scrolled to instead of stopping at the
//                        viewport edge.
//   wrap=true (chat)     wraps long lines under the code column and
//                        never scrolls sideways: the +/- glyph gets its
//                        own column, so continuation lines align with
//                        the code rather than under the marker.
import { useEffect, useRef, useState } from 'react';
import {
  ChevronDown,
  ChevronRight,
  ChevronUp,
  FileMinus2,
  FilePenLine,
  FilePlus2,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useStore } from '../../lib/store';
import type { PatchFileDTO, PatchLineDTO } from '../../lib/types';
import { ICON } from '../ui/icon';

interface DiffHunk {
  oldStart: number;
  oldCount: number;
  newStart: number;
  newCount: number;
  lines: PatchLineDTO[];
}

// groupHunks splits rendered diff lines into contiguous hunks (line
// numbering restarts after each hunk), so each one renders a git-style
// "@@ -a,b +c,d @@" header.
function groupHunks(lines: PatchLineDTO[]): DiffHunk[] {
  const hunks: DiffHunk[] = [];
  let cur: DiffHunk | null = null;
  let prevOld = 0;
  let prevNew = 0;
  for (const line of lines) {
    const oldNum = line.old_num ?? 0;
    const newNum = line.new_num ?? 0;
    const oldBreak = oldNum > 0 && prevOld > 0 && oldNum !== prevOld + 1;
    const newBreak = newNum > 0 && prevNew > 0 && newNum !== prevNew + 1;
    if (!cur || oldBreak || newBreak) {
      cur = {
        oldStart: oldNum,
        oldCount: 0,
        newStart: newNum,
        newCount: 0,
        lines: [],
      };
      hunks.push(cur);
    }
    if (oldNum > 0) {
      if (!cur.oldStart) cur.oldStart = oldNum;
      cur.oldCount++;
    }
    if (newNum > 0) {
      if (!cur.newStart) cur.newStart = newNum;
      cur.newCount++;
    }
    cur.lines.push(line);
    prevOld = oldNum;
    prevNew = newNum;
  }
  return hunks;
}

function hunkHeader(h: DiffHunk): string {
  const fmt = (start: number, count: number) =>
    count === 1 ? String(start) : `${start},${count}`;
  return `@@ -${fmt(h.oldStart, h.oldCount)} +${fmt(h.newStart, h.newCount)} @@`;
}

function GitDiffLine({ line, wrap }: { line: PatchLineDTO; wrap: boolean }) {
  const oldCol = (line.old_num ?? 0) > 0 ? String(line.old_num) : '';
  const newCol = (line.new_num ?? 0) > 0 ? String(line.new_num) : '';
  const marker = line.kind === 'add' ? '+' : line.kind === 'delete' ? '-' : '';
  const isAdd = line.kind === 'add';
  const isDel = line.kind === 'delete';
  const numBg = isAdd ? 'bg-ok/15' : isDel ? 'bg-err/15' : 'bg-panel2';
  const lineBg = isAdd ? 'bg-ok/10' : isDel ? 'bg-err/10' : '';
  const markerCls = isAdd ? 'text-ok' : isDel ? 'text-err' : 'text-dim';
  return (
    <div
      className={`grid grid-cols-[4rem_4rem_1rem_minmax(0,1fr)] font-mono text-xs leading-5 ${lineBg} ${
        wrap ? 'w-full' : 'w-max min-w-full'
      }`}
    >
      <div
        className={`select-none px-2 text-right text-dim tabular-nums ${numBg}`}
      >
        {oldCol}
      </div>
      <div
        className={`select-none border-r border-edge/40 px-2 text-right text-dim tabular-nums ${numBg}`}
      >
        {newCol}
      </div>
      <div
        className={`select-none text-center ${markerCls}`}
        aria-hidden="true"
      >
        {marker}
      </div>
      <div
        className={`px-2 text-fg ${
          wrap ? 'whitespace-pre-wrap break-words' : 'whitespace-pre'
        }`}
      >
        {line.text}
      </div>
    </div>
  );
}

// ACTION_ICON marks how a file changed with a glyph instead of a text
// badge: the shape reads at a glance and keeps the header one line
// high. Unknown actions fall back to the edit glyph.
const ACTION_ICON = {
  add: FilePlus2,
  update: FilePenLine,
  delete: FileMinus2,
} as const;

function FileHeader({
  file,
  open,
  onToggle,
  collapsible,
}: {
  file: PatchFileDTO;
  open: boolean;
  onToggle: () => void;
  collapsible: boolean;
}) {
  const openFileTarget = useStore((s) => s.openFileTarget);
  const ActionIcon =
    ACTION_ICON[file.action as keyof typeof ACTION_ICON] ?? FilePenLine;
  const iconCls =
    file.action === 'add'
      ? 'text-ok'
      : file.action === 'delete'
        ? 'text-err'
        : 'text-accent';
  return (
    <div className="sticky top-0 z-[var(--oc-z-raised)] flex items-center gap-2 border-b border-edge bg-panel px-2 py-1.5">
      {collapsible && (
        <button
          onClick={onToggle}
          className="text-dim hover:text-fg"
          data-tip={open ? 'Collapse' : 'Expand'}
        >
          {open ? (
            <ChevronDown size={ICON.sm} />
          ) : (
            <ChevronRight size={ICON.sm} />
          )}
        </button>
      )}
      <ActionIcon size={ICON.sm} className={`shrink-0 ${iconCls}`} />
      <button
        type="button"
        onClick={() => void openFileTarget(file.path)}
        className="min-w-0 truncate text-left font-mono text-xs text-fg hover:text-accent"
        data-tip={file.path}
      >
        {file.path}
      </button>
      <span className="flex-1" />
      <span className="text-micro text-ok tabular-nums">+{file.added}</span>
      <span className="text-micro text-err tabular-nums">−{file.removed}</span>
    </div>
  );
}

function FileDiff({
  file,
  collapsible,
  wrap,
  maxLines,
  showAllLines,
  onShowAllLines,
}: {
  file: PatchFileDTO;
  collapsible: boolean;
  wrap: boolean;
  // maxLines caps how many of the file's lines render at once; the
  // footer offers the rest. Chat surfaces pass it so a multi-thousand
  // line patch cannot mount one row per line just because a turn's
  // process rows were expanded.
  maxLines?: number;
  showAllLines?: boolean;
  onShowAllLines?: () => void;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(true);
  const allLines = file.lines ?? [];
  const truncated =
    maxLines !== undefined && !showAllLines && allLines.length > maxLines;
  const lines = truncated ? allLines.slice(0, maxLines) : allLines;
  const hunks = groupHunks(lines);
  return (
    <div className="border-b border-edge last:border-b-0">
      <FileHeader
        file={file}
        open={open}
        onToggle={() => setOpen((v) => !v)}
        collapsible={collapsible}
      />
      {(!collapsible || open) && (
        <>
          {hunks.length === 0 ? (
            <div className="px-3 py-1.5 font-mono text-micro text-dim">
              {t('tool.diffUnavailable')}
            </div>
          ) : (
            hunks.map((h, i) => (
              <div key={i}>
                <div className="select-none bg-panel2 px-3 py-0.5 text-center font-mono text-micro text-dim">
                  {hunkHeader(h)}
                </div>
                {h.lines.map((line, j) => (
                  <GitDiffLine key={j} line={line} wrap={wrap} />
                ))}
              </div>
            ))
          )}
          {truncated && (
            <div className="flex items-center justify-end gap-2 border-t border-edge/60 px-3 py-1 text-micro text-dim">
              <span className="tabular-nums">
                {t('tool.moreLines', { count: allLines.length - lines.length })}
              </span>
              <button
                type="button"
                onClick={onShowAllLines}
                className="rounded-tight border border-edge px-1.5 py-0.5 hover:text-fg"
              >
                {t('tool.showAll')}
              </button>
            </div>
          )}
        </>
      )}
    </div>
  );
}

export function GitDiffView({
  files,
  collapsible = true,
  wrap = false,
  framed = true,
  maxHeight = 'max-h-80',
  expandable = false,
  maxLines,
}: {
  files: PatchFileDTO[];
  collapsible?: boolean;
  // wrap keeps long lines inside the viewport instead of scrolling
  // sideways; chat surfaces set it, wide panels leave it off.
  wrap?: boolean;
  // framed draws the viewport chrome (border + card background). Chat
  // cards host the diff inside their own shell, so they pass false.
  framed?: boolean;
  maxHeight?: string;
  // expandable adds a "show full diff" footer once the viewport hides
  // lines. Panels that fill a whole pane leave it off: they never hide
  // more than the pane already scrolls.
  expandable?: boolean;
  // maxLines caps the lines rendered per file; see FileDiff.
  maxLines?: number;
}) {
  const { t } = useTranslation();
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const [expanded, setExpanded] = useState(false);
  const [showAllLines, setShowAllLines] = useState(false);
  const [overflowing, setOverflowing] = useState(false);

  // Track the real scroll box so the footer only appears when lines are
  // actually hidden (a short patch must not offer to expand).
  useEffect(() => {
    const el = scrollRef.current;
    if (!el || expanded) return;
    const check = () => setOverflowing(el.scrollHeight - el.clientHeight > 8);
    check();
    const observer = new ResizeObserver(check);
    observer.observe(el);
    return () => observer.disconnect();
  }, [expanded, files]);

  const fading = expandable && overflowing && !expanded;
  // A patch may touch one path twice (rewrite, delete+add), so the path
  // alone is not a unique key: duplicate React keys make it drop
  // siblings on the next re-render.
  const fileViews = files.map((f, i) => (
    <FileDiff
      key={`${f.path}#${i}`}
      file={f}
      collapsible={collapsible}
      wrap={wrap}
      maxLines={maxLines}
      showAllLines={showAllLines}
      onShowAllLines={() => setShowAllLines(true)}
    />
  ));
  const frameCls = framed
    ? 'rounded-control border border-edge bg-panel'
    : 'bg-panel';
  // Non-expandable surfaces (the Git panels) keep the scroll box as the
  // root element: they size it with h-full against a definite-height
  // parent, which an extra wrapper would break.
  if (!expandable) {
    return (
      <div
        ref={scrollRef}
        className={`${maxHeight} ${
          wrap ? 'overflow-x-hidden' : 'overflow-x-auto'
        } overflow-y-auto ${frameCls}`}
      >
        {fileViews}
      </div>
    );
  }
  return (
    <div className={framed ? `overflow-hidden ${frameCls}` : 'bg-panel'}>
      <div className="relative">
        <div
          ref={scrollRef}
          className={`${expanded ? '' : maxHeight} ${
            wrap ? 'overflow-x-hidden' : 'overflow-x-auto'
          } overflow-y-auto ${framed ? '' : 'bg-panel'}`}
        >
          {fileViews}
        </div>
        {fading && (
          // A painted scrim, not a CSS mask: a mask on a scroll box that
          // grows by thousands of pixels on expand risks the engine
          // skipping the repaint of the newly revealed area.
          <div aria-hidden="true" className="diff-fade-bottom" />
        )}
      </div>
      {(overflowing || expanded) && (
        <button
          type="button"
          onClick={() => setExpanded((v) => !v)}
          className="flex w-full items-center justify-center gap-1 border-t border-edge bg-panel2 px-2 py-1 text-micro text-dim transition-colors hover:text-fg"
        >
          {expanded ? (
            <ChevronUp size={ICON.xs} />
          ) : (
            <ChevronDown size={ICON.xs} />
          )}
          {expanded ? t('tool.collapseDiff') : t('tool.showFullDiff')}
        </button>
      )}
    </div>
  );
}
