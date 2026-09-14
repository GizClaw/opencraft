import { describe, expect, it } from 'vitest';
import { toolResultText } from './toolresult';

describe('toolResultText', () => {
  it('returns an empty string for a result without content', () => {
    expect(toolResultText(undefined)).toBe('');
    expect(toolResultText({})).toBe('');
    expect(toolResultText({ parts: [] })).toBe('');
  });

  it('renders the text part a built-in tool returns', () => {
    expect(
      toolResultText({
        parts: [{ type: 'text', text: '{"exit_code":0,"stdout":"ok"}' }],
      }),
    ).toBe('{"exit_code":0,"stdout":"ok"}');
  });

  it('joins the text parts of a multi-part result in order', () => {
    expect(
      toolResultText({
        parts: [
          { type: 'text', text: 'first' },
          { type: 'text', text: 'second' },
        ],
      }),
    ).toBe('first\nsecond');
  });

  it('falls back to the JSON of a structured data part', () => {
    expect(
      toolResultText({
        parts: [
          { type: 'text', text: 'scan' },
          { type: 'data', media_type: 'application/json', value: { files: 2 } },
        ],
      }),
    ).toBe('scan\n{"files":2}');
  });

  it('skips the media kinds that have no text form', () => {
    expect(
      toolResultText({
        parts: [
          { type: 'image', source: { kind: 'url', url: 'file:///shot.png' } },
          { type: 'text', text: 'screenshot taken' },
        ],
      }),
    ).toBe('screenshot taken');
  });
});
