// DiffView renders git-style unified diffs shared by tool cards and
// the Git panel. Parsed PatchFileDTO rows come from lib/diff.ts.
import { useState } from 'react';
import { ChevronDown, ChevronRight } from 'lucide-react';
import { useStore } from '../../lib/store';
import type { PatchFileDTO, PatchLineDTO } from '../../lib/types';

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

function GitDiffLine({ line }: { line: PatchLineDTO }) {
  const oldCol = (line.old_num ?? 0) > 0 ? String(line.old_num) : '';
  const newCol = (line.new_num ?? 0) > 0 ? String(line.new_num) : '';
  const marker = line.kind === 'add' ? '+' : line.kind === 'delete' ? '-' : ' ';
  const isAdd = line.kind === 'add';
  const isDel = line.kind === 'delete';
  const numBg = isAdd ? 'bg-ok/15' : isDel ? 'bg-err/15' : 'bg-panel2/50';
  const lineBg = isAdd ? 'bg-ok/10' : isDel ? 'bg-err/10' : '';
  const markerCls = isAdd ? 'text-ok' : isDel ? 'text-err' : 'text-dim';
  return (
    <div
      className={`grid grid-cols-[4rem_4rem_minmax(0,1fr)] font-mono text-xs leading-5 ${lineBg}`}
    >
      <div
        className={`select-none px-2 text-right text-dim tabular-nums ${numBg}`}
      >
        {oldCol}
      </div>
      <div
        className={`select-none px-2 text-right text-dim tabular-nums ${numBg}`}
      >
        {newCol}
      </div>
      <div className="whitespace-pre px-2 text-fg">
        <span className={`select-none ${markerCls}`}>{marker}</span>
        {line.text}
      </div>
    </div>
  );
}

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
  return (
    <div className="sticky top-0 z-10 flex items-center gap-2 border-b border-edge bg-panel px-2 py-1.5">
      {collapsible && (
        <button
          onClick={onToggle}
          className="text-dim hover:text-fg"
          title={open ? 'Collapse' : 'Expand'}
        >
          {open ? (
            <ChevronDown size="0.9286rem" />
          ) : (
            <ChevronRight size="0.9286rem" />
          )}
        </button>
      )}
      <button
        type="button"
        onClick={() => void openFileTarget(file.path)}
        className="min-w-0 truncate text-left font-mono text-xs text-fg hover:text-accent"
        title={file.path}
      >
        {file.path}
      </button>
      <span className="flex-1" />
      <span className="text-[0.7143rem] text-ok tabular-nums">
        +{file.added}
      </span>
      <span className="text-[0.7143rem] text-err tabular-nums">
        −{file.removed}
      </span>
    </div>
  );
}

function FileDiff({
  file,
  collapsible,
}: {
  file: PatchFileDTO;
  collapsible: boolean;
}) {
  const [open, setOpen] = useState(true);
  const hunks = groupHunks(file.lines ?? []);
  return (
    <div className="border-b border-edge last:border-b-0">
      <FileHeader
        file={file}
        open={open}
        onToggle={() => setOpen((v) => !v)}
        collapsible={collapsible}
      />
      {(!collapsible || open) &&
        hunks.map((h, i) => (
          <div key={i}>
            <div className="select-none bg-panel2/60 px-3 py-0.5 text-center font-mono text-[0.7143rem] text-accent">
              {hunkHeader(h)}
            </div>
            {h.lines.map((line, j) => (
              <GitDiffLine key={j} line={line} />
            ))}
          </div>
        ))}
    </div>
  );
}

export function GitDiffView({
  files,
  collapsible = true,
  maxHeight = 'max-h-80',
}: {
  files: PatchFileDTO[];
  collapsible?: boolean;
  maxHeight?: string;
}) {
  return (
    <div
      className={`${maxHeight} overflow-y-auto rounded-lg border border-edge bg-panel/60`}
    >
      {files.map((f) => (
        <FileDiff key={f.path} file={f} collapsible={collapsible} />
      ))}
    </div>
  );
}
