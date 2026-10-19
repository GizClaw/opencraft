// conv/history.ts and conv/model.ts reached through a rebuild: what a
// steered row looks like once the transcript is read again - the
// delegation-note card, the archive tagging a delivered steer, and the
// undelivered rows a rebuild carries over instead of dropping.

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { COMPACT_SUMMARY_PREFIX } from '../compact';
import type { MessageView } from '../store';
import { stateRoot } from '../../state/app';
import { firstMessageTitle, itemText, useStore } from '../store';
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

describe('steered rows across transcript rebuilds', () => {
  function runningConversation(runID = 'r-run') {
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'SUBMIT', kind: 'send' });
    actor?.send({ type: 'RUN_STARTED', runID });
    bindRun(runID);
    return actor;
  }

  it('restores a delegation note turn as an app-authored card row', async () => {
    apiMock.resumeSession.mockResolvedValue({
      conversation_id: 's-note',
      mode: 'workspace',
      think: 'medium',
      model: '',
    });
    apiMock.sessionTurns.mockResolvedValue([
      {
        seq: 1,
        at: '2026-09-03T00:00:00Z',
        status: 'completed',
        run_id: 'subagent:card-1',
        kind: 'delegation_note',
        delegation_note: {
          target: 'researcher',
          status: 'succeeded',
          card_id: 'card-1',
          run_id: 'run-child',
          parent_run_id: 'run-parent',
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
      },
      historyTurn(2, 'the real question', 'the answer'),
    ]);

    await useStore.getState().resume('s-note');

    const conv = useStore.getState().conversations['s-note'];
    const note = conv.messages[0];
    expect(note.kind).toBe('delegation_note');
    expect(note.note?.target).toBe('researcher');
    expect(note.note?.body).toBe('the report');
    // The note does not name the conversation: the live title mirror
    // reads the first row the user wrote.
    expect(firstMessageTitle(conv.messages)).toBe('the real question');
  });

  it('tags an archived mid-turn user row as a delivered steer', async () => {
    apiMock.resumeSession.mockResolvedValue({
      conversation_id: 's-archive',
      mode: 'workspace',
      think: 'medium',
      model: '',
    });
    apiMock.sessionTurns.mockResolvedValue([
      {
        seq: 1,
        at: '2026-09-03T00:00:00Z',
        status: 'succeeded',
        messages: [
          {
            role: 'user',
            content: { parts: [{ type: 'text', text: 'the question' }] },
          },
          {
            role: 'assistant',
            content: { parts: [{ type: 'text', text: 'starting on it' }] },
          },
          {
            role: 'tool',
            content: {
              parts: [
                {
                  type: 'tool_result',
                  result: {
                    call_id: 'c-1',
                    content: [{ type: 'text', text: 'ok' }],
                  },
                },
              ],
            },
          },
          // The steer node appends the interjection as a plain user
          // message at the round boundary: it is the second user row of
          // the turn, and a resume has to keep rendering it as one.
          {
            role: 'user',
            content: { parts: [{ type: 'text', text: 'also check the docs' }] },
          },
          {
            role: 'assistant',
            content: { parts: [{ type: 'text', text: 'done' }] },
          },
        ],
        artifacts: [],
      },
    ]);

    await useStore.getState().resume('s-archive');

    const conv = useStore.getState().conversations['s-archive'];
    const rows = conv.messages.filter((m) => m.role === 'user');
    expect(rows.map((m) => [m.text, m.steer ?? 'ask'])).toEqual([
      ['the question', 'ask'],
      ['also check the docs', 'delivered'],
    ]);
  });

  it('does not tag a second turn, a summary, or a steer that precedes the reply', async () => {
    apiMock.resumeSession.mockResolvedValue({
      conversation_id: 's-archive',
      mode: 'workspace',
      think: 'medium',
      model: '',
    });
    apiMock.sessionTurns.mockResolvedValue([
      {
        seq: 1,
        at: '2026-09-03T00:00:00Z',
        status: 'succeeded',
        messages: [
          // A compaction summary is user-role context, not the user
          // speaking, and a world-state row ahead of the ask must not
          // consume the "first user row" slot either.
          {
            role: 'user',
            content: {
              parts: [
                {
                  type: 'text',
                  text: `${COMPACT_SUMMARY_PREFIX}\nfolded history`,
                },
              ],
            },
          },
          {
            role: 'user',
            content: { parts: [{ type: 'text', text: 'first question' }] },
          },
          {
            role: 'assistant',
            content: { parts: [{ type: 'text', text: 'first answer' }] },
          },
        ],
        artifacts: [],
      },
      {
        seq: 2,
        at: '2026-09-03T00:01:00Z',
        status: 'succeeded',
        messages: [
          {
            role: 'user',
            content: { parts: [{ type: 'text', text: 'second question' }] },
          },
          {
            role: 'assistant',
            content: { parts: [{ type: 'text', text: 'second answer' }] },
          },
        ],
        artifacts: [],
      },
    ]);

    await useStore.getState().resume('s-archive');

    const rows = useStore
      .getState()
      .conversations['s-archive'].messages.filter((m) => m.role === 'user');
    expect(rows.every((m) => m.steer === undefined)).toBe(true);
  });

  it('carries undelivered rows across a transcript rebuild', async () => {
    runningConversation();
    await useStore.getState().steer('lost note');
    useStore.getState().handleEvent({
      type: 'turn_end',
      data: {
        run_id: 'r-run',
        conversation_id: 's-1',
        status: 'completed',
        steer_pending: 1,
      },
    });
    await new Promise((resolve) => setTimeout(resolve, 0));

    // The archive cannot hold that text (nothing appended it), so a
    // rebuild from archived turns has to carry the row over: it is the
    // only copy.
    apiMock.sessionTurns.mockResolvedValue([
      historyTurn(1, 'the question', 'the answer'),
    ]);
    await useStore.getState().retryTranscript('s-1');

    const conv = useStore.getState().conversations['s-1'];
    expect(conv.messages.at(-1)).toMatchObject({
      role: 'user',
      text: 'lost note',
      steer: 'undelivered',
    });
    // The archived turn is back too: the carried row is appended after
    // the rebuilt transcript, not instead of it. Assistant text lives in
    // the message's items, so it is read through itemText.
    expect(
      conv.messages
        .flatMap((m) => m.items)
        .filter(
          (it): it is Extract<MessageView['items'][number], { kind: 'text' }> =>
            it.kind === 'text',
        )
        .map((it) => itemText(it)),
    ).toContain('the answer');
  });

  it('carries undelivered rows across a resume hydration', async () => {
    runningConversation();
    await useStore.getState().steer('lost note');
    useStore.getState().handleEvent({
      type: 'turn_end',
      data: {
        run_id: 'r-run',
        conversation_id: 's-1',
        status: 'completed',
        steer_pending: 1,
      },
    });
    await new Promise((resolve) => setTimeout(resolve, 0));

    apiMock.resumeSession.mockResolvedValue({
      conversation_id: 's-1',
      mode: 'workspace',
      think: 'medium',
      model: '',
    });
    apiMock.sessionTurns.mockResolvedValue([
      historyTurn(1, 'the question', 'the answer'),
    ]);
    useStore.setState({ workspace: '/tmp/other' });
    await useStore.getState().resume('s-1');

    const conv = useStore.getState().conversations['s-1'];
    expect(conv.messages.at(-1)).toMatchObject({
      text: 'lost note',
      steer: 'undelivered',
    });
  });

  it('does not print a carried row the archive already holds', async () => {
    runningConversation();
    await useStore.getState().steer('taken note');
    // The boundary did take this message, but the run's own terminal
    // event was lost: the conversation moved on to a replacement run,
    // whose turn_end settles the row as undelivered (nothing left to
    // classify it with) and carries it as the only copy of the text.
    runningConversation('r-new');
    useStore.getState().handleEvent({
      type: 'turn_end',
      data: {
        run_id: 'r-new',
        conversation_id: 's-1',
        status: 'completed',
        steer_pending: 0,
      },
    });
    const steers = () =>
      useStore.getState().conversations['s-1'].messages.filter((m) => m.steer);
    expect(steers().map((m) => m.steer)).toEqual(['undelivered']);

    // A rebuild that finds the same text archived as a delivered
    // interjection is the truth: the carried row would print the words
    // twice.
    apiMock.sessionTurns.mockResolvedValue([
      {
        seq: 1,
        at: '2026-09-03T00:00:00Z',
        status: 'succeeded',
        messages: [
          {
            role: 'user',
            content: { parts: [{ type: 'text', text: 'the question' }] },
          },
          {
            role: 'assistant',
            content: { parts: [{ type: 'text', text: 'working on it' }] },
          },
          {
            role: 'user',
            content: { parts: [{ type: 'text', text: 'taken note' }] },
          },
          {
            role: 'assistant',
            content: { parts: [{ type: 'text', text: 'done' }] },
          },
        ],
        artifacts: [],
      },
    ]);
    await useStore.getState().retryTranscript('s-1');

    const rows = useStore
      .getState()
      .conversations['s-1'].messages.filter((m) => m.text === 'taken note');
    expect(rows.map((m) => m.steer)).toEqual(['delivered']);
  });
});
