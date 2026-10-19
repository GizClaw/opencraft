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
// blank line that ends a section, and never one that follows a heading -
// but never so far back that the "preview" is the first line and the fold
// hides the rest: a boundary that sits more than a quarter of the budget
// below the limit is passed over for the line at the limit (see
// sectionWithin).
//
// One thing the cut must not change is what the shown part means: a
// reference-style link keeps its definition at the end of the block (and
// a footnote its note), so the definitions the fold leaves behind travel
// with the preview.

// MarkdownPreview is one folded block: what to render now. How much waits
// behind the fold is the block's own length, which the caller has.
export interface MarkdownPreview {
  shown: string;
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
  // fenceMarker reads fences at the document's own indentation, so one
  // inside a list item (4 spaces in) or a quote (`> ```` ) is not
  // recognized, and the cut lands mid-fence - which remark still renders
  // as code to the end, so the preview reads right without a closer.
  if (fence) shown += `\n${fence.char.repeat(fence.length)}`;
  // The hidden part may hold the definitions the shown part links to.
  // Without them remark renders `[guide]` as literal text, which is the
  // one way a fold would change what the preview says, not just how much
  // of it is there. They are a handful of lines in a block of thousands.
  const definitions = definitionsAfter(text, cut, fence);
  if (definitions) shown += `\n\n${definitions}`;
  return { shown };
}

// foldPoint finds where to cut: the last blank line (a section boundary)
// that ends at or below the limit and outside a fence, else the last line
// that does, else the limit itself. Both boundaries have to be worth
// taking (see sectionWithin below). The fence it answers with is the one
// the last resort landed inside — the caller has to close it.
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
  // The last two non-blank lines, so a setext underline can be recognized
  // by the title above it.
  let lastContent = '';
  let contentBefore = '';
  let fenceAtLimit: FenceMarker | null = null;
  let start = 0;
  // The scan stops at the limit: `line` and `section` are both gated on
  // ending below it, and the fence the last resort cuts inside is the one
  // in effect at the limit, which this reads before moving past it.
  while (start <= limit) {
    const newline = text.indexOf('\n', start);
    const end = newline < 0 ? text.length : newline;
    const after = newline < 0 ? end : end + 1;
    // The cut's own line decides the fence at the cut: it can sit inside
    // an opener (the preview keeps code up to the limit) as well as inside
    // a closer (the block ends before it).
    if (start <= limit && limit < after) {
      fenceAtLimit = fenceAtCut(fence, text.slice(start, limit));
    }
    const body = text.slice(start, end);
    const marker = fenceMarker(body);
    if (marker) {
      if (!fence) {
        fence = marker;
      } else if (fenceCloses(body, fence)) {
        fence = null;
      }
    } else if (!fence && after <= limit) {
      line = after;
      if (body.trim() === '') {
        previousSection = section;
        section = after;
        sectionAfterHeading = isHeading(lastContent, contentBefore);
      } else {
        contentBefore = lastContent;
        lastContent = body;
      }
    }
    if (newline < 0) break;
    start = after;
  }
  // A boundary that follows a heading is only worth taking when there is an
  // earlier one to back up to: on its own it leaves the preview a title
  // with nothing under it, and cutting at the limit (which keeps the body
  // below the title) is the better answer.
  let boundary = 0;
  if (section > 0) {
    const candidate = sectionAfterHeading ? previousSection : section;
    if (sectionWithin(limit, candidate)) boundary = candidate;
  }
  if (boundary > 0) return { cut: boundary, fence: null };
  // The same line is not a second candidate: when the boundary above was
  // the last line inside the limit, cutting there is what it refused.
  if (line > 0 && line !== section && sectionWithin(limit, line)) {
    return { cut: line, fence: null };
  }
  return { cut: limit, fence: fenceAtLimit };
}

// sectionWithin says whether a boundary is close enough below the limit to
// be worth cutting at. A run of prose over 2k characters without a blank
// line, a heading and one paragraph, a long table: at all of those the
// nearest boundary can sit hundreds of characters in, and the fold would
// answer a 3000-character answer with its first line. A quarter of the
// budget is the point below which cutting mid-line is the smaller lie.
function sectionWithin(limit: number, cut: number): boolean {
  return cut >= limit / 4;
}

