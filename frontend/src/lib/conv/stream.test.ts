// conv/stream.ts: applyStream and the block caps a live turn grows
// against (items, reasoning and text length, tool results).

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { stateRoot } from '../../state/app';
import { itemText, useStore } from '../store';
import type { MessageView } from './model';
import { foldLastTextItem, mergeAppend } from './stream';
import { actorValue, bindRun, primeStoreTest } from '../../test/storeHarness';

vi.mock('../api', async () => ({
  api: (await import('../../test/apiMock')).apiMock,
}));
vi.mock(
  '../../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/diagnostics',
  async () => (await import('../../test/apiMock')).diagnosticsMock,
);

beforeEach(primeStoreTest);

describe('stream deltas and caps', () => {
  it('marks the text block it appends as streamed', () => {
    // The transcript keeps a block whole when its deltas arrived here (see
    // StreamItemView): only the live path stamps the marker, so a block
    // replayed from history cannot claim the reader watched it.
    const msg: MessageView = {
      id: 'm-1',
      role: 'assistant',
      text: '',
      items: [],
      attachments: [],
    };
    mergeAppend(msg, 'text', 'hello');
    expect(msg.items[0]).toMatchObject({ kind: 'text', streamed: true });
    // Appending to the same block keeps the marker.
    mergeAppend(msg, 'text', ' again');
    expect(msg.items[0]).toMatchObject({ streamed: true });
    foldLastTextItem(msg);
    const folded = msg.items[0];
    if (folded.kind !== 'text') throw new Error('expected a text item');
    expect(itemText(folded)).toBe('hello again');
    expect(folded.streamed).toBe(true);
  });

  it('stamps a block hydrated from the archive when a delta lands in it', () => {
    // resume merges the archive with a live shell: the block was built from
    // history without the marker, and the delta that follows is proof this
    // window watched it grow. Without the stamp paging drops the fold
    // guard and the answer collapses under the reader at the turn end.
    const msg: MessageView = {
      id: 'm-1',
      role: 'assistant',
      text: '',
      items: [{ kind: 'text', id: 'p-1', text: 'partial' }],
      attachments: [],
    };

    mergeAppend(msg, 'text', ' more');

    expect(msg.items[0]).toMatchObject({ kind: 'text', streamed: true });
    const item = msg.items[0];
    if (item.kind !== 'text') throw new Error('expected a text item');
    expect(itemText(item)).toBe('partial more');
  });

  it('folds stream deltas into one assistant message', () => {
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    bindRun('r-1');
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
        },
      },
    });
    const handle = useStore.getState().handleEvent;

    handle({
      type: 'stream',
      data: {
        run_id: 'r-1',
        conversation_id: 's-1',
        delta: {
          type: 'part',
          part: {
            type: 'reasoning',
            text: 'thinking…',
          },
        },
      },
    });
    handle({
      type: 'stream',
      data: {
        run_id: 'r-1',
        conversation_id: 's-1',
        delta: {
          type: 'part',
          part: {
            type: 'tool_call',
            call: {
              id: 'call-1',
              name: 'read_file',
              arguments: { file_path: 'a.go' },
            },
          },
        },
      },
    });
    handle({
      type: 'stream',
      data: {
        run_id: 'r-1',
        conversation_id: 's-1',
        delta: {
          type: 'part',
          part: {
            type: 'tool_result',
            result: {
              call_id: 'call-1',
              content: {
                parts: [{ type: 'text', text: '{"content":"ok"}' }],
              },
              is_error: false,
            },
          },
        },
      },
    });
    handle({
      type: 'stream',
      data: {
        run_id: 'r-1',
        conversation_id: 's-1',
        delta: {
          type: 'part',
          part: { type: 'text', text: 'done' },
        },
      },
    });
    useStore.getState().flushStreams();

    const conv = useStore.getState().conversations['s-1'];
    expect(actorValue('s-1')?.turn).toBe('running');
    expect(conv.messages).toHaveLength(1);
    const items = conv.messages[0].items;
    expect(items[0]).toMatchObject({ kind: 'reasoning', text: 'thinking…' });
    expect(items[1]).toMatchObject({
      kind: 'tool_call',
      tool: {
        name: 'read_file',
        status: 'done',
        result: '{"content":"ok"}',
      },
    });
    expect(items[2]).toMatchObject({ kind: 'text', text: 'done' });
  });

  it('stamps a live tool call and keeps the image part of its result', () => {
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    bindRun('r-1');
    const handle = useStore.getState().handleEvent;

    handle({
      type: 'stream',
      data: {
        run_id: 'r-1',
        conversation_id: 's-1',
        delta: {
          type: 'part',
          part: {
            type: 'tool_call',
            call: {
              id: 'call-1',
              name: 'view_image',
              arguments: { path: 'shots/hero.png' },
            },
          },
        },
      },
    });
    handle({
      type: 'stream',
      data: {
        run_id: 'r-1',
        conversation_id: 's-1',
        delta: {
          type: 'part',
          part: {
            type: 'tool_result',
            result: {
              call_id: 'call-1',
              content: {
                parts: [
                  {
                    type: 'image',
                    source: {
                      kind: 'inline',
                      media_type: 'image/jpeg',
                      data: 'QUJD',
                    },
                  },
                  {
                    type: 'text',
                    text: 'view_image: shots/hero.png (1440x900, 123456 bytes)',
                  },
                ],
              },
              is_error: false,
            },
          },
        },
      },
    });
    useStore.getState().flushStreams();

    const items = useStore.getState().conversations['s-1'].messages[0].items;
    const item = items[0];
    if (item.kind !== 'tool_call') throw new Error('expected a tool call');
    // The card measures a live call itself: the call is stamped when this
    // client sees it, the result when it lands, and the image part of the
    // result travels with it.
    expect(item.tool.status).toBe('done');
    expect(typeof item.tool.seenAt).toBe('number');
    expect(item.tool.endedAt).toBeGreaterThanOrEqual(item.tool.seenAt ?? 0);
    expect(item.tool.images).toEqual([
      { data_url: 'data:image/jpeg;base64,QUJD', media_type: 'image/jpeg' },
    ]);
  });

  it('folds a live turn past the item cap and keeps the count', () => {
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    const handle = useStore.getState().handleEvent;
    const call = (i: number) =>
      handle({
        type: 'stream',
        data: {
          run_id: 'r-1',
          conversation_id: 's-1',
          delta: {
            type: 'part',
            part: {
              type: 'tool_call',
              call: { id: `call-${i}`, name: 'exec_command', arguments: {} },
            },
          },
        },
      });
    // 50 over the cap: the oldest blocks fold into the counter while the
    // newest stay mounted.
    const total = 450;
    for (let i = 0; i < total; i += 1) call(i);
    useStore.getState().flushStreams();

    const message = useStore.getState().conversations['s-1']?.messages.at(-1);
    expect(message?.droppedItems).toBe(total - 400);
    expect(message?.items).toHaveLength(400);
    const first = message?.items[0];
    expect(first?.kind === 'tool_call' && first.tool.id).toBe(
      `call-${total - 400}`,
    );
  });

  it('caps reasoning and text blocks while streaming', () => {
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    const handle = useStore.getState().handleEvent;
    const part = (type: 'text' | 'reasoning', text: string) =>
      handle({
        type: 'stream',
        data: {
          run_id: 'r-1',
          conversation_id: 's-1',
          delta: { type: 'part', part: { type, text } },
        },
      });
    // Chunked pushes: a reasoning trace keeps only its tail, visible text
    // keeps head and tail around a trim marker.
    for (let i = 0; i < 40; i += 1) part('reasoning', 'r'.repeat(1024));
    part('text', 'seed ');
    for (let i = 0; i < 400; i += 1) part('text', 't'.repeat(1024));
    useStore.getState().flushStreams();

    const message = useStore.getState().conversations['s-1']?.messages.at(-1);
    const reasoning = message?.items.find((it) => it.kind === 'reasoning');
    const text = message?.items.find((it) => it.kind === 'text');
    expect(reasoning && itemText(reasoning).length).toBeLessThanOrEqual(
      16 * 1024 + 64,
    );
    expect(reasoning && itemText(reasoning)).toContain('[trimmed');
    expect(text && itemText(text).length).toBeLessThanOrEqual(256 * 1024 + 64);
    expect(text && itemText(text)).toContain('[trimmed');
  });

  // The append path carries the block's length instead of joining it
  // (see TextItemFields.chars): the cap then measures a number, and the
  // join happens once, in the renderer. Drift between the two shows up
  // as a block that folds late or early, so the two are asserted equal.

  it('keeps a streaming block\u2019s length without joining it', () => {
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    const handle = useStore.getState().handleEvent;
    const chunk = 't'.repeat(1024);
    for (let i = 0; i < 40; i += 1) {
      handle({
        type: 'stream',
        data: {
          run_id: 'r-1',
          conversation_id: 's-1',
          delta: { type: 'part', part: { type: 'text', text: chunk } },
        },
      });
      // One delta per commit: the block is appended to, not rebuilt from
      // a coalesced string.
      useStore.getState().flushStreams();
    }

    const message = useStore.getState().conversations['s-1']?.messages.at(-1);
    const item = message?.items.find((it) => it.kind === 'text');
    const display = item ? itemText(item) : '';
    expect(display).toHaveLength(40 * chunk.length);
    expect(item && 'chars' in item ? item.chars : undefined).toBe(
      display.length,
    );
  });

  // settleConversation runs on every write, so it carries the last
  // measurement of a transcript forward instead of walking it (see
  // TranscriptFootprint): a row the write left alone is found by identity
  // and not measured. The probe row counts the reads of its text, which
  // is the only way to see a measurement that did not happen.

  it('reports a block\u2019s dropped characters exactly, fold after fold', () => {
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    const handle = useStore.getState().handleEvent;
    // 400 pushes of 1 KiB against the 256 KiB per-block cap: 144 whole
    // chunks go, and the marker has to say so.
    const chunk = 't'.repeat(1024);
    const pushes = 400;
    for (let i = 0; i < pushes; i += 1) {
      handle({
        type: 'stream',
        data: {
          run_id: 'r-1',
          conversation_id: 's-1',
          delta: { type: 'part', part: { type: 'text', text: chunk } },
        },
      });
      // Committing each delta is what makes the block fold repeatedly;
      // one coalesced push would fold once and measure nothing.
      useStore.getState().flushStreams();
    }

    const message = useStore.getState().conversations['s-1']?.messages.at(-1);
    const item = message?.items.find((it) => it.kind === 'text');
    const display = item ? itemText(item) : '';
    const marker = /…\[trimmed (\d+) chars\]/.exec(display);
    expect(marker).not.toBeNull();
    const kept = display.length - (marker?.[0].length ?? 0);
    expect(Number(marker?.[1])).toBe(pushes * chunk.length - kept);
  });
});
