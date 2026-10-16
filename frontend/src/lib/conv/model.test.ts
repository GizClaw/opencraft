import { describe, expect, it } from 'vitest';
import { firstMessageTitle, type MessageView } from './model';

describe('firstMessageTitle', () => {
  const user = (text: string, attachments: unknown[] = []): MessageView => ({
    id: 'm-1',
    role: 'user',
    text,
    items: [],
    attachments: attachments as MessageView['attachments'],
  });

  it('uses the first line of the first user message', () => {
    expect(
      firstMessageTitle([
        user('fix the typo\nand rerun the tests'),
        user('second prompt'),
      ]),
    ).toBe('fix the typo');
  });

  it('skips empty user text and falls back to [attachment]', () => {
    expect(firstMessageTitle([user('  '), user('', [{}])])).toBe(
      '[attachment]',
    );
  });

  it('caps the title at 70 runes', () => {
    const title = firstMessageTitle([user('汉'.repeat(80))]);
    expect(title).toBe(`${'汉'.repeat(70)}…`);
  });

  it('returns an empty string without user messages', () => {
    expect(firstMessageTitle([])).toBe('');
  });

  // A delegation note is a user-role row the app wrote: the header and
  // the live title mirror must not name the conversation after the
  // app's report (the backend's own fallback skips it the same way).
  it('skips rows the app itself wrote', () => {
    const note: MessageView = {
      ...user('[delegated worker "researcher" finished: succeeded]'),
      kind: 'delegation_note',
    };
    expect(firstMessageTitle([note, user('the real question')])).toBe(
      'the real question',
    );
    expect(firstMessageTitle([note])).toBe('');
  });
});
