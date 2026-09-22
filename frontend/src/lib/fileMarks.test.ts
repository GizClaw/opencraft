// The marks layout is the one place a git answer meets a document that
// may have moved on since: the ranges are clamped, never trusted. These
// cases pin that arithmetic down, because a wrong index paints a mark
// onto an unrelated line and nothing downstream can tell.
import { describe, expect, it } from 'vitest';
import {
  classifyMarks,
  layoutSignature,
  lineCountOf,
  marksSignature,
} from './fileMarks';
import type { GitFileMarks } from './types';

function marks(over: Partial<GitFileMarks> = {}): GitFileMarks {
  return {
    in_repo: true,
    path: 'internal/a.go',
    kind: 'modified',
    staged: false,
    unstaged: true,
    untracked: false,
    unmerged: false,
    binary: false,
    truncated: false,
    additions: 0,
    deletions: 0,
    mtime_ns: 1,
    size: 10,
    ...over,
  };
}

describe('classifyMarks', () => {
  it('paints inserted and replaced runs onto their lines', () => {
    const layout = classifyMarks(
      marks({
        adds: [{ start: 2, count: 2 }],
        mods: [{ start: 5, count: 1 }],
      }),
      6,
    );
    expect(layout.tones).toEqual([null, 'add', 'add', null, 'mod', null]);
  });

  it('keeps the previous file blank instead of guessing', () => {
    const layout = classifyMarks(null, 2);
    expect(layout.tones).toEqual([null, null]);
    expect(layout.deletes).toEqual([
      { top: 0, bottom: 0 },
      { top: 0, bottom: 0 },
    ]);
  });

  it('drops ranges the document no longer holds and clamps the rest', () => {
    const layout = classifyMarks(
      marks({
        adds: [{ start: 4, count: 10 }],
        mods: [{ start: 0, count: 2 }],
      }),
      5,
    );
    // start 0 is clamped to line 1; the rest of the add run falls off
    // the end of the document.
    expect(layout.tones).toEqual(['mod', 'mod', null, 'add', 'add']);
  });

  it('lets a replacement win over an insertion on the same line', () => {
    const layout = classifyMarks(
      marks({
        adds: [{ start: 1, count: 2 }],
        mods: [{ start: 2, count: 1 }],
      }),
      2,
    );
    expect(layout.tones).toEqual(['add', 'mod']);
  });

  it('hangs a deletion gap off the line that follows it', () => {
    const layout = classifyMarks(marks({ dels: [{ after: 2, count: 3 }] }), 4);
    expect(layout.deletes[2]).toEqual({ top: 3, bottom: 0 });
  });

  it('folds edge gaps onto the first and last line', () => {
    const layout = classifyMarks(
      marks({
        dels: [
          { after: 0, count: 1 },
          { after: 9, count: 2 },
        ],
      }),
      4,
    );
    expect(layout.deletes[0].top).toBe(1);
    expect(layout.deletes[3].bottom).toBe(2);
    expect(layout.deletes[3].top).toBe(0);
  });

  it('sums several gaps on one line and ignores empty ones', () => {
    const layout = classifyMarks(
      marks({
        dels: [
          { after: 1, count: 2 },
          { after: 1, count: 1 },
          { after: 2, count: 0 },
        ],
      }),
      4,
    );
    expect(layout.deletes[1].top).toBe(3);
    expect(layout.deletes[2].top).toBe(0);
  });

  it('answers a zero-line document without touching the array', () => {
    const layout = classifyMarks(marks({ adds: [{ start: 1, count: 3 }] }), 0);
    expect(layout.tones).toEqual([]);
    expect(layout.deletes).toEqual([]);
  });
});

describe('layoutSignature', () => {
  it('is stable for equal layouts and changes with the tones', () => {
    const a = classifyMarks(marks({ adds: [{ start: 1, count: 1 }] }), 3);
    const b = classifyMarks(marks({ adds: [{ start: 1, count: 1 }] }), 3);
    const c = classifyMarks(marks({ mods: [{ start: 1, count: 1 }] }), 3);
    expect(layoutSignature(a)).toBe(layoutSignature(b));
    expect(layoutSignature(a)).not.toBe(layoutSignature(c));
  });

  it('carries the deletion gaps', () => {
    const none = classifyMarks(marks(), 2);
    const gap = classifyMarks(marks({ dels: [{ after: 1, count: 1 }] }), 2);
    expect(layoutSignature(none)).not.toBe(layoutSignature(gap));
  });
});

describe('marksSignature', () => {
  it('is empty for no marks and by-value for a payload', () => {
    expect(marksSignature(null)).toBe('');
    const one = marks({ adds: [{ start: 1, count: 1 }] });
    expect(marksSignature(one)).toBe(marksSignature({ ...one }));
    expect(marksSignature(one)).not.toBe(
      marksSignature({ ...one, mtime_ns: 2 }),
    );
  });
});

describe('lineCountOf', () => {
  it('counts the trailing empty line the editor shows', () => {
    expect(lineCountOf('')).toBe(1);
    expect(lineCountOf('a')).toBe(1);
    expect(lineCountOf('a\n')).toBe(2);
    expect(lineCountOf('a\nb\nc')).toBe(3);
  });
});
