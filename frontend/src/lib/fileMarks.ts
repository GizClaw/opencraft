// The per-line git change layout of one previewed file.
//
// The binding hands over ranges in the working-tree coordinates git
// diffed against HEAD; this module is the one place that clips them
// onto the document the viewer actually renders. The two can disagree —
// the editor holds a payload read a moment before the marks were
// computed, and the file may have been rewritten in between — so every
// range is treated as untrusted and clamped, never indexed directly.
import type { GitFileMarks, GitLineRange, MarkTone } from './types';

/** LineDeletes counts deleted lines whose gap sits at one line edge. */
export interface LineDeletes {
  /** Above this line: the gap git reported before it. */
  top: number;
  /** Below the last line of the document. */
  bottom: number;
}

/** MarkLayout is the marks of one document: one entry per line. */
export interface MarkLayout {
  tones: (MarkTone | null)[];
  deletes: LineDeletes[];
}

export function emptyLayout(lineCount: number): MarkLayout {
  const lines = Math.max(0, lineCount);
  return {
    tones: new Array<MarkTone | null>(lines).fill(null),
    deletes: Array.from({ length: lines }, () => ({ top: 0, bottom: 0 })),
  };
}

/**
 * classifyMarks maps the binding's ranges onto a document of
 * `lineCount` lines. Ranges outside the document are dropped, and a
 * deletion gap that lands on the document edge is folded onto the first
 * or last line, because a gutter can only draw next to a line.
 */
export function classifyMarks(
  marks: GitFileMarks | null | undefined,
  lineCount: number,
): MarkLayout {
  const layout = emptyLayout(lineCount);
  if (!marks || lineCount <= 0) return layout;
  for (const range of marks.adds ?? []) {
    paint(layout.tones, range, 'add');
  }
  // Modifications win over insertions on an overlap: a replaced line is
  // a change, not an addition.
  for (const range of marks.mods ?? []) {
    paint(layout.tones, range, 'mod');
  }
  for (const del of marks.dels ?? []) {
    // `after` is an anchor, not an index: it is clamped and truncated
    // the way paint() treats its ranges, because a fractional or
    // non-finite value would make `deletes[after]` undefined and throw
    // during the editor's render (a render error no boundary catches).
    const count = Math.trunc(del.count);
    if (!Number.isFinite(count) || count <= 0) continue;
    const after = Math.trunc(del.after);
    if (!Number.isFinite(after) || after <= 0) {
      layout.deletes[0].top += count;
      continue;
    }
    if (after >= lineCount) {
      layout.deletes[lineCount - 1].bottom += count;
      continue;
    }
    layout.deletes[after].top += count;
  }
  return layout;
}

/**
 * layoutSignature is a stable string for a layout, so a render can skip
 * reconfiguring the editor when a poll returned the same answer.
 */
export function layoutSignature(layout: MarkLayout): string {
  let out = '';
  for (const tone of layout.tones)
    out += tone === 'add' ? 'a' : tone === 'mod' ? 'm' : '.';
  for (const del of layout.deletes) out += `|${del.top},${del.bottom}`;
  return out;
}

/** marksSignature compares two binding payloads by value. */
export function marksSignature(marks: GitFileMarks | null): string {
  return marks === null ? '' : JSON.stringify(marks);
}

function paint(
  tones: (MarkTone | null)[],
  range: GitLineRange,
  tone: MarkTone,
): void {
  if (!range || range.count <= 0) return;
  const start = Math.max(1, Math.trunc(range.start));
  const end = Math.min(tones.length, start + Math.trunc(range.count) - 1);
  for (let line = start; line <= end; line += 1) {
    tones[line - 1] = tone;
  }
}

/** lineCountOf counts the lines a rendered payload holds. */
export function lineCountOf(text: string): number {
  let count = 1;
  for (let i = 0; i < text.length; i += 1) {
    if (text.charCodeAt(i) === 10) count += 1;
  }
  return count;
}
