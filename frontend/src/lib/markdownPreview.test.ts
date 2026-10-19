import { describe, expect, it } from 'vitest';
import { markdownPreview } from './markdownPreview';

const section = (i: number) =>
  `## Section ${i}\n\nParagraph ${i} explains the result in prose with \`code\` and a [link](https://example.com/${i}).\n\nAnother line of the same section, still prose.\n`;

const fenceCount = (text: string) =>
  text.split('\n').filter((line) => /^ {0,3}(```|~~~)/.test(line)).length;

describe('markdownPreview', () => {
  it('folds nothing that fits', () => {
    const text = section(1);
    expect(markdownPreview(text, text.length)).toBeNull();
    expect(markdownPreview(text, text.length + 1)).toBeNull();
  });

  it('cuts at a section boundary, not mid-paragraph', () => {
    const text = Array.from({ length: 6 }, (_, i) => section(i)).join('\n');
    const preview = markdownPreview(text, 400);
    expect(preview).not.toBeNull();
    const { shown, hidden } = preview!;
    // The cut backs up to a blank line, so the preview ends where a
    // section does instead of half way through a sentence.
    expect(shown.endsWith('section, still prose.')).toBe(true);
    expect(shown.startsWith('## Section 0')).toBe(true);
    expect(text.startsWith(shown)).toBe(true);
    // hidden counts what the cut left behind, so the two agree up to the
    // trailing whitespace the preview drops.
    expect(hidden).toBeGreaterThanOrEqual(text.length - shown.length - 2);
    expect(shown.length).toBeLessThanOrEqual(400);
  });

  it('does not leave a heading at the end of the preview', () => {
    const text = Array.from({ length: 6 }, (_, i) => section(i)).join('\n');
    const preview = markdownPreview(text, 400);
    expect(preview).not.toBeNull();
    // The blank line inside the budget follows `## Section 2`, whose body
    // is behind the fold: the cut backs up to the previous section so the
    // preview does not end on a title with nothing under it.
    expect(preview!.shown).not.toMatch(/## Section \d+$/);
    expect(preview!.shown.endsWith('still prose.')).toBe(true);
  });

  it('never leaves a fenced block open', () => {
    // The fence opens before the limit and closes after it: cutting
    // through it would render every paragraph below as code.
    const text = [
      'intro line',
      '',
      '```ts',
      'const a = 1;',
      'const b = 2;',
      'const c = 3;',
      'const d = 4;',
      '```',
      '',
      'after the block',
      '',
    ].join('\n');
    const preview = markdownPreview(text, 40);
    expect(preview).not.toBeNull();
    expect(preview!.shown).toBe('intro line');
    expect(fenceCount(preview!.shown) % 2).toBe(0);
  });

  it('closes the fence when the whole limit sits inside one', () => {
    // One code block, longer than the budget, with no line outside the
    // fence to cut at: the preview keeps its first lines and closes the
    // fence so the rest of the block does not read as prose.
    const body = Array.from(
      { length: 40 },
      (_, i) => `const value${i} = ${i};`,
    );
    const text = ['```ts', ...body, '```', ''].join('\n');
    const preview = markdownPreview(text, 200);
    expect(preview).not.toBeNull();
    const { shown } = preview!;
    expect(shown.startsWith('```ts')).toBe(true);
    expect(shown.endsWith('```')).toBe(true);
    expect(fenceCount(shown)).toBe(2);
    expect(shown.length).toBeLessThanOrEqual(200 + 5);
  });

  it('keeps a tilde fence closed by a tilde fence', () => {
    const text = ['~~~', 'a', 'b', 'c', 'd', '~~~', '', 'tail', ''].join('\n');
    const preview = markdownPreview(text, 12);
    expect(preview).not.toBeNull();
    expect(preview!.shown.endsWith('~~~')).toBe(true);
  });

  it('cuts a single long line at the limit', () => {
    const text = 'x'.repeat(500);
    const preview = markdownPreview(text, 100);
    expect(preview).not.toBeNull();
    expect(preview!.shown).toBe('x'.repeat(100));
    expect(preview!.hidden).toBe(400);
  });

  it('cuts a long list at a line boundary', () => {
    const text = Array.from({ length: 30 }, (_, i) => `- item ${i}`).join('\n');
    const preview = markdownPreview(text, 100);
    expect(preview).not.toBeNull();
    const lines = preview!.shown.split('\n');
    expect(lines[0]).toBe('- item 0');
    expect(lines[lines.length - 1].startsWith('- item ')).toBe(true);
    expect(preview!.shown.length).toBeLessThanOrEqual(100);
  });

  it('treats an indented fence inside a list as a fence', () => {
    const text = [
      '- step one',
      '',
      '   ```sh',
      '   npm test',
      '   npm run build',
      '   ```',
      '',
      'trailing prose',
      '',
    ].join('\n');
    const preview = markdownPreview(text, 30);
    expect(preview).not.toBeNull();
    // The blank line inside the list is before the fence, so the cut
    // lands there and the block stays closed.
    expect(fenceCount(preview!.shown) % 2).toBe(0);
  });

  it('handles CRLF text', () => {
    const text = ['# Title', '', 'a paragraph', '', 'another one', ''].join(
      '\r\n',
    );
    const preview = markdownPreview(text, 24);
    expect(preview).not.toBeNull();
    // The last blank line inside the budget is the one under the title;
    // the paragraph's own boundary is past it.
    expect(preview!.shown).toBe('# Title');
    expect(preview!.hidden).toBe(text.length - 11);
  });
});
