import { describe, expect, it } from 'vitest';
import { toolResultImages, toolResultText } from './toolresult';

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

describe('toolResultImages', () => {
  it('turns an inline image part into a data URL', () => {
    expect(
      toolResultImages({
        parts: [
          { type: 'text', text: 'view_image: shot.png (1440x900, 12 bytes)' },
          {
            type: 'image',
            source: {
              kind: 'inline',
              data: 'QUJD',
              media_type: 'image/jpeg',
            },
          },
        ],
      }),
    ).toEqual([
      { data_url: 'data:image/jpeg;base64,QUJD', media_type: 'image/jpeg' },
    ]);
  });

  it('assumes jpeg when the source carries no media type', () => {
    expect(
      toolResultImages({
        parts: [{ type: 'image', source: { kind: 'inline', data: 'QUJD' } }],
      }),
    ).toEqual([
      { data_url: 'data:image/jpeg;base64,QUJD', media_type: 'image/jpeg' },
    ]);
  });

  it('skips url sources and empty payloads', () => {
    expect(
      toolResultImages({
        parts: [
          { type: 'image', source: { kind: 'url', url: 'file:///shot.png' } },
          { type: 'image', source: { kind: 'inline', data: '' } },
          { type: 'image' },
        ],
      }),
    ).toEqual([]);
    expect(toolResultImages(undefined)).toEqual([]);
  });
});
