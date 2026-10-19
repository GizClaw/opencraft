// conv/paging.ts: the tail sync a `session_updated` asks for, and the
// history cursor a page of the archive moves.

import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { MessageView } from '../store';
import { stateRoot } from '../../state/app';
import { useStore } from '../store';
import { apiMock } from '../../test/apiMock';
import { bindRun, primeStoreTest } from '../../test/storeHarness';

vi.mock('../api', async () => ({
  api: (await import('../../test/apiMock')).apiMock,
}));
vi.mock(
  '../../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/diagnostics',
  async () => (await import('../../test/apiMock')).diagnosticsMock,
);

beforeEach(primeStoreTest);

describe('transcript tail sync', () => {
  const user = (text: string): MessageView => ({
    id: `m-u-${text}`,
    role: 'user',
    text,
    items: [],
    attachments: [],
  });
  const assistant = (text: string): MessageView => ({
    id: `m-a-${text}`,
    role: 'assistant',
    text,
    items: [],
    attachments: [],
  });

  function noteTurn(seq: number) {
    return {
      seq,
      at: '2026-09-03T00:05:00Z',
      status: 'completed',
      run_id: `subagent:card-${seq}`,
      kind: 'delegation_note',
      delegation_note: {
        target: 'researcher',
        status: 'succeeded',
        card_id: `card-${seq}`,
        body: 'the report',
      },
      messages: [
        {
          role: 'user',
          content: {
            parts: [
              {
                type: 'text',
                text: '[delegated worker "researcher" finished: succeeded]\n\nthe report',
              },
            ],
          },
        },
      ],
      artifacts: [],
    };
  }

  // hydratedConversation puts an archived turn plus a live turn on
  // screen: the shape a conversation has while its run is still going.
  function hydratedConversation() {
    const state = useStore.getState();
    useStore.setState({
      conversations: {
        ...state.conversations,
        's-1': {
          ...state.conversations['s-1'],
          messages: [
            user('the question'),
            assistant('the first answer'),
            user('follow up'),
            assistant('working on it'),
          ],
          turnArtifacts: [
            {
              id: 'h-1',
              start: 0,
              seq: 1,
              runID: 'r-1',
              docs: [],
            },
            { id: 'live', start: 2, runID: 'r-2', docs: [] },
          ],
        },
      },
    });
  }

  it('appends a note the stream never carried to the open transcript', async () => {
    hydratedConversation();
    apiMock.turnsSince.mockResolvedValue([noteTurn(2)]);

    useStore.getState().handleEvent({
      type: 'session_updated',
      data: { id: 's-1' },
    });

    await vi.waitFor(() => {
      const conv = useStore.getState().conversations['s-1'];
      expect(conv.messages).toHaveLength(5);
    });
    const conv = useStore.getState().conversations['s-1'];
    // The read is a tail cursor, not a re-hydration: the transcript keeps
    // the turns it had and the card lands after them.
    expect(apiMock.turnsSince).toHaveBeenCalledWith('s-1', 1, 20);
    expect(conv.messages[4]).toMatchObject({
      role: 'user',
      kind: 'delegation_note',
    });
    expect(conv.messages[4].note?.target).toBe('researcher');
    expect(conv.turnArtifacts.map((t) => t.id)).toEqual(['h-1', 'live', 'h-2']);
    expect(conv.turnArtifacts[2].start).toBe(4);
  });

  it('leaves the transcript alone when the archive holds nothing new', async () => {
    hydratedConversation();
    const before = useStore.getState().conversations['s-1'];
    apiMock.turnsSince.mockResolvedValue([]);

    useStore.getState().handleEvent({
      type: 'session_updated',
      data: { id: 's-1' },
    });

    await vi.waitFor(() => {
      expect(apiMock.turnsSince).toHaveBeenCalled();
    });
    expect(useStore.getState().conversations['s-1']).toBe(before);
  });

  it('replaces a live turn with its archived copy at the turn end', async () => {
    hydratedConversation();
    // The live rows are what the stream built; the archive is the record
    // the turn is replaced by. The rebuilt row is the archive's, whatever
    // the live one happened to hold.
    const held = useStore.getState().conversations['s-1'];
    useStore.setState({
      conversations: {
        ...useStore.getState().conversations,
        's-1': {
          ...held,
          messages: [
            ...held.messages.slice(0, 3),
            {
              id: 'm-live',
              role: 'assistant',
              text: 'streamed answer',
              items: [
                {
                  kind: 'text',
                  id: 'p-1',
                  text: 'streamed answer',
                  chars: 15,
                },
              ],
              attachments: [],
            },
          ],
        },
      },
    });
    apiMock.turnByRunID.mockResolvedValue({
      seq: 3,
      at: '2026-09-03T00:06:00Z',
      status: 'completed',
      run_id: 'r-2',
      messages: [
        {
          role: 'user',
          content: { parts: [{ type: 'text', text: 'follow up' }] },
        },
        {
          role: 'assistant',
          content: { parts: [{ type: 'text', text: 'streamed answer' }] },
        },
      ],
      artifacts: [],
    });

    useStore.getState().handleEvent({
      type: 'turn_end',
      data: {
        run_id: 'r-2',
        conversation_id: 's-1',
        status: 'completed',
      },
    });

    await vi.waitFor(() => {
      const conv = useStore.getState().conversations['s-1'];
      const answer = conv.messages[conv.messages.length - 1];
      expect(answer.items.map((it) => it.kind)).toContain('text');
    });
    const conv = useStore.getState().conversations['s-1'];
    const answer = conv.messages[conv.messages.length - 1];
    const texts = answer.items.filter((it) => it.kind === 'text');
    expect(texts).toHaveLength(1);
    expect(texts.map((it) => it.text).join('')).toContain('streamed answer');
  });

  it('holds a note that arrives mid-turn until the turn ends', async () => {
    hydratedConversation();
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-2' });
    bindRun('r-2');
    // The note was written while the turn ran, and the turn's own row is
    // written when it ends: the note's seq is the older of the two.
    const parentTurn = {
      seq: 3,
      at: '2026-09-03T00:06:00Z',
      status: 'completed',
      run_id: 'r-2',
      messages: [
        {
          role: 'user',
          content: { parts: [{ type: 'text', text: 'follow up' }] },
        },
        {
          role: 'assistant',
          content: { parts: [{ type: 'text', text: 'the answer' }] },
        },
      ],
      artifacts: [],
    };
    apiMock.turnByRunID.mockResolvedValue(parentTurn);
    apiMock.turnsSince.mockResolvedValue([noteTurn(2), parentTurn]);

    useStore.getState().handleEvent({
      type: 'session_updated',
      data: { id: 's-1' },
    });
    // A running turn owns the tail: its deltas land on the last assistant
    // row, so a card folded in now would split the answer around it.
    expect(apiMock.turnsSince).not.toHaveBeenCalled();

    useStore.getState().handleEvent({
      type: 'turn_end',
      data: {
        run_id: 'r-2',
        conversation_id: 's-1',
        status: 'completed',
      },
    });

    await vi.waitFor(() => {
      const note = useStore
        .getState()
        .conversations['s-1'].messages.find(
          (m) => m.kind === 'delegation_note',
        );
      expect(note?.note?.body).toBe('the report');
    });
    const conv = useStore.getState().conversations['s-1'];
    // The wait is what makes this work: the anchor is the seq the
    // transcript held when the note arrived, so the row that is *older*
    // than the finished turn is still found, placed where a full hydrate
    // renders it (the note was written first), and the finished turn is
    // not printed twice.
    expect(apiMock.turnsSince).toHaveBeenCalledWith('s-1', 1, 20);
    expect(conv.messages).toHaveLength(5);
    expect(conv.messages[2].kind).toBe('delegation_note');
    expect(conv.messages[3].text).toBe('follow up');
    // The finished turn is present once, as the archived copy the
    // reconciliation put back (the run id in the fresh read matched it).
    const answer = conv.messages[4].items
      .filter((it) => it.kind === 'text')
      .map((it) => it.text)
      .join('');
    expect(answer).toContain('the answer');
    expect(conv.turnArtifacts.map((t) => t.id)).toEqual(['h-1', 'h-2', 'h-3']);
    // The reconciled turn keeps its archived identity (seq 3) and the
    // note is spliced in above it, where the archive's write order puts
    // it: the note was appended while that turn was still running.
    expect(conv.turnArtifacts).toMatchObject([
      { id: 'h-1', start: 0, seq: 1 },
      { id: 'h-2', start: 2, seq: 2 },
      { id: 'h-3', start: 3, seq: 3 },
    ]);
  });
});
