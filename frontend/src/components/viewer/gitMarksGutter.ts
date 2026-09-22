// The git change gutter of the file preview: a narrow strip left of the
// line numbers with one mark per changed line — green for inserted
// lines, blue for replaced ones, and a red wedge where lines were
// deleted (a deletion has no line of its own, so the wedge sits at the
// gap: above the following line, or at the document's top/bottom edge).
//
// Kept out of CodePane's module graph on purpose: it is the only part
// of the viewer that pulls @codemirror/view at render time, and the
// pane itself must stay cheap for the preview dialog.
import { GutterMarker, gutter } from '@codemirror/view';
import { Prec } from '@codemirror/state';
import type { Extension } from '@codemirror/state';
import type { MarkLayout } from '../../lib/fileMarks';

/** The gutter's CSS class; the styles live in style.css. */
export const MARKS_GUTTER_CLASS = 'oc-git-marks';

class LineMarks extends GutterMarker {
  constructor(
    readonly tone: 'add' | 'mod' | null,
    readonly delTop: number,
    readonly delBottom: number,
    readonly tip: string,
  ) {
    super();
  }

  eq(other: LineMarks): boolean {
    return (
      other.tone === this.tone &&
      other.delTop === this.delTop &&
      other.delBottom === this.delBottom &&
      other.tip === this.tip
    );
  }

  toDOM(): HTMLElement {
    const el = document.createElement('span');
    const classes = ['oc-gm'];
    if (this.tone) classes.push(`oc-gm-${this.tone}`);
    if (this.delTop > 0) classes.push('oc-gm-del-top');
    if (this.delBottom > 0) classes.push('oc-gm-del-bottom');
    el.className = classes.join(' ');
    if (this.tip) el.dataset.tip = this.tip;
    return el;
  }
}

// spacer reserves the column's width on an otherwise empty gutter, so
// the line numbers do not shift when the first mark appears.
const spacer = new LineMarks(null, 0, 0, '');

/**
 * marksGutter draws `layout` next to the lines of the document it was
 * computed from, with `tip` supplying the hover text of one mark. An
 * empty layout yields no extension at all: an unused gutter would still
 * cost a column of padding.
 */
export function marksGutter(
  layout: MarkLayout,
  tip: (
    tone: 'add' | 'mod' | null,
    delTop: number,
    delBottom: number,
  ) => string,
): Extension[] {
  const { tones, deletes } = layout;
  const hasMarks =
    tones.some((tone) => tone !== null) ||
    deletes.some((del) => del.top > 0 || del.bottom > 0);
  if (!hasMarks) return [];
  return [
    // High precedence so the strip lands left of the line numbers; the
    // gutter order follows extension precedence.
    Prec.high(
      gutter({
        class: MARKS_GUTTER_CLASS,
        initialSpacer: () => spacer,
        lineMarker(view, line) {
          // The document can lag the layout for one render; an index
          // past it simply has nothing to draw.
          const number = view.state.doc.lineAt(line.from).number;
          const tone = tones[number - 1] ?? null;
          const del = deletes[number - 1];
          const top = del?.top ?? 0;
          const bottom = del?.bottom ?? 0;
          if (!tone && top === 0 && bottom === 0) return null;
          return new LineMarks(tone, top, bottom, tip(tone, top, bottom));
        },
      }),
    ),
  ];
}