// definitionsAfter collects the link and footnote definitions from the
// part of the block the fold hides, so the preview keeps resolving the
// references in it. Fenced lines are skipped: a definition inside a code
// block is code, and appending it would point a link at it.
//
// openFence is the fence still open at the cut, if the last-resort cut
// landed inside one: the scan starts inside that block, so its lines stay
// code until this scanner reads the closer.
function definitionsAfter(
  text: string,
  cut: number,
  openFence: FenceMarker | null,
): string {
  let out = '';
  let fence: FenceMarker | null = openFence;
  let start = cut;
  while (start < text.length) {
    const newline = text.indexOf('\n', start);
    const end = newline < 0 ? text.length : newline;
    const marker = fenceMarkerAt(text, start, end);
    if (marker) {
      if (!fence) {
        fence = marker;
      } else if (fenceCloses(text.slice(start, end), fence)) {
        fence = null;
      }
    } else if (!fence && isDefinition(text, start, end)) {
      out += `${text.slice(start, end).trimEnd()}\n`;
    }
    if (newline < 0) break;
    start = end + 1;
  }
  return out.trimEnd();
}

// fenceMarkerAt is fenceMarker for a line that may not be one: the tail of
// a long block is scanned line by line and almost none of it is a fence,
// so the two characters that could start one are read before the line is
// sliced.
function fenceMarkerAt(
  text: string,
  start: number,
  end: number,
): FenceMarker | null {
  let i = start;
  while (i < end && i - start < 3 && text[i] === ' ') i += 1;
  if (i >= end || (text[i] !== '`' && text[i] !== '~')) return null;
  return fenceMarker(text.slice(start, end));
}

// isDefinition reads one reference or footnote definition line without
// slicing it: `[label]: ...` behind at most three spaces of indentation.
function isDefinition(text: string, start: number, end: number): boolean {
  let i = start;
  while (i < end && i - start < 3 && text[i] === ' ') i += 1;
  if (i >= end || text[i] !== '[') return false;
  // The label has to close on this line, and the scan stops where the line
  // does: a tail of `[` lines would otherwise cost the rest of the block
  // per line, and the block is the fold's whole tail.
  for (let close = i + 2; close + 1 < end; close += 1) {
    if (text[close] === ']' && text[close + 1] === ':') return true;
  }
  return false;
}

// fenceAtCut answers the fence still open at a cut taken inside `line`:
// the one the scan already carried, after the part of the line the cut
// shows has been read as the opener or the closer it is. A run the cut
// went through is not a fence yet - the preview would end mid-run, which
// remark reads as prose.
function fenceAtCut(
  fence: FenceMarker | null,
  shown: string,
): FenceMarker | null {
  if (fence) return fenceCloses(shown, fence) ? null : fence;
  return fenceMarker(shown);
}

// fenceCloses reads `line` as the closer of `fence`: the same character, a
// run at least as long, at most three spaces in, and nothing but spaces
// after it (CommonMark). A code line that happens to start with ```js is
// therefore still code; reading it as the closer ended the block early and
// handed everything under it to the fold as prose.
function fenceCloses(line: string, fence: FenceMarker): boolean {
  const match = /^ {0,3}(`{3,}|~{3,})[ \t]*$/.exec(line);
  if (!match) return false;
  return match[1][0] === fence.char && match[1].length >= fence.length;
}

// fenceMarker reads the fence a line opens. CommonMark allows up to three
// spaces of indentation and requires the run to be at least three
// characters of one kind; an info string may follow it.
function fenceMarker(line: string): FenceMarker | null {
  const match = /^ {0,3}(`{3,}|~{3,})/.exec(line);
  if (!match) return null;
  return { char: match[1][0], length: match[1].length };
}

// isHeading recognizes a heading whose body a fold must not separate from
// it: ATX (`# Title`, also inside a quote or a list item) and setext (a
// title with `===` or `---` under it, which is the line the blank line
// actually follows). `previous` is the last non-blank line before `line`.
function isHeading(line: string, previous: string): boolean {
  const body = blockBody(line);
  if (/^#{1,6}(\s|$)/.test(body)) return true;
  return previous !== '' && /^(=+|-+)\s*$/.test(body);
}

// blockBody strips the container prefix - a quote marker or a list bullet
// - so a heading written inside one is still read as a heading.
function blockBody(line: string): string {
  let body = line.trimStart();
  for (;;) {
    const prefix = /^(?:>\s?|[-*+]\s|\d+[.)]\s)/.exec(body);
    if (!prefix) return body;
    body = body.slice(prefix[0].length).trimStart();
  }
}
