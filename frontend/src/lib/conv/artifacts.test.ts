// conv/artifacts.ts: pending interactions and the per-run artifact
// strip a turn writes into.

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useStore } from '../store';
import { apiMock } from '../../test/apiMock';
import {
  conversationForRun,
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

describe('interactions and artifacts', () => {
  it('adds and removes pending interactions', () => {
    const handle = useStore.getState().handleEvent;
    handle({
      type: 'interact',
      data: {
        id: 'p-1',
        run_id: 'r-1',
        conversation_id: 's-1',
        kind: 'confirm',
        severity: 'notice',
        title: 'Allow?',
        body: [],
        options: [],
        multi: false,
        allow_other: false,
        source: 'test',
      },
    });
    expect(
      useStore.getState().conversations['s-1'].pendingInteracts,
    ).toHaveLength(1);
    expect(useStore.getState().pendingPromptConvs['p-1']).toBe('s-1');

    handle({ type: 'resolved', data: { id: 'p-1' } });
    expect(
      useStore.getState().conversations['s-1'].pendingInteracts,
    ).toHaveLength(0);
    expect(useStore.getState().pendingPromptConvs['p-1']).toBeUndefined();
  });

  it('artifact events land on the strip of the run that wrote the file', () => {
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          turnArtifacts: [
            {
              id: 'turn-1',
              start: 0,
              runID: 'r-1',
              docs: [{ path: 'a.md', bytes: 1 }],
            },
            // A delegation note the app appended while r-1 is still
            // running: it is the last strip, and the file is not its.
            { id: 'turn-2', start: 1, runID: 'subagent:card-1', docs: [] },
          ],
        },
      },
    });
    useStore.getState().handleEvent({
      type: 'artifact',
      data: {
        conversation_id: 's-1',
        run_id: 'r-1',
        path: 'a.md',
        bytes: 42,
      },
    });
    const strips = useStore.getState().conversations['s-1'].turnArtifacts;
    expect(strips[0].docs).toEqual([{ path: 'a.md', bytes: 42 }]);
    expect(strips[1].docs).toEqual([]);
  });

  it('artifact events wait for the live strip while the run id is in flight', () => {
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          turnArtifacts: [
            { id: 'turn-1', start: 0, seq: 7, docs: [] },
            { id: 'turn-2', start: 1, docs: [] },
          ],
        },
      },
    });
    const handle = useStore.getState().handleEvent;
    // The start-turn response has not landed yet, so the run the event
    // names is unknown: the trailing live strip owns it.
    handle({
      type: 'artifact',
      data: { conversation_id: 's-1', run_id: 'r-new', path: 'a.md', bytes: 5 },
    });
    let strips = useStore.getState().conversations['s-1'].turnArtifacts;
    expect(strips[1].docs).toEqual([{ path: 'a.md', bytes: 5 }]);

    // Once the trailing strip is an archived or owned one, an unknown
    // run has no strip to merge into and the event is dropped.
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          turnArtifacts: [
            { id: 'turn-1', start: 0, seq: 7, docs: [] },
            { id: 'turn-2', start: 1, runID: 'r-1', docs: [] },
          ],
        },
      },
    });
    handle({
      type: 'artifact',
      data: {
        conversation_id: 's-1',
        run_id: 'r-gone',
        path: 'b.md',
        bytes: 9,
      },
    });
    strips = useStore.getState().conversations['s-1'].turnArtifacts;
    expect(strips[0].docs).toEqual([]);
    expect(strips[1].docs).toEqual([]);
  });

  it('an automation run on the open conversation writes into its own strip', async () => {
    const handle = useStore.getState().handleEvent;
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          messages: [
            {
              id: 'm-1',
              role: 'user',
              text: 'earlier',
              items: [],
              attachments: [],
            },
            {
              id: 'm-2',
              role: 'assistant',
              text: 'earlier answer',
              items: [],
              attachments: [],
            },
          ],
          turnArtifacts: [
            { id: 'h-1', start: 0, seq: 1, runID: 'r-old', docs: [] },
          ],
        },
      },
    });

    // The scheduler fires on the conversation on screen: the UI never
    // started this run, so the turn its rows and files belong to has to
    // come from the run-start event — the task's message as the user row
    // plus the strip that owns it.
    handle({
      type: 'automation_run_started',
      data: {
        run_id: 'r-auto',
        conversation_id: 's-1',
        message: 'write the brief',
      },
    });
    let strips = useStore.getState().conversations['s-1'].turnArtifacts;
    expect(strips).toHaveLength(2);
    expect(strips[1]).toMatchObject({ runID: 'r-auto', start: 2, docs: [] });
    expect(useStore.getState().conversations['s-1'].messages[2]).toMatchObject({
      role: 'user',
      text: 'write the brief',
    });

    handle({
      type: 'stream',
      data: {
        run_id: 'r-auto',
        conversation_id: 's-1',
        delta: {
          type: 'part',
          part: { type: 'text', text: 'writing the brief' },
        },
      },
    });
    handle({
      type: 'artifact',
      data: {
        conversation_id: 's-1',
        run_id: 'r-auto',
        path: 'reports/brief.md',
        bytes: 42,
      },
    });
    strips = useStore.getState().conversations['s-1'].turnArtifacts;
    expect(strips[1].docs).toEqual([{ path: 'reports/brief.md', bytes: 42 }]);
    // The file is the automation's, not the turn sitting above it.
    expect(strips[0].docs).toEqual([]);
    // The streamed answer is the run's own row, not the tail of the
    // previous turn's answer.
    const live = useStore.getState().conversations['s-1'].messages;
    expect(live).toHaveLength(4);
    expect(live[3]).toMatchObject({ role: 'assistant' });
    expect(live[3].items).toEqual([
      expect.objectContaining({ kind: 'text', text: 'writing the brief' }),
    ]);

    // The turn ends: the strip carries the terminal state live, and then
    // the archived copy replaces it — the transcript a reload draws.
    apiMock.turnByRunID.mockResolvedValue({
      ...historyTurn(2, 'write the brief', 'wrote it'),
      run_id: 'r-auto',
      status: 'completed',
      artifacts: [{ path: 'reports/brief.md', bytes: 42 }],
    });
    handle({
      type: 'turn_end',
      data: {
        run_id: 'r-auto',
        conversation_id: 's-1',
        status: 'completed',
        duration_ms: 1500,
      },
    });
    strips = useStore.getState().conversations['s-1'].turnArtifacts;
    expect(strips[1]).toMatchObject({
      status: 'completed',
      durationMs: 1500,
      docs: [{ path: 'reports/brief.md', bytes: 42 }],
    });
    expect(conversationForRun('r-auto')).toBeUndefined();

    await vi.waitFor(() => {
      const conv = useStore.getState().conversations['s-1'];
      expect(conv.turnArtifacts[1]).toMatchObject({
        seq: 2,
        runID: 'r-auto',
        docs: [{ path: 'reports/brief.md', bytes: 42 }],
      });
      // The archive's copy replaces the live one: one prompt row and the
      // archived answer, not the live pair beside them.
      expect(
        conv.messages.filter((m) => m.text === 'write the brief'),
      ).toHaveLength(1);
      const texts = conv.messages.flatMap((m) =>
        m.items.flatMap((it) => (it.kind === 'text' ? [it.text] : [])),
      );
      expect(texts).toContain('wrote it');
      expect(texts).not.toContain('writing the brief');
    });
  });

  // The prompt travels in the start event because it becomes the run's
  // user row. A payload from before that field existed carries none, and
  // the turn still has to open: the row is empty, but it is there, so the
  // deltas below it stay this run's instead of growing the turn above.
});
