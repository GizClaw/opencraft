// conv/model.ts: message identity and the budgets a conversation is
// kept under (row count, text and media), plus first-message titling.

import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { MessageView } from '../store';
import { stateRoot } from '../../state/app';
import {
  firstMessageTitle,
  pendingConversationIDs,
  storeBytes,
  useStore,
} from '../store';
import { apiMock } from '../../test/apiMock';
import { bindRun, historyTurn, primeStoreTest } from '../../test/storeHarness';

vi.mock('../api', async () => ({
  api: (await import('../../test/apiMock')).apiMock,
}));
vi.mock(
  '../../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/diagnostics',
  async () => (await import('../../test/apiMock')).diagnosticsMock,
);

beforeEach(primeStoreTest);

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

describe('pending prompts', () => {
  it('maps pending prompt owners to unique conversation ids', () => {
    expect(
      pendingConversationIDs({
        'p-1': 's-1',
        'p-2': 's-1',
        'p-3': 's-2',
      }),
    ).toEqual(['s-1', 's-2']);
  });
});

describe('transcript cap', () => {
  function manyMessages(n: number): MessageView[] {
    return Array.from({ length: n }, (_, i) => ({
      id: `m-${i}`,
      role: i % 2 === 0 ? ('user' as const) : ('assistant' as const),
      text: `message-${i}`,
      items: [],
      attachments: [],
    }));
  }

  it('caps conversation messages on a turn boundary and re-bases starts', () => {
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-x' });
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          messages: manyMessages(1000),
          turnArtifacts: [
            { id: 'old', start: 100, docs: [] },
            { id: 'new', start: 900, docs: [] },
          ],
        },
      },
    });
    useStore.getState().handleEvent({
      type: 'turn_end',
      data: { run_id: 'r-x', conversation_id: 's-1', status: 'completed' },
    });

    const conv = useStore.getState().conversations['s-1'];
    // The exact cut (200) is mid-turn, so the trim backs up to the
    // largest turn boundary at or below it (100) instead of leaving a
    // partial turn behind: 900 messages kept, both starts re-based.
    expect(conv.messages).toHaveLength(900);
    expect(conv.messages[0].text).toBe('message-100');
    expect(conv.turnArtifacts.map((t) => t.start)).toEqual([0, 800]);
  });

  it('cuts at the next boundary when backing up would keep it all', async () => {
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          messages: manyMessages(1000),
          turnArtifacts: [
            { id: 'top', start: 0, docs: [] },
            { id: 'new', start: 900, docs: [] },
          ],
        },
      },
    });
    await useStore.getState().setThink('high');

    const conv = useStore.getState().conversations['s-1'];
    // The exact cut (200) is inside the first turn, and backing up to
    // the only boundary below it (0) would keep the whole transcript —
    // so the trim moves down to the next boundary (900) instead. The
    // whole turn goes, and the kept rows still begin at their own strip:
    // nothing is left behind without the turn that owns it.
    expect(conv.messages).toHaveLength(100);
    expect(conv.messages[0].text).toBe('message-900');
    expect(
      conv.turnArtifacts.map((t) => ({ id: t.id, start: t.start })),
    ).toEqual([{ id: 'new', start: 0 }]);
  });

  it('keeps a lone live turn whole rather than cut inside it', async () => {
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          messages: manyMessages(900),
          turnArtifacts: [{ id: 'live', start: 0, docs: [], runID: 'r-live' }],
        },
      },
    });
    await useStore.getState().setThink('high');

    // No boundary past the cut exists: the transcript is this one turn,
    // the live run's. Cutting inside it would drop the strip its run,
    // its deltas and its archived copy are found by while keeping its
    // rows, so the cap yields — the rows are what the reader watches.
    const conv = useStore.getState().conversations['s-1'];
    expect(conv.messages).toHaveLength(900);
    expect(conv.turnArtifacts).toHaveLength(1);
    expect(conv.turnArtifacts[0].id).toBe('live');
    expect(conv.turnArtifacts[0].start).toBe(0);
  });

  it('does not cut at a strip that owns no rows', async () => {
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          messages: manyMessages(850),
          turnArtifacts: [
            { id: 'big', start: 0, docs: [] },
            // The seam a run met on the wire opens its strip at, before
            // the run's first row has landed: this strip owns nothing,
            // so a cut here would drop every row in the store.
            { id: 'live', start: 850, docs: [], runID: 'r-live' },
          ],
        },
      },
    });
    await useStore.getState().setThink('high');

    const conv = useStore.getState().conversations['s-1'];
    expect(conv.messages).toHaveLength(850);
    expect(
      conv.turnArtifacts.map((t) => ({ id: t.id, start: t.start })),
    ).toEqual([
      { id: 'big', start: 0 },
      { id: 'live', start: 850 },
    ]);
  });

  it('moves the history cursor past the turns the trim dropped', async () => {
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          messages: manyMessages(900),
          turnArtifacts: [
            { id: 'h-1', start: 0, docs: [], seq: 1 },
            { id: 'live', start: 300, docs: [], runID: 'r-live' },
            { id: 'h-3', start: 700, docs: [], seq: 3 },
          ],
          historySeq: 1,
          historyHasMore: true,
        },
      },
    });
    await useStore.getState().setThink('high');

    // h-1 (seq 1) is dropped and the live strip — seq-less — is what the
    // kept rows start at, so the cursor has to name the oldest archived
    // turn that is still loaded (h-3, seq 3); keeping 1 would ask the
    // backend for turns below a turn that is gone, and h-2/h-1 could
    // never be paged back in.
    const conv = useStore.getState().conversations['s-1'];
    expect(conv.messages).toHaveLength(600);
    expect(
      conv.turnArtifacts.map((t) => ({ id: t.id, start: t.start })),
    ).toEqual([
      { id: 'live', start: 0 },
      { id: 'h-3', start: 400 },
    ]);
    expect(conv.historySeq).toBe(3);
  });

  it('keeps the dropped turns reachable when the trim took them all', async () => {
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          messages: manyMessages(850),
          turnArtifacts: [
            { id: 'h-1', start: 0, docs: [], seq: 1 },
            { id: 'live', start: 830, docs: [], runID: 'r-live' },
          ],
          historySeq: 1,
          historyHasMore: false,
        },
      },
    });
    await useStore.getState().setThink('high');

    // The only archived turn is gone; the page below now has to start
    // one past it (seq 2), and the transcript has to offer to fetch it
    // at all — otherwise the rows the reader scrolls up for are lost
    // until the session is reloaded.
    const conv = useStore.getState().conversations['s-1'];
    expect(conv.messages).toHaveLength(20);
    expect(conv.turnArtifacts).toEqual([
      { id: 'live', start: 0, docs: [], runID: 'r-live' },
    ]);
    expect(conv.historySeq).toBe(2);
    expect(conv.historyHasMore).toBe(true);
  });
});

