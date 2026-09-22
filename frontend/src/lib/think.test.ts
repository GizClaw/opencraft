import { describe, expect, it } from 'vitest';
import { latestThink } from './think';
import type { AssistantItem, MessageView } from './store';

function msg(id: string, items: AssistantItem[]): MessageView {
  return { id, role: 'assistant', text: '', items, attachments: [] };
}

const reasoning = (
  id: string,
  text: string,
  chunks?: string[],
): AssistantItem => ({
  kind: 'reasoning',
  id,
  text,
  chunks,
});

const text = (id: string, body: string): AssistantItem => ({
  kind: 'text',
  id,
  text: body,
});

describe('latestThink', () => {
  it('returns null without a reasoning block', () => {
    expect(latestThink([])).toBeNull();
    expect(latestThink([msg('m', [text('t', 'hello')])])).toBeNull();
  });

  it('returns the newest block with its id', () => {
    const messages = [
      msg('m1', [reasoning('r1', 'first thought'), text('t1', 'done')]),
      msg('m2', [reasoning('r2', 'second thought')]),
    ];
    expect(latestThink(messages)).toEqual({ id: 'r2', text: 'second thought' });
  });

  it('joins the chunks of a block that is still streaming', () => {
    const messages = [msg('m', [reasoning('r1', '', ['half ', 'a thought'])])];
    expect(latestThink(messages)).toEqual({ id: 'r1', text: 'half a thought' });
  });

  it('skips a block that has no text yet', () => {
    const messages = [
      msg('m1', [reasoning('r1', 'the earlier thought')]),
      msg('m2', [reasoning('r2', '')]),
    ];
    expect(latestThink(messages)).toEqual({
      id: 'r1',
      text: 'the earlier thought',
    });
  });

  it('keeps the last thought of a finished turn', () => {
    const messages = [
      msg('m1', [reasoning('r1', 'thought'), text('t1', 'answer')]),
      msg('m2', [text('t2', 'follow-up')]),
    ];
    expect(latestThink(messages)).toEqual({ id: 'r1', text: 'thought' });
  });
});
