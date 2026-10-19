// What the parsed-block cache buys: one block's parse against the lookup
// that replaces it on a second mount.
//
// The block is a ~2KB answer with a list and a fenced code block, which is
// what a settled block looks like after the transcript folds the long ones
// (BLOCK_FOLD_CHARS in StreamItemView). The parse is react-markdown's
// pipeline as Markdown.tsx runs it — the same two plugins, the same
// component map left out of the measurement because it only names elements,
// not work.
//
// Read the ratio as the parse a second mount no longer pays, not as the
// mount itself being free: React still builds the DOM from the elements the
// cache hands back. The parse is the half the probe reports per block
// (flush_md_p95), and it is the half this file is about.
import { bench, describe } from 'vitest';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import rehypeHighlight from 'rehype-highlight';
import { MarkdownCache } from './markdownCache';

const REMARK_PLUGINS = [remarkGfm];
const REHYPE_PLUGINS = [rehypeHighlight];

const BLOCK = [
  '## What changed',
  '',
  'The engine reads `craft.Options.Values` now, and the *paths* arrive as',
  '`${ocraft:WORKDIR}` rather than through the process environment.',
  '',
  ...Array.from(
    { length: 20 },
    (_, i) => `${i + 1}. step ${i}: fold block ${i} behind its control`,
  ),
  '',
  '```ts',
  ...Array.from({ length: 20 }, (_, i) => `const value${i} = compute(${i});`),
  '```',
].join('\n');

const cache = new MarkdownCache();

describe('markdown block', () => {
  bench('parse', () => {
    ReactMarkdown({
      children: BLOCK,
      remarkPlugins: REMARK_PLUGINS,
      rehypePlugins: REHYPE_PLUGINS,
    });
  });

  bench('cached', () => {
    const cached = cache.get(BLOCK);
    if (cached === undefined) {
      cache.set(
        BLOCK,
        ReactMarkdown({
          children: BLOCK,
          remarkPlugins: REMARK_PLUGINS,
          rehypePlugins: REHYPE_PLUGINS,
        }),
      );
    }
  });
});
