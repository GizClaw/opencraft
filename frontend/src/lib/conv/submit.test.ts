// conv/turn.ts: submissions and the turn queue - what a send does to the
// run it starts, the one staged draft, barge-in start failures, cancel
// and fork.

import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { MessageView } from '../store';
import { stateRoot } from '../../state/app';
import { useStore } from '../store';
import { apiMock } from '../../test/apiMock';
import {
  actorValue,
  assistantTexts,
  bindRun,
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

describe('submissions and the turn queue', () => {
  it('send appends the user message and registers the run', async () => {
    await useStore.getState().send('hello');

    const conv = useStore.getState().conversations['s-1'];
    expect(actorValue('s-1')?.turn).toBe('running');
    expect(conv.messages[0]).toMatchObject({
      role: 'user',
      text: 'hello',
    });
    expect(conversationForRun('r-1')).toBe('s-1');
    expect(apiMock.startTurn).toHaveBeenCalledWith(
      's-1',
      expect.objectContaining({ role: 'user' }),
      '/tmp/w',
    );
  });

  it('ignores send while busy or unconfigured', async () => {
    stateRoot.registry.get('s-1')?.send({ type: 'SUBMIT', kind: 'send' });
    await useStore.getState().send('ignored');
    expect(apiMock.startTurn).not.toHaveBeenCalled();

    useStore.setState({ configured: false });
    await useStore.getState().send('also ignored');
    expect(apiMock.startTurn).not.toHaveBeenCalled();
  });

  it('refuses every submission into a deleted conversation', async () => {
    // The delete path drops the conversation from the store, so this is
    // the belt behind it: the table's deleted gate, which the machine's
    // own guards carry too, keeps the four submission paths from
    // spending an IPC on a conversation that is gone.
    stateRoot.registry
      .get('s-1')
      ?.send({ type: 'SESSION_DELETED', deletedAt: 'now' });

    expect(useStore.getState().queueInput('queued')).toBe(false);
    expect(await useStore.getState().sendInterrupt('interrupt')).toBe(false);
    expect(await useStore.getState().steer('steer')).toBe(false);
    await useStore.getState().send('send');

    expect(apiMock.startTurn).not.toHaveBeenCalled();
    expect(apiMock.steerTurn).not.toHaveBeenCalled();
    const conv = useStore.getState().conversations['s-1'];
    expect(conv?.queued).toBeUndefined();
    expect(conv?.messages).toEqual([]);
  });

  it('sendInterrupt barges in while a turn is running', async () => {
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'SUBMIT', kind: 'send' });
    actor?.send({ type: 'RUN_STARTED', runID: 'r-old' });
    apiMock.startTurn.mockResolvedValue({
      run_id: 'r-new',
      context_id: 's-1',
    });

    const ok = await useStore.getState().sendInterrupt('second');

    expect(ok).toBe(true);
    const conv = useStore.getState().conversations['s-1'];
    expect(conv.messages.at(-1)).toMatchObject({
      role: 'user',
      text: 'second',
    });
    expect(conversationForRun('r-new')).toBe('s-1');
    expect(actorValue('s-1')?.turn).toBe('running');
    expect(actor?.getSnapshot().context).toMatchObject({
      currentRunID: 'r-new',
    });

    // The superseded run's terminal event stays inert.
    actor?.send({
      type: 'TURN_ENDED',
      runID: 'r-old',
      status: 'interrupted',
    });
    expect(actorValue('s-1')?.turn).toBe('running');
    expect(actor?.getSnapshot().context).toMatchObject({
      currentRunID: 'r-new',
    });
  });

  it('queueInput stages one draft and drains it after turn_end', async () => {
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'SUBMIT', kind: 'send' });
    actor?.send({ type: 'RUN_STARTED', runID: 'r-old' });

    expect(useStore.getState().queueInput('staged')).toBe(true);
    expect(useStore.getState().conversations['s-1']?.queued).toMatchObject({
      text: 'staged',
      interrupt: false,
    });
    expect(apiMock.startTurn).not.toHaveBeenCalled();

    apiMock.startTurn.mockResolvedValue({
      run_id: 'r-new',
      context_id: 's-1',
    });
    useStore.getState().handleEvent({
      type: 'turn_end',
      data: {
        run_id: 'r-old',
        conversation_id: 's-1',
        status: 'completed',
      },
    });
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(useStore.getState().conversations['s-1']?.queued).toBeUndefined();
    expect(
      useStore.getState().conversations['s-1'].messages.at(-1),
    ).toMatchObject({ role: 'user', text: 'staged' });
    expect(conversationForRun('r-new')).toBe('s-1');
    expect(actorValue('s-1')?.turn).toBe('running');
  });

  it('drains a staged draft into the workspace that owns the conversation', async () => {
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'SUBMIT', kind: 'send' });
    actor?.send({ type: 'RUN_STARTED', runID: 'r-old' });
    expect(useStore.getState().queueInput('staged elsewhere')).toBe(true);

    // The user switched to another workspace while the queued turn was
    // still running: the drain must keep targeting the conversation's
    // own workspace instead of the one now on screen, otherwise the
    // start lands in the wrong workspace's session store.
    useStore.setState({ workspace: '/tmp/other' });
    useStore.getState().handleEvent({
      type: 'turn_end',
      data: {
        run_id: 'r-old',
        conversation_id: 's-1',
        status: 'completed',
      },
    });
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(apiMock.startTurn).toHaveBeenCalledWith(
      's-1',
      expect.objectContaining({ role: 'user' }),
      '/tmp/w',
    );
  });

  it('an Enter during starting fires the draft when the run starts', async () => {
    let resolveFirst!: (value: { run_id: string; context_id: string }) => void;
    apiMock.startTurn
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveFirst = resolve;
          }),
      )
      .mockResolvedValue({ run_id: 'r-second', context_id: 's-1' });

    const first = useStore.getState().send('first');
    expect(actorValue('s-1')?.turn).toBe('starting');

    const ok = await useStore.getState().sendInterrupt('second');
    expect(ok).toBe(true);
    expect(useStore.getState().conversations['s-1']?.queued).toMatchObject({
      text: 'second',
      interrupt: true,
    });

    resolveFirst({ run_id: 'r-first', context_id: 's-1' });
    await first;
    await new Promise((resolve) => setTimeout(resolve, 0));

    const conv = useStore.getState().conversations['s-1'];
    expect(conv.queued).toBeUndefined();
    expect(conv.messages.map((m) => m.text)).toEqual(['first', 'second']);
    expect(conversationForRun('r-second')).toBe('s-1');
    expect(actorValue('s-1')?.turn).toBe('running');
    expect(actorValue('s-1')?.turn).not.toBe('starting');
  });

  it('keeps a Tab draft staged when the turn it waited for fails', async () => {
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'SUBMIT', kind: 'send' });
    actor?.send({ type: 'RUN_STARTED', runID: 'r-old' });
    bindRun('r-old');

    expect(useStore.getState().queueInput('staged')).toBe(true);
    useStore.getState().handleEvent({
      type: 'turn_end',
      data: {
        run_id: 'r-old',
        conversation_id: 's-1',
        status: 'failed',
        error: 'engine boom',
        request_id: 'req-1',
        response_id: 'resp-1',
      },
    });

    expect(actorValue('s-1')?.turn).toBe('failed');
    expect(useStore.getState().conversations['s-1']?.queued).toMatchObject({
      text: 'staged',
      interrupt: false,
    });
    expect(apiMock.startTurn).not.toHaveBeenCalled();

    // A fresh manual send supersedes the stale draft.
    apiMock.startTurn.mockResolvedValue({
      run_id: 'r-new',
      context_id: 's-1',
    });
    await useStore.getState().send('next');
    const conv = useStore.getState().conversations['s-1'];
    expect(conv.queued).toBeUndefined();
    expect(conv.messages.at(-1)).toMatchObject({
      role: 'user',
      text: 'next',
    });
  });

  it('restores the superseded run when a barge-in start fails', async () => {
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'SUBMIT', kind: 'send' });
    actor?.send({ type: 'RUN_STARTED', runID: 'r-old' });
    bindRun('r-old');
    apiMock.startTurn.mockRejectedValueOnce(new Error('start boom'));

    const ok = await useStore.getState().sendInterrupt('second');

    expect(ok).toBe(true);
    expect(actorValue('s-1')?.turn).toBe('running');
    expect(actor?.getSnapshot().context).toMatchObject({
      currentRunID: 'r-old',
      supersededRunID: undefined,
    });
    const conv = useStore.getState().conversations['s-1'];
    expect(conv.messages.at(-1)).toMatchObject({
      role: 'user',
      text: 'second',
    });
    expect(conv.turnArtifacts.at(-1)).toMatchObject({
      status: 'failed',
      error: 'Error: start boom',
    });

    // The restored run's own terminal event still ends the turn.
    actor?.send({ type: 'TURN_ENDED', runID: 'r-old', status: 'completed' });
    expect(actorValue('s-1')?.turn).toBe('succeeded');
  });

  it('keeps watching a run it adopted when a barge-in over it fails', async () => {
    // The run was adopted off the wire — no send of this window opened
    // it — so the conversation's run map is the only record that it is
    // still live. A barge-in that cannot start has to find it there and
    // resume watching its streams.
    useStore.getState().handleEvent({
      type: 'stream',
      data: {
        run_id: 'r-new',
        conversation_id: 's-1',
        delta: { type: 'part', part: { type: 'text', text: 'streaming' } },
      },
    });
    useStore.getState().flushStreams();
    expect(actorValue('s-1')?.turn).toBe('running');
    expect(conversationForRun('r-new')).toBe('s-1');
    apiMock.startTurn.mockRejectedValueOnce(new Error('start boom'));

    const ok = await useStore.getState().sendInterrupt('second');

    expect(ok).toBe(true);
    const actor = stateRoot.registry.get('s-1');
    expect(actorValue('s-1')?.turn).toBe('running');
    expect(actor?.getSnapshot().context).toMatchObject({
      currentRunID: 'r-new',
      supersededRunID: undefined,
    });
    // The send that never started is reported on its own turn.
    expect(
      useStore.getState().conversations['s-1'].turnArtifacts.at(-1),
    ).toMatchObject({ status: 'failed', error: 'Error: start boom' });

    // The adopted run's own terminal event still ends the turn.
    actor?.send({ type: 'TURN_ENDED', runID: 'r-new', status: 'completed' });
    expect(actorValue('s-1')?.turn).toBe('succeeded');
  });

  it('absorbs the superseded terminal when a barge-in start fails after it ended', async () => {
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'SUBMIT', kind: 'send' });
    actor?.send({ type: 'RUN_STARTED', runID: 'r-old' });
    const base = useStore.getState().conversations['s-1'];
    bindRun('r-old');
    useStore.setState({
      conversations: {
        's-1': {
          ...base,
          messages: [
            {
              id: 'm-old',
              role: 'user',
              text: 'old',
              items: [],
              attachments: [],
            },
          ],
          turnArtifacts: [{ id: 't-old', start: 0, runID: 'r-old', docs: [] }],
        },
      },
    });
    let rejectStart!: (err: Error) => void;
    apiMock.startTurn.mockImplementationOnce(
      () =>
        new Promise((_resolve, reject) => {
          rejectStart = reject;
        }),
    );

    const pending = useStore.getState().sendInterrupt('second');
    expect(actorValue('s-1')?.turn).toBe('starting');
    // The old run ends while the replacement is still starting.
    useStore.getState().handleEvent({
      type: 'turn_end',
      data: {
        run_id: 'r-old',
        conversation_id: 's-1',
        status: 'interrupted',
        error: 'engine boom',
      },
    });
    expect(conversationForRun('r-old')).toBeUndefined();

    rejectStart(new Error('start boom'));
    await pending;

    expect(actorValue('s-1')?.turn).toBe('failed');
    expect(actor?.getSnapshot().context).toMatchObject({
      currentRunID: undefined,
      supersededRunID: undefined,
      endedRunIDs: ['r-old'],
      failureStatus: 'interrupted',
      turnError: 'engine boom',
    });
    expect(
      useStore.getState().conversations['s-1'].turnArtifacts.at(-1),
    ).toMatchObject({
      status: 'failed',
      error: 'Error: start boom',
    });
  });

  // A plain send that fails produces no run, and the ended-run memory
  // must survive it: the synthetic terminal event with an empty run id
  // the catch path used to send wiped the only slot, so the next event
  // - a late delta of a run that had already ended - read as a new live
  // run and put the conversation into a turn it could never finish.

  it('a failed plain send cannot reopen a run that already ended', async () => {
    const actor = stateRoot.registry.get('s-1');
    bindRun('r-1');
    actor?.send({ type: 'SUBMIT', kind: 'send' });
    actor?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    actor?.send({ type: 'TURN_ENDED', runID: 'r-1', status: 'completed' });
    expect(actorValue('s-1')?.turn).toBe('succeeded');

    apiMock.startTurn.mockRejectedValueOnce(new Error('start boom'));
    await useStore.getState().send('next');
    expect(actorValue('s-1')?.turn).toBe('failed');

    useStore.getState().handleEvent({
      type: 'stream',
      data: {
        run_id: 'r-1',
        conversation_id: 's-1',
        delta: { type: 'part', part: { type: 'text', text: 'late tail' } },
      },
    });
    useStore.getState().flushStreams();

    expect(actorValue('s-1')?.turn).toBe('failed');
    const conv = useStore.getState().conversations['s-1'];
    // The delta opened no strip for the finished run and appended no
    // text: the strip list is still the failed send's own entry.
    expect(conv.turnArtifacts).toHaveLength(1);
    expect(conv.turnArtifacts[0].runID).toBeUndefined();
    expect(assistantTexts(conv.messages)).toEqual([]);
  });

  it('cancelRun during a barge-in start cancels the superseded run', async () => {
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'SUBMIT', kind: 'send' });
    actor?.send({ type: 'RUN_STARTED', runID: 'r-old' });
    // Enter again: the replacement is starting and waiting for r-old
    // to finalize, so Stop must target the superseded run.
    actor?.send({ type: 'SUBMIT', kind: 'interrupt' });
    expect(actor?.getSnapshot().context).toMatchObject({
      supersededRunID: 'r-old',
    });

    await useStore.getState().cancelRun();

    expect(apiMock.cancelTurn).toHaveBeenCalledWith('r-old');
    expect(useStore.getState().statusText).toBe('');
  });

  it('cancelRun ignores a not-found superseded run', async () => {
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'SUBMIT', kind: 'send' });
    actor?.send({ type: 'RUN_STARTED', runID: 'r-old' });
    actor?.send({ type: 'SUBMIT', kind: 'interrupt' });
    apiMock.cancelTurn.mockRejectedValue(new Error('host: turn not found'));

    await useStore.getState().cancelRun();

    expect(useStore.getState().statusText).toBe('');
  });

  it('takeQueued returns and clears the staged draft', () => {
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'SUBMIT', kind: 'send' });
    actor?.send({ type: 'RUN_STARTED', runID: 'r-old' });
    expect(useStore.getState().queueInput('draft to restore')).toBe(true);

    const staged = useStore.getState().takeQueued();

    expect(staged).toMatchObject({
      text: 'draft to restore',
      attachments: [],
      interrupt: false,
    });
    expect(useStore.getState().conversations['s-1']?.queued).toBeUndefined();
  });

  it('forkTurn creates the fork and switches to its hydrated history', async () => {
    apiMock.forkTurn.mockResolvedValue('s-fork');
    apiMock.resumeSession.mockResolvedValue({
      conversation_id: 's-fork',
      mode: 'workspace',
      think: 'medium',
      model: '',
    });
    apiMock.sessionTurns.mockResolvedValue([
      historyTurn(1, 'fork prompt', 'fork answer'),
    ]);

    await useStore.getState().forkTurn('r-fork');

    expect(apiMock.forkTurn).toHaveBeenCalledWith('s-1', 'r-fork');
    expect(apiMock.resumeSession).toHaveBeenCalledWith('s-fork');
    expect(stateRoot.focusSnapshot.value).toBe('active');
    expect(stateRoot.focusSnapshot.context.sessionID).toBe('s-fork');
    const conv = useStore.getState().conversations['s-fork'];
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
    expect(texts).toEqual(['fork prompt', 'fork answer']);
  });
});
