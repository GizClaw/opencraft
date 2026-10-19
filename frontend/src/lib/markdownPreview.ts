// markdownPreview answers the leading part of a long markdown block,
// which is what the transcript shows before the reader asks for the rest.
//
// It exists because the cost of an answer is paid per character, not per
// message: react-markdown turns every inline span, list item and code line
// into its own element and text node (a 10k-character answer measured
// ~890 elements and ~1240 text nodes), and every one of those is a WebKit
// object that outlives the row. A transcript that renders its history
// whole, into a virtualized list the reader scrolls back through, is where
// a session's renderer memory went. Rendering the first screenful of a
// settled block and folding the rest behind one click scales the cost with
// what is on screen instead of what was written.
//
// The cut has to keep the preview renderable as the markdown it was: a
// fold in the middle of a paragraph is fine, a fold that leaves a fenced
// code block open is not (everything after it would render as code), a
// fold in the middle of a line can turn a path into an accidental link,
// and a fold right below a heading leaves a title with nothing under it.
// So the cut is taken at a line boundary outside a fence, preferring the
// blank line that ends a section, and never one that follows a heading.

// MarkdownPreview is one folded block: what to render now, and how much
// text waits behind it.
export interface MarkdownPreview {
  shown: string;
  hidden: number;
}

// FenceMarker is the fence a line opens or closes.
interface FenceMarker {
  char: string;
  length: number;
}

// markdownPreview folds text down to about `limit` characters, or answers
// null when there is nothing to fold. `limit` is a budget, not a promise:
// the cut backs up to the nearest section boundary below it, and a block
// with no boundary to cut at keeps its first line whole (or the raw limit,
// with its fence closed).
export function markdownPreview(
  text: string,
  limit: number,
): MarkdownPreview | null {
  if (text.length <= limit) return null;
  const { cut, fence } = foldPoint(text, limit);
  let shown = text.slice(0, cut).replace(/\s+$/, '');
  // A block with no line boundary below the limit is cut mid-line; when
  // that line is inside a fence, the fence is closed so the preview still
  // reads as code instead of swallowing everything below it. The closer
  // has to be the opener's own character: a ``` does not close a ~~~.
  if (fence) shown += `\n${fence.char.repeat(fence.length)}`;
  return { shown, hidden: Math.max(0, text.length - cut) };
}

// foldPoint finds where to cut: the last blank line (a section boundary)
// that ends at or below the limit and outside a fence, else the last such
// line, else the limit itself. The fence it answers with is the one that
// last resort landed inside — the caller has to close it.
function foldPoint(
  text: string,
  limit: number,
): { cut: number; fence: FenceMarker | null } {
  let fence: FenceMarker | null = null;
  // The last two usable section boundaries, and the last line of content:
  // a boundary that follows a heading is only taken when there is an
  // earlier one to back up to.
  let section = 0;
  let previousSection = 0;
  let sectionAfterHeading = false;
  let line = 0;
  let lastContent = '';
  let fenceAtLimit: FenceMarker | null = null;
  let start = 0;
  for (;;) {
    const newline = text.indexOf('\n', start);
    const end = newline < 0 ? text.length : newline;
    const after = newline < 0 ? end : end + 1;
    if (start <= limit && limit < after) fenceAtLimit = fence;
    const body = text.slice(start, end);
    const marker = fenceMarker(body);
    if (marker) {
      if (!fence) {
        fence = marker;
      } else if (marker.char === fence.char && marker.length >= fence.length) {
        fence = null;
      }
    } else if (!fence && after <= limit) {
      line = after;
      if (body.trim() === '') {
        previousSection = section;
        section = after;
        sectionAfterHeading = isHeading(lastContent);
      } else {
        lastContent = body;
      }
    }
    if (newline < 0) break;
    start = after;
  }
  if (section > 0) {
    const cut =
      sectionAfterHeading && previousSection > 0 ? previousSection : section;
    return { cut, fence: null };
  }
  if (line > 0) return { cut: line, fence: null };
  return { cut: limit, fence: fenceAtLimit };
}

// fenceMarker reads the fence a line opens (or closes). CommonMark allows
// up to three spaces of indentation and requires the run to be at least
// three characters of one kind; a longer run can only be closed by one at
// least as long, which is what the caller checks.
function fenceMarker(line: string): FenceMarker | null {
  const match = /^ {0,3}(`{3,}|~{3,})/.exec(line);
  if (!match) return null;
  return { char: match[1][0], length: match[1].length };
}

// isHeading recognizes an ATX heading: a preview that ends on one shows a
// title whose body is behind the fold.
function isHeading(line: string): boolean {
  return /^ {0,3}#{1,6}\s/.test(line);
}
