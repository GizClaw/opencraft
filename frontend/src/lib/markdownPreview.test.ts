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
    const { shown } = preview!;
    // The cut backs up to a blank line, so the preview ends where a
    // section does instead of half way through a sentence.
    expect(shown.endsWith('section, still prose.')).toBe(true);
    expect(shown.startsWith('## Section 0')).toBe(true);
    expect(text.startsWith(shown)).toBe(true);
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
    // The blank line under the title is inside the budget and follows a
    // heading, and there is no earlier section to back up to: the cut
    // moves forward to the next line boundary instead of showing the
    // title with nothing under it. The CRLFs inside the preview stay.
    expect(preview!.shown).toBe('# Title\r\n\r\na paragraph');
  });

  it('does not answer a long paragraph with its first line', () => {
    // The nearest blank line is 8 characters in, 3000 from the fold: a
    // preview of "Intro." is not a preview. The limit cut wins.
    const text = `Intro.\n\n${'x'.repeat(3000)}`;
    const preview = markdownPreview(text, 2048);
    expect(preview).not.toBeNull();
    expect(preview!.shown.startsWith('Intro.')).toBe(true);
    expect(preview!.shown.length).toBeGreaterThan(2048 / 2);
  });

  it('does not answer a setext title plus a long body with the title', () => {
    const text = `Title\n=====\n\n${'y'.repeat(3000)}`;
    const preview = markdownPreview(text, 2048);
    expect(preview).not.toBeNull();
    expect(preview!.shown.length).toBeGreaterThan(2048 / 2);
  });

  it('recognizes a setext heading the cut would leave alone', () => {
    // The blank line under the underline is inside the budget and follows
    // a heading, body included: the cut backs up to the section above it.
    const text = `${'a'.repeat(1000)}\n\nTitle\n=====\n\n${'b'.repeat(1000)}`;
    const preview = markdownPreview(text, 1500);
    expect(preview).not.toBeNull();
    expect(preview!.shown.endsWith('a')).toBe(true);
  });

  it('recognizes a heading inside a quote or a list item', () => {
    const text = `${'a'.repeat(1000)}\n\n> ## Quoted\n\n${'b'.repeat(1000)}`;
    const preview = markdownPreview(text, 1500);
    expect(preview).not.toBeNull();
    expect(preview!.shown.endsWith('a')).toBe(true);
  });

  it('carries the reference definitions the fold hides', () => {
    // Without the definition the preview renders `[guide]` as literal
    // text: the one thing a fold must not change is what the shown part
    // means.
    const text = [
      'Read the [guide] first.',
      '',
      'x'.repeat(3000),
      '',
      '[guide]: ./refs/guide.md',
      '[^1]: the note',
      '',
    ].join('\n');
    const preview = markdownPreview(text, 2048);
    expect(preview).not.toBeNull();
    expect(preview!.shown).toContain('[guide]: ./refs/guide.md');
    expect(preview!.shown).toContain('[^1]: the note');
    expect(preview!.shown.startsWith('Read the [guide] first.')).toBe(true);
  });

  it('leaves a definition inside a fenced block alone', () => {
    const text = [
      'Read the [guide] first.',
      '',
      'x'.repeat(3000),
      '',
      '```md',
      '[guide]: ./refs/guide.md',
      '```',
      '',
    ].join('\n');
    const preview = markdownPreview(text, 2048);
    expect(preview).not.toBeNull();
    expect(preview!.shown).not.toContain('[guide]:');
  });
});
