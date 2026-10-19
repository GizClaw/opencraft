// conv/turn.ts with conv/model.ts and conv/writer.ts: mid-turn steer -
// the optimistic interjection row, the delivery stamps a round boundary
// sets, and the undelivered tail it leaves (resend, drop, cap).

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { stateRoot } from '../../state/app';
import { useStore } from '../store';
import { apiMock } from '../../test/apiMock';
import {
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

describe('mid-turn steer', () => {
  function runningConversation(runID = 'r-run') {
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'SUBMIT', kind: 'send' });
    actor?.send({ type: 'RUN_STARTED', runID });
    bindRun(runID);
    return actor;
  }

  /** steerRows lists the interjection rows of a conversation. */
  function steerRows(conversationID = 's-1') {
    return (
      useStore.getState().conversations[conversationID]?.messages ?? []
    ).filter((m) => m.steer);
  }

  function endTurn(runID: string, steerPending?: number | null) {
    useStore.getState().handleEvent({
      type: 'turn_end',
      data: {
        run_id: runID,
        conversation_id: 's-1',
        status: 'completed',
        ...(steerPending === undefined ? {} : { steer_pending: steerPending }),
      },
    });
  }

  /** reportPending is the live count a round boundary sends mid-turn. */
  function reportPending(runID: string, pending: number) {
    useStore.getState().handleEvent({
      type: 'steer_pending',
      data: { run_id: runID, conversation_id: 's-1', steer_pending: pending },
    });
  }

  it('draws the optimistic row as a queued interjection', async () => {
    runningConversation();

    const ok = await useStore.getState().steer('mid-turn note');

    expect(ok).toBe(true);
    expect(apiMock.steerTurn).toHaveBeenCalledWith('r-run', 'mid-turn note');
    expect(apiMock.startTurn).not.toHaveBeenCalled();
    const conv = useStore.getState().conversations['s-1'];
    expect(conv.messages.at(-1)).toMatchObject({
      role: 'user',
      text: 'mid-turn note',
      // The row never looked like a turn opener: it is stamped before the
      // RPC, so the transcript shows what it is from the first paint.
      steer: 'pending',
    });
    expect(conv.steerSent).toEqual([
      {
        runID: 'r-run',
        messageID: conv.messages.at(-1)?.id,
        text: 'mid-turn note',
      },
    ]);
  });

  it('stamps delivered rows through turn_end', async () => {
    runningConversation();
    await useStore.getState().steer('kept');

    endTurn('r-run', 0);
    await new Promise((resolve) => setTimeout(resolve, 0));

    const conv = useStore.getState().conversations['s-1'];
    // The row stays in place: the archive has the text too, so the
    // settled state is the only thing that changes.
    expect(conv.messages.at(-1)).toMatchObject({
      text: 'kept',
      steer: 'delivered',
    });
    expect(conv.steerSent ?? []).toEqual([]);
  });

  it('flips a taken interjection while the turn is still running', async () => {
    runningConversation();
    await useStore.getState().steer('first');
    await useStore.getState().steer('second');

    // A boundary drained one message: rows are FIFO, so the oldest is the
    // one it carried, and the row still queued keeps waiting for a
    // boundary of its own instead of being guessed at.
    reportPending('r-run', 1);
    expect(steerRows().map((m) => [m.text, m.steer])).toEqual([
      ['first', 'delivered'],
      ['second', 'pending'],
    ]);
    // The settled row leaves the pending list, which is exactly what
    // turn_end reconciles — the ending must not reclassify it.
    expect(
      (useStore.getState().conversations['s-1'].steerSent ?? []).map(
        (s) => s.text,
      ),
    ).toEqual(['second']);

    // The turn then ends without reaching another boundary: what the
    // boundary took stays delivered, what waited is undelivered.
    endTurn('r-run', 1);
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(steerRows().map((m) => [m.text, m.steer])).toEqual([
      ['first', 'delivered'],
      ['second', 'undelivered'],
    ]);
  });

  it('never un-settles a delivered row when a count moves back up', async () => {
    runningConversation();
    await useStore.getState().steer('first');
    reportPending('r-run', 0);
    await useStore.getState().steer('second');

    // The count now covers only the row that is really still queued: a
    // report can never reach back and un-deliver what a boundary carried.
    reportPending('r-run', 1);
    expect(steerRows().map((m) => [m.text, m.steer])).toEqual([
      ['first', 'delivered'],
      ['second', 'pending'],
    ]);
  });

  it('keeps the draft when a rebuild replaced the row and the RPC rejects', async () => {
    runningConversation();
    apiMock.steerTurn.mockImplementation(async () => {
      // A transcript rebuild (retryTranscript, a hydrate) replaces the
      // rows while the submission is in flight: the pending row is gone
      // from the transcript, and nothing ever took its text.
      const conv = useStore.getState().conversations['s-1'];
      useStore.setState({
        conversations: {
          's-1': {
            ...conv,
            messages: conv.messages.filter((m) => !m.steer),
          },
        },
      });
      throw new Error('turn not found');
    });

    const ok = await useStore.getState().steer('rebuilt away');

    // The text reached neither the transcript nor the archive, so the
    // draft is the only place it survives; a missing row is not evidence
    // that something archived it.
    expect(ok).toBe(false);
    expect(steerRows()).toEqual([]);
    expect(useStore.getState().toasts.at(-1)).toMatchObject({
      kind: 'warning',
    });
  });

  it('keeps the draft when a retry rebuilt the conversation mid-submission', async () => {
    runningConversation();
    let rejectSteer: (err: Error) => void = () => {};
    apiMock.steerTurn.mockImplementation(
      () =>
        new Promise<void>((_resolve, reject) => {
          rejectSteer = reject;
        }),
    );
    // The user hits retry on the transcript banner while the submission
    // is in flight. retryTranscript rebuilds the shell from the archive,
    // which never saw this text (the RPC had not landed), and the
    // rebuilt shell carries no steer registrations at all.
    apiMock.sessionTurns.mockResolvedValue([]);

    const submission = useStore.getState().steer('rebuilt away');
    await new Promise((resolve) => setTimeout(resolve, 0));
    await useStore.getState().retryTranscript('s-1');
    rejectSteer(new Error('turn not found'));

    // Nothing took the text: the archive does not have it and the
    // rebuilt transcript dropped the row. The composer draft is then
    // the only copy left, so the submission has to report the failure
    // instead of claiming the conversation took it over.
    expect(await submission).toBe(false);
    expect(steerRows()).toEqual([]);
    expect(useStore.getState().toasts.at(-1)).toMatchObject({
      kind: 'warning',
    });
  });

  it('carries a submission across a retry and lets the ending settle it', async () => {
    runningConversation();
    let resolveSteer: () => void = () => {};
    apiMock.steerTurn.mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          resolveSteer = resolve;
        }),
    );
    apiMock.sessionTurns.mockResolvedValue([]);

    const submission = useStore.getState().steer('still queued');
    await new Promise((resolve) => setTimeout(resolve, 0));
    await useStore.getState().retryTranscript('s-1');
    resolveSteer();

    // The submission succeeds, so the composer clears its draft: the row
    // is then the only copy of the text, and it has to travel across the
    // rebuild with the registration that names its run.
    expect(await submission).toBe(true);
    expect(steerRows().map((m) => [m.text, m.steer])).toEqual([
      ['still queued', 'pending'],
    ]);
    expect(useStore.getState().conversations['s-1'].steerSent).toEqual([
      { runID: 'r-run', messageID: steerRows()[0].id, text: 'still queued' },
    ]);

    // The boundary never drained it: the ending says one message is still
    // waiting, and the carried row is the one it names.
    endTurn('r-run', 1);
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(steerRows().map((m) => [m.text, m.steer])).toEqual([
      ['still queued', 'undelivered'],
    ]);
  });

  it('keeps a pending row registered when a hydrate rebuilds the shell', async () => {
    // A pending row outlives the switch back into the conversation: it
    // is waiting for a boundary of a run that is still live. The
    // hydrate a fresh actor triggers rebuilds the shell around it, and
    // the registration naming its run is the only thing that can settle
    // it later.
    stateRoot.registry.ensure('s-2', {
      workspaceGeneration: stateRoot.generation(),
      workspace: '/tmp/w',
    });
    stateRoot.registry.get('s-2')?.send({ type: 'RUN_STARTED', runID: 'r-2' });
    useStore.setState({
      conversations: {
        ...useStore.getState().conversations,
        's-2': {
          ...useStore.getState().conversations['s-1'],
          messages: [
            {
              id: 'm-steer',
              role: 'user',
              text: 'kept',
              items: [],
              attachments: [],
              steer: 'pending',
            },
          ],
          steerSent: [{ runID: 'r-2', messageID: 'm-steer', text: 'kept' }],
        },
      },
    });
    apiMock.sessionTurns.mockResolvedValue([
      historyTurn(1, 'earlier question', 'earlier answer'),
    ]);

    await useStore.getState().resume('s-2');

    // The row travels with the live run, and it is still waiting for
    // that run's boundary.
    const hydrated = useStore.getState().conversations['s-2'];
    expect(hydrated.messages.at(-1)).toMatchObject({
      id: 'm-steer',
      steer: 'pending',
    });
    expect(hydrated.steerSent).toEqual([
      { runID: 'r-2', messageID: 'm-steer', text: 'kept' },
    ]);

    // Which is what lets the ending classify it: without the
    // registration the row would wait for a boundary that already came.
    useStore.getState().handleEvent({
      type: 'turn_end',
      data: {
        run_id: 'r-2',
        conversation_id: 's-2',
        status: 'completed',
        steer_pending: 0,
      },
    });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(
      useStore.getState().conversations['s-2'].messages.at(-1)?.steer,
    ).toBe('delivered');
  });

  it('marks an undelivered steer in place at turn_end', async () => {
    runningConversation();
    await useStore.getState().steer('lost note');
    const rowID = useStore.getState().conversations['s-1'].messages.at(-1)?.id;

    endTurn('r-run', 1);
    await new Promise((resolve) => setTimeout(resolve, 0));

    const conv = useStore.getState().conversations['s-1'];
    // Still a transcript row — not a card that lost its place — so the
    // text the archive never saw stays exactly where it was typed.
    expect(conv.messages.at(-1)).toMatchObject({
      id: rowID,
      text: 'lost note',
      steer: 'undelivered',
    });
    expect(conv.steerSent ?? []).toEqual([]);
  });

  it('classifies only the newest entries as undelivered', async () => {
    runningConversation();
    await useStore.getState().steer('first');
    await useStore.getState().steer('second');

    endTurn('r-run', 1);
    await new Promise((resolve) => setTimeout(resolve, 0));

    const rows = steerRows();
    expect(rows.map((m) => [m.text, m.steer])).toEqual([
      ['first', 'delivered'],
      ['second', 'undelivered'],
    ]);
  });

  it('keeps the undelivered row when turn_end wins the race and the RPC rejects', async () => {
    runningConversation();
    apiMock.steerTurn.mockImplementation(async () => {
      endTurn('r-run', 1);
      throw new Error('turn not found');
    });

    const ok = await useStore.getState().steer('raced note');

    // The turn settled mid-submission: the row is already marked
    // undelivered, so a late rejection must not pull it back out or
    // start a fresh turn (the text would then be sent twice).
    expect(ok).toBe(true);
    expect(apiMock.startTurn).not.toHaveBeenCalled();
    expect(steerRows().map((m) => [m.text, m.steer])).toEqual([
      ['raced note', 'undelivered'],
    ]);
    expect(useStore.getState().toasts).toHaveLength(0);
  });

  it('does not duplicate the steer row when turn_end and the RPC both land', async () => {
    runningConversation();
    apiMock.steerTurn.mockImplementation(async () => {
      endTurn('r-run', 1);
    });

    expect(await useStore.getState().steer('raced note')).toBe(true);

    // Exactly one copy of the text, marked undelivered, with the
    // registration for the run cleared.
    const conv = useStore.getState().conversations['s-1'];
    expect(conv.messages.filter((m) => m.text === 'raced note')).toHaveLength(
      1,
    );
    expect(conv.messages.find((m) => m.text === 'raced note')?.steer).toBe(
      'undelivered',
    );
    expect(conv.steerSent ?? []).toEqual([]);
    expect(useStore.getState().toasts).toHaveLength(0);
  });

  it('caps the undelivered rows at the newest twenty', async () => {
    runningConversation();
    for (let i = 0; i < 25; i += 1) {
      await useStore.getState().steer(`note ${i}`);
    }

    endTurn('r-run', 25);
    await new Promise((resolve) => setTimeout(resolve, 0));

    const rows = steerRows();
    expect(rows).toHaveLength(20);
    expect(rows[0].text).toBe('note 5');
    expect(rows[19].text).toBe('note 24');
    expect(rows.every((m) => m.steer === 'undelivered')).toBe(true);
  });

  it('treats a pending count above the local rows as bounded, not phantom', async () => {
    runningConversation();
    await useStore.getState().steer('only one');

    endTurn('r-run', 5);
    await new Promise((resolve) => setTimeout(resolve, 0));

    // A count larger than what this client registered (another window, a
    // lost row) still only settles what exists.
    expect(steerRows().map((m) => [m.text, m.steer])).toEqual([
      ['only one', 'undelivered'],
    ]);
  });

  it('settles every steered row when the backend cannot read the count', async () => {
    runningConversation();
    await useStore.getState().steer('first');
    await useStore.getState().steer('second');

    endTurn('r-run', null);
    await new Promise((resolve) => setTimeout(resolve, 0));

    // Unknown is not zero: the rows are the only copy of that text, so
    // all of them settle as undelivered instead of being reconciled away.
    expect(steerRows().map((m) => [m.text, m.steer])).toEqual([
      ['first', 'undelivered'],
      ['second', 'undelivered'],
    ]);
  });

  it('settles every steered row when turn_end omits the count', async () => {
    runningConversation();
    await useStore.getState().steer('from an older producer');

    // A producer that does not know the field must fail closed: the
    // absent value is not a zero the transcript can be reconciled away
    // against.
    endTurn('r-run');
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(steerRows().map((m) => [m.text, m.steer])).toEqual([
      ['from an older producer', 'undelivered'],
    ]);
  });

  it('settles a row whose run never reported back at all', async () => {
    runningConversation('r-old');
    await useStore.getState().steer('orphan note');
    expect(steerRows().map((m) => m.steer)).toEqual(['pending']);

    // The superseded run's terminal event is lost: its row would claim a
    // delivery is still coming forever. The next turn_end in that
    // conversation settles it.
    const actor = runningConversation('r-new');
    actor?.send({ type: 'RUN_STARTED', runID: 'r-new' });
    endTurn('r-new', 0);
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(steerRows().map((m) => [m.text, m.steer])).toEqual([
      ['orphan note', 'undelivered'],
    ]);
    expect(useStore.getState().conversations['s-1'].steerSent ?? []).toEqual(
      [],
    );
  });

  it('leaves a row of a live run alone when another run ends', async () => {
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'SUBMIT', kind: 'send' });
    actor?.send({ type: 'RUN_STARTED', runID: 'r-new' });
    // r-old is still registered as live: its terminal event has not
    // landed even though the conversation already moved on to r-new.
    bindRun('r-old');
    bindRun('r-new');
    await useStore.getState().steer('waiting note');

    // The row belongs to r-new, which is still waiting for its own
    // boundary, so r-old's turn_end must not settle it.
    endTurn('r-old', 0);
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(steerRows().map((m) => [m.text, m.steer])).toEqual([
      ['waiting note', 'pending'],
    ]);
  });

  it('resends an undelivered row as a new turn and drops it', async () => {
    runningConversation();
    await useStore.getState().steer('lost note');
    endTurn('r-run', 1);
    await new Promise((resolve) => setTimeout(resolve, 0));
    const row = steerRows()[0];
    expect(row).toBeDefined();
    apiMock.startTurn.mockResolvedValue({
      run_id: 'r-new',
      context_id: 's-1',
    });

    await useStore.getState().resendSteer(row.id);

    const conv = useStore.getState().conversations['s-1'];
    expect(steerRows()).toEqual([]);
    expect(conv.messages.at(-1)).toMatchObject({
      role: 'user',
      text: 'lost note',
    });
    expect(conv.messages.at(-1)?.steer).toBeUndefined();
    expect(conversationForRun('r-new')).toBe('s-1');
  });

  it('drops an undelivered row without sending', async () => {
    runningConversation();
    await useStore.getState().steer('lost note');
    endTurn('r-run', 1);
    await new Promise((resolve) => setTimeout(resolve, 0));
    const row = steerRows()[0];

    useStore.getState().dismissSteer(row.id);

    expect(steerRows()).toEqual([]);
    expect(apiMock.startTurn).not.toHaveBeenCalled();
  });

  it('never resends a delivered row', async () => {
    runningConversation();
    await useStore.getState().steer('delivered note');
    endTurn('r-run', 0);
    await new Promise((resolve) => setTimeout(resolve, 0));
    const row = steerRows()[0];

    await useStore.getState().resendSteer(row.id);
    useStore.getState().dismissSteer(row.id);

    // A delivered interjection is conversation history: it is not
    // the user's to drop from the transcript (the archive has it).
    expect(steerRows().map((m) => m.steer)).toEqual(['delivered']);
    expect(apiMock.startTurn).not.toHaveBeenCalled();
  });

  it('keeps the text out of the transcript when a live turn rejects', async () => {
    runningConversation();
    apiMock.steerTurn.mockRejectedValue(new Error('steer queue full'));

    const ok = await useStore.getState().steer('rejected');

    expect(ok).toBe(false);
    const conv = useStore.getState().conversations['s-1'];
    expect(conv.messages.some((m) => m.text === 'rejected')).toBe(false);
    expect(conv.steerSent ?? []).toEqual([]);
    expect(useStore.getState().toasts).toHaveLength(1);
    expect(useStore.getState().toasts[0].kind).toBe('warning');
  });

  it('falls back to a normal send when the turn ended before the rejection', async () => {
    const actor = runningConversation();
    apiMock.steerTurn.mockImplementation(async () => {
      // The run's terminal event never arrived; the conversation reached
      // its idle state another way (a cancel that settled locally).
      actor?.send({ type: 'TURN_ENDED', runID: 'r-run', status: 'completed' });
      stateRoot.registry.releaseRun('r-run');
      throw new Error('turn not found');
    });
    apiMock.startTurn.mockResolvedValue({
      run_id: 'r-fallback',
      context_id: 's-1',
    });

    const ok = await useStore.getState().steer('late note');

    expect(ok).toBe(true);
    expect(apiMock.startTurn).toHaveBeenCalledTimes(1);
    const conv = useStore.getState().conversations['s-1'];
    expect(conv.messages.filter((m) => m.text === 'late note')).toHaveLength(1);
    expect(conv.messages.at(-1)).toMatchObject({
      text: 'late note',
    });
    // The row was pulled back out of the transcript and re-sent as the
    // turn the user meant, not left as an interjection nothing owns.
    expect(conv.messages.at(-1)?.steer).toBeUndefined();
    expect(conversationForRun('r-fallback')).toBe('s-1');
    expect(useStore.getState().toasts).toHaveLength(0);
  });

  it('refuses steer with attachments or without a live run', async () => {
    const busyActor = stateRoot.registry.get('s-1');
    busyActor?.send({ type: 'SUBMIT', kind: 'send' });
    expect(await useStore.getState().steer('no run yet')).toBe(false);

    runningConversation();
    const attachment = {
      id: 'a-1',
      kind: 'file' as const,
      path: '/tmp/a.txt',
      name: 'a.txt',
    };
    expect(await useStore.getState().steer('with file', [attachment])).toBe(
      false,
    );
    expect(apiMock.steerTurn).not.toHaveBeenCalled();
  });
});
