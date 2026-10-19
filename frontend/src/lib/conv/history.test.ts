// conv/history.ts: archived turns rendered into the transcript -
// hydrate, merge with a live shell, and the shapes of an archived row.

import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { MessageView } from '../store';
import { stateRoot } from '../../state/app';
import { useStore } from '../store';
import { apiMock } from '../../test/apiMock';
import {
  actorValue,
  historyTurn,
  primeStoreTest,
} from '../../test/storeHarness';

vi.mock('../api', async () => ({
  api: (await import('../../test/apiMock')).apiMock,
}));
vi.mock(
  '../../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/diagnostics',
  async () => (await import('../../test/apiMock')).diagnosticsMock,
);

beforeEach(primeStoreTest);

describe('hydrated history', () => {
  it('resume applies the snapshot and loads history into a ready conversation', async () => {
    apiMock.sessionTurns.mockResolvedValue([
      historyTurn(1, 'history user', 'history answer'),
    ]);

    await useStore.getState().resume('s-2');

    const state = useStore.getState();
    expect(stateRoot.focusSnapshot.value).toBe('active');
    expect(stateRoot.focusSnapshot.context.sessionID).toBe('s-2');
    const conv = state.conversations['s-2'];
    expect(actorValue('s-2')?.transcript).toBe('ready');
    expect(conv.mode).toBe('workspace');
    expect(conv.think).toBe('medium');
    expect(conv.messages.map((m) => m.text || '')).toContain('history user');
    // Startup hydration asks for the newest page plus one turn, which is
    // how the store learns whether older history exists.
    expect(apiMock.sessionTurns).toHaveBeenCalledWith('s-2', 7, 0);
  });

  it('resume keeps archived turn status on the turn artifact', async () => {
    apiMock.sessionTurns.mockResolvedValue([
      {
        ...historyTurn(1, 'history user', 'partial answer'),
        status: 'failed',
        error: 'engine boom',
        interrupt_cause: 'host_shutdown',
        error_kind: 'provider_failure',
      },
    ]);

    await useStore.getState().resume('s-2');

    const conv = useStore.getState().conversations['s-2'];
    expect(conv.turnArtifacts[0]).toMatchObject({
      status: 'failed',
      error: 'engine boom',
      interruptCause: 'host_shutdown',
      errorKind: 'provider_failure',
    });
  });

  // The renderer keeps no compatibility rules of its own: archived
  // content is shown verbatim. The old `> ⛔ ...` failure marker this
  // test used to strip was written into the message *view* by
  // pre-archive-column builds and was never persisted, so there is no
  // stored shape to clean up (see internal/foundation/compat).

  it('resume renders archived text verbatim', async () => {
    apiMock.sessionTurns.mockResolvedValue([
      {
        seq: 1,
        at: '2026-09-03T00:00:00Z',
        status: 'failed',
        messages: [
          {
            role: 'user',
            content: { parts: [{ type: 'text', text: 'history user' }] },
          },
          {
            role: 'assistant',
            content: {
              parts: [
                {
                  type: 'text',
                  text: 'partial\n\n> ⛔ graph "opencraft-assistant" node "llm": provider_failure during generate',
                },
              ],
            },
          },
        ],
        artifacts: [],
      },
    ]);

    await useStore.getState().resume('s-2');

    const conv = useStore.getState().conversations['s-2'];
    const assistantTexts = conv.messages
      .filter((m) => m.role === 'assistant')
      .flatMap((m) =>
        m.items
          .filter(
            (
              it,
            ): it is Extract<MessageView['items'][number], { kind: 'text' }> =>
              it.kind === 'text',
          )
          .map((it) => it.text),
      );
    expect(assistantTexts).toEqual([
      'partial\n\n> ⛔ graph "opencraft-assistant" node "llm": provider_failure during generate',
    ]);
    expect(conv.turnArtifacts[0].status).toBe('failed');
  });

  // The archive stores a tool result as the ordered parts the tool
  // returned (flowcraft core v0.4.0), so resuming reads the text of
  // those parts rather than a single flattened string.

  it('resume renders an archived tool result from its content parts', async () => {
    apiMock.sessionTurns.mockResolvedValue([
      {
        seq: 1,
        at: '2026-09-10T02:53:57Z',
        status: 'succeeded',
        messages: [
          {
            role: 'assistant',
            content: {
              parts: [
                {
                  type: 'tool_call',
                  call: {
                    id: 'call-1',
                    name: 'exec_command',
                    arguments: { command: 'ls' },
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
                    call_id: 'call-1',
                    content: {
                      parts: [
                        {
                          type: 'text',
                          text: '{"exit_code":0,"stdout":"README.md\\n"}',
                        },
                        {
                          type: 'image',
                          source: {
                            kind: 'inline',
                            media_type: 'image/jpeg',
                            data: 'QUJD',
                          },
                        },
                      ],
                    },
                  },
                },
              ],
            },
          },
        ],
        artifacts: [],
      },
    ]);

    await useStore.getState().resume('s-2');

    const items = useStore.getState().conversations['s-2'].messages[0].items;
    expect(items).toHaveLength(1);
    expect(items[0]).toMatchObject({
      kind: 'tool_call',
      tool: {
        id: 'call-1',
        name: 'exec_command',
        status: 'done',
        result: '{"exit_code":0,"stdout":"README.md\\n"}',
      },
    });
    const item = items[0];
    if (item.kind !== 'tool_call') throw new Error('expected a tool call');
    // An archived call carries no per-call timing, so it must not grow a
    // duration this client never measured; the image part it does carry
    // is kept as the model's frame.
    expect(item.tool.seenAt).toBeUndefined();
    expect(item.tool.images).toEqual([
      { data_url: 'data:image/jpeg;base64,QUJD', media_type: 'image/jpeg' },
    ]);
  });

  it('does not duplicate an archived assistant message', async () => {
    apiMock.sessionTurns.mockResolvedValue([
      historyTurn(1, 'history user', 'history answer'),
    ]);

    await useStore.getState().resume('s-2');

    const conv = useStore.getState().conversations['s-2'];
    const assistantTexts = conv.messages.flatMap((m) =>
      m.role === 'user'
        ? []
        : m.items
            .filter(
              (
                it,
              ): it is Extract<
                MessageView['items'][number],
                { kind: 'text' }
              > => it.kind === 'text',
            )
            .map((it) => it.text),
    );
    expect(conv.messages).toHaveLength(2);
    expect(assistantTexts.filter((t) => t === 'history answer')).toHaveLength(
      1,
    );
  });

  it('resume merges history with an active live shell', async () => {
    const live = stateRoot.registry.ensure('s-2', {
      workspaceGeneration: stateRoot.generation(),
    });
    live?.send({ type: 'RUN_STARTED', runID: 'r-live' });
    useStore.setState({
      conversations: {
        ...useStore.getState().conversations,
        's-2': {
          ...useStore.getState().conversations['s-1'],
          messages: [
            {
              id: 'live-1',
              role: 'assistant',
              text: '',
              items: [{ kind: 'text', id: 'live-text', text: 'live answer' }],
              attachments: [],
            },
          ],
        },
      },
    });
    apiMock.sessionTurns.mockResolvedValue([
      historyTurn(1, 'history user', 'history answer'),
    ]);

    await useStore.getState().resume('s-2');

    const conv = useStore.getState().conversations['s-2'];
    expect(actorValue('s-2')?.transcript).toBe('ready');
    const texts = conv.messages.flatMap((m) =>
      m.role === 'user'
        ? [m.text]
        : m.items
            .filter(
              (
                it,
              ): it is Extract<
                MessageView['items'][number],
                { kind: 'text' }
              > => it.kind === 'text',
            )
            .map((it) => it.text),
    );
    expect(texts).toContain('history user');
    expect(texts).toContain('history answer');
    expect(conv.messages[conv.messages.length - 1].items).toContainEqual(
      expect.objectContaining({ kind: 'text', text: 'live answer' }),
    );
    expect(stateRoot.focusSnapshot.value).toBe('active');
    expect(stateRoot.focusSnapshot.context.sessionID).toBe('s-2');
  });
});