describe('memory budgets', () => {
  // 256 KiB is small enough that the newest BUDGET_KEEP_MESSAGES (24)
  // are well under the 16 MiB budget, so the eviction below can actually
  // reach it.
  const CHARS = 256 << 10;
  const BUDGET = 16 << 20;

  function imageMessage(i: number): MessageView {
    return {
      id: `m-${i}`,
      role: 'user',
      text: '',
      items: [],
      attachments: [
        {
          id: `att-${i}`,
          kind: 'image',
          path: `/tmp/shot-${i}.png`,
          name: `shot-${i}.png`,
          media_type: 'image/jpeg',
          data_url: `data:image/jpeg;base64,${'A'.repeat(CHARS)}`,
        },
      ],
    };
  }

  function resultMessage(i: number): MessageView {
    return {
      id: `m-${i}`,
      role: 'assistant',
      text: '',
      attachments: [],
      items: [
        {
          kind: 'tool_call',
          id: `item-${i}`,
          tool: {
            id: `call-${i}`,
            name: 'bash',
            args: '{}',
            status: 'done',
            result: 'B'.repeat(CHARS),
          },
        },
      ],
    };
  }

  async function load(messages: MessageView[]) {
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          messages,
        },
      },
    });
    // Any write path settles the conversation: setThink goes through
    // updateConv, the same settle a stream flush calls.
    await useStore.getState().setThink('high');
  }

  it('drops the oldest inline images just past the media budget', async () => {
    const count = 100;
    await load(Array.from({ length: count }, (_, i) => imageMessage(i)));

    const conv = useStore.getState().conversations['s-1'];
    const held = storeBytes();
    expect(held.media).toBeLessThanOrEqual(BUDGET);
    // Oldest first and no further: the total lands within one image of
    // the budget instead of clearing the transcript.
    expect(held.media).toBeGreaterThan(BUDGET - (CHARS + 32));
    // An evicted entry keeps everything but the bytes: the path is what
    // both viewers re-read from.
    const first = conv.messages[0].attachments[0];
    expect(first.data_url).toBeUndefined();
    expect(first.path).toBe('/tmp/shot-0.png');
    expect(first.media_type).toBe('image/jpeg');
    // The newest message is what the reader is looking at.
    expect(conv.messages[count - 1].attachments[0].data_url).toBeDefined();
  });

  it('leaves the newest messages alone when they alone blow the budget', async () => {
    // Two images per message, 1 MiB each: the protected tail holds
    // 24 messages × 2 MiB, which is already past the budget. The bound is
    // what it is — trimming there would drop the pictures on screen.
    const count = 40;
    const heavy: MessageView[] = Array.from({ length: count }, (_, i) => {
      const msg = imageMessage(i);
      return {
        ...msg,
        attachments: [
          {
            ...msg.attachments[0],
            data_url: `data:image/jpeg;base64,${'C'.repeat(1 << 20)}`,
          },
          {
            ...msg.attachments[0],
            id: `att-b-${i}`,
            data_url: `data:image/jpeg;base64,${'D'.repeat(1 << 20)}`,
          },
        ],
      };
    });
    await load(heavy);

    const conv = useStore.getState().conversations['s-1'];
    expect(storeBytes().media).toBeGreaterThan(BUDGET);
    expect(conv.messages[0].attachments[0].data_url).toBeUndefined();
    expect(conv.messages[0].attachments[1].data_url).toBeUndefined();
    expect(conv.messages[count - 1].attachments[0].data_url).toBeDefined();
  });

  it('folds the oldest tool results past the text budget', async () => {
    const count = 100;
    await load(Array.from({ length: count }, (_, i) => resultMessage(i)));

    const conv = useStore.getState().conversations['s-1'];
    const held = storeBytes();
    expect(held.text).toBeLessThanOrEqual(BUDGET);
    const folded = conv.messages[0].items[0];
    expect(folded.kind).toBe('tool_call');
    if (folded.kind !== 'tool_call') return;
    // Folded, not dropped: the marker says how much went, and the tail
    // (where a failure says why) stays.
    expect(folded.tool.result).toContain('…[trimmed');
    expect(folded.tool.result?.endsWith('BBBB')).toBe(true);
    expect(folded.tool.result?.length ?? 0).toBeLessThan(4 << 10);
    const newest = conv.messages[count - 1].items[0];
    if (newest.kind === 'tool_call') {
      expect(newest.tool.result).toHaveLength(CHARS);
    }
  });

  it('caps one tool result at MAX_TOOL_RESULT_CHARS when it lands', () => {
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
            call: { id: 'call-1', name: 'bash', arguments: { cmd: 'cat log' } },
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
                parts: [{ type: 'text', text: 'L'.repeat(700 << 10) }],
              },
              is_error: false,
            },
          },
        },
      },
    });
    useStore.getState().flushStreams();

    const item = useStore.getState().conversations['s-1'].messages[0].items[0];
    expect(item.kind).toBe('tool_call');
    if (item.kind !== 'tool_call') return;
    expect(item.tool.result).toContain('…[trimmed');
    // The bound plus the marker that says where the middle went.
    expect(item.tool.result?.length ?? 0).toBeLessThan((256 << 10) + 64);
    expect(item.tool.result?.endsWith('LLLL')).toBe(true);
  });

  it('engages the media budget on the paging write itself', async () => {
    // Scrolling back through screenshot-heavy history: paged-in turns
    // used to skip settleConversation altogether, so their inline frames
    // piled up untrimmed (the fixture run that found this read 20.7 MiB
    // against the 16 MiB budget). Paging settles the budgets now — and
    // still skips the message cap, so the page just fetched stays.
    const FRAME = 2 << 20;
    const seed: MessageView[] = Array.from({ length: 30 }, (_, i) => ({
      id: `seed-${i}`,
      role: i % 2 === 0 ? ('user' as const) : ('assistant' as const),
      text: `seed-${i}`,
      items: [],
      attachments: [],
    }));
    const frame = (seq: number) => ({
      seq,
      at: '2026-09-01T00:00:00Z',
      messages: [
        {
          role: 'user',
          content: { parts: [{ type: 'text', text: `look-${seq}` }] },
        },
        {
          role: 'assistant',
          content: {
            parts: [
              {
                type: 'tool_call',
                call: {
                  id: `c-${seq}`,
                  name: 'view_image',
                  arguments: { path: `shots/${seq}.png` },
                },
              },
            ],
          },
        },
        {
          role: 'tool',
          content: {
            parts: [
              {
                type: 'tool_result',
                result: {
                  call_id: `c-${seq}`,
                  content: {
                    parts: [
                      {
                        type: 'image',
                        source: {
                          kind: 'inline',
                          media_type: 'image/png',
                          data: 'A'.repeat(FRAME),
                        },
                      },
                    ],
                  },
                  is_error: false,
                },
              },
            ],
          },
        },
      ],
      artifacts: [],
    });
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          messages: seed,
          historyHasMore: true,
          historySeq: 90,
        },
      },
    });
    // One extra turn rides ahead of the page (historyPage drops it), ten
    // turns keep their frame each: 20 MiB of frames against the 16 MiB.
    apiMock.sessionTurns.mockResolvedValue([
      historyTurn(87, 'extra', 'extra'),
      ...Array.from({ length: 10 }, (_, i) => frame(88 + i)),
    ]);

    const added = await useStore.getState().loadEarlierHistory('s-1');
    expect(added).toBe(20);

    const conv = useStore.getState().conversations['s-1'];
    const held = storeBytes();
    expect(held.media).toBeLessThanOrEqual(BUDGET);
    expect(held.media).toBeGreaterThan(BUDGET - (FRAME + 64));
    // The oldest frames went; the newest page turn kept the picture the
    // reader just scrolled to.
    const first = conv.messages[1].items[0];
    if (first.kind !== 'tool_call') throw new Error('expected a tool call');
    expect(first.tool.images).toBeUndefined();
    const newest = conv.messages[19].items[0];
    if (newest.kind !== 'tool_call') throw new Error('expected a tool call');
    expect(newest.tool.images).toHaveLength(1);
  });

  it('keeps the history cursor when the archive sends an empty page', async () => {
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          messages: [],
          historyHasMore: true,
          historySeq: 90,
        },
      },
    });
    apiMock.sessionTurns.mockResolvedValue([]);

    expect(await useStore.getState().loadEarlierHistory('s-1')).toBe(0);

    const conv = useStore.getState().conversations['s-1'];
    // The page says the archive holds nothing below, not that the loaded
    // transcript starts at 0: a cursor of 0 would send the next read back
    // to the beginning of the session.
    expect(conv.historySeq).toBe(90);
    expect(conv.historyHasMore).toBe(false);
    expect(conv.historyLoading).toBe(false);
  });
});
