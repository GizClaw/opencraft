import type { ReactNode } from 'react';

// markdownCache keeps the tree react-markdown built for one block, so a row
// the transcript mounts again shows it without parsing it again.
//
// The transcript is a windowed list: a row that scrolls out is unmounted,
// and the one that scrolls back is built from nothing. For a markdown block
// that means the parse is paid again — react-markdown's mdast/hast walk and
// rehype-highlight's work on every code fence, 12-20ms for a block of a few
// kilobytes (the probe's flush_md_p95 is that number). Scrolling back over a
// screenful pays it once per row, which is the "one jump feels like 200ms"
// half of a long session, and it is paid again for text the reader has
// already read.
//
// The parse is a pure function of its own source text: the plugin lists and
// the component map are module constants (see Markdown.tsx), and the two
// props a surface varies — the link handler and the base path — reach the
// elements through MarkdownSurface at render time, not through the tree. So
// the elements one parse produced are exactly what a second mount needs, and
// mounting them is all it does.
//
// The cache is bounded for the mirror-image reason the preview cache is
// bounded (see attachmentPreviews.ts): an element tree costs what its nodes
// cost, and the transcript's own folding exists to keep those nodes small
// (BLOCK_FOLD_CHARS in StreamItemView). A cache that grew with every block a
// session ever scrolled past would put them back. The budget is counted in
// source characters — the unit a block's size is written in, and the thing
// its tree is proportional to — so the longest block that still fits is the
// longest one the transcript renders while the reader scrolls.
//
// An entry larger than MAX_MARKDOWN_ENTRY_CHARS is not kept at all. It would
// evict every block the reader had just scrolled over to hold one answer,
// and that answer is reached by a deliberate scroll, not by the scroll-back
// this exists to make cheap.

// MAX_MARKDOWN_CACHE_CHARS bounds the whole cache. The rendered tree runs at
// roughly a fifth of the source in nodes (~0.21 elements and text nodes per
// character, measured on a 10k-character answer), so this is a few megabytes
// of elements: several screens of folded blocks, or the last few long
// answers the reader watched arrive.
export const MAX_MARKDOWN_CACHE_CHARS = 1 << 18;

// MAX_MARKDOWN_CACHE_ENTRIES bounds the keys themselves, so a transcript of
// thousands of one-line messages cannot trade characters for bookkeeping.
export const MAX_MARKDOWN_CACHE_ENTRIES = 256;

// MAX_MARKDOWN_ENTRY_CHARS is the largest block worth keeping: above it one
// entry would own most of the budget (see the note above).
export const MAX_MARKDOWN_ENTRY_CHARS = 1 << 15;

// MarkdownCache is a least-recently-used map from source text to the tree
// parsed from it. Reads and writes both count as use: a block a row is
// showing stays, the one that fell off the top of the transcript goes first.
export class MarkdownCache {
  private readonly entries = new Map<string, ReactNode>();
  private chars = 0;
  // hits and misses are the cache's own verdict on a reader's scrolling: a
  // miss is a parse that was paid again, a hit is one that was not.
  private hitCount = 0;
  private missCount = 0;

  constructor(
    private readonly maxChars: number = MAX_MARKDOWN_CACHE_CHARS,
    private readonly maxEntries: number = MAX_MARKDOWN_CACHE_ENTRIES,
    private readonly maxEntryChars: number = MAX_MARKDOWN_ENTRY_CHARS,
  ) {}

  // get answers the tree for text, or undefined when this window has not
  // parsed that exact text (or was not allowed to keep it).
  get(text: string): ReactNode | undefined {
    const tree = this.entries.get(text);
    if (tree === undefined) {
      this.missCount += 1;
      return undefined;
    }
    this.entries.delete(text);
    this.entries.set(text, tree);
    this.hitCount += 1;
    return tree;
  }

  // set keeps one parse. A block past maxEntryChars is the caller's to
  // render and to drop: it stays out of the budget entirely.
  set(text: string, tree: ReactNode) {
    if (text.length > this.maxEntryChars) return;
    if (this.entries.delete(text)) this.chars -= text.length;
    this.entries.set(text, tree);
    this.chars += text.length;
    this.evict();
  }

  // clear drops every tree. A workspace switch calls it: the transcript of
  // the workspace being left is gone, and its parsed blocks with it (the
  // switch also reloads the page, which is what actually hands the memory
  // back — see applyWorkspaceSwitch).
  clear() {
    this.entries.clear();
    this.chars = 0;
  }

  // charCount, size, hits and misses are the cache's own accounting, for the
  // tests and for the perf probe's series.
  get charCount(): number {
    return this.chars;
  }

  get size(): number {
    return this.entries.size;
  }

  get hits(): number {
    return this.hitCount;
  }

  get misses(): number {
    return this.missCount;
  }

  // evict drops the oldest entries until both bounds hold. The newest entry
  // is never dropped for size: a single block over the whole budget is still
  // one the row in front of the reader would otherwise reparse.
  private evict() {
    while (this.entries.size > 1) {
      const overEntries = this.entries.size > this.maxEntries;
      const overChars = this.chars > this.maxChars;
      if (!overEntries && !overChars) return;
      const oldest = this.entries.keys().next();
      if (oldest.done) return;
      this.entries.delete(oldest.value);
      this.chars -= oldest.value.length;
    }
  }
}

// markdownTrees is the window's one cache. Exported as a singleton so every
// markdown surface shares it, the workspace glue can drop it and the perf
// probe can report it.
export const markdownTrees = new MarkdownCache();
