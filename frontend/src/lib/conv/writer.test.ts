// conv/writer.ts: the event sink - deltas folded into rows, the flush
// cadence, the perf counters, and what turn_end leaves behind.

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushCommitStats, setPerfMetricsEnabled } from '../perfMetrics';
import type { MessageView } from '../store';
import { stateRoot } from '../../state/app';
import { itemText, streamFlushStats, useStore } from '../store';
import { apiMock } from '../../test/apiMock';
import {
  actorValue,
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

describe('stream writes', () => {
  it('coalesces queued text deltas into one store update per flush', () => {
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    bindRun('r-1');
    const handle = useStore.getState().handleEvent;
    const storeEvent = (text: string) =>
      handle({
        type: 'stream',
        data: {
          run_id: 'r-1',
          conversation_id: 's-1',
          delta: {
            type: 'part',
            part: { type: 'text', text },
          },
        },
      });

    let storeUpdates = 0;
    const unsubscribe = useStore.subscribe(() => {
      storeUpdates += 1;
    });
    storeEvent('a');
    storeEvent('b');
    storeEvent('c');
    expect(storeUpdates).toBe(0);

    useStore.getState().flushStreams();
    unsubscribe();

    expect(storeUpdates).toBe(1);
    const conv = useStore.getState().conversations['s-1'];
    expect(conv.messages[0].items).toEqual([
      expect.objectContaining({ kind: 'text', text: 'abc' }),
    ]);
  });

  it('counts stream flushes past the sample ring', () => {
    // The probe reports flushes per window by diffing this total; the ring
    // of timings is capped at 256 entries, so a count derived from the ring
    // would flat-line at 256 forever.
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    bindRun('r-1');
    const handle = useStore.getState().handleEvent;
    const before = streamFlushStats().total;
    for (let i = 0; i < 260; i += 1) {
      handle({
        type: 'stream',
        data: {
          run_id: 'r-1',
          conversation_id: 's-1',
          delta: { type: 'part', part: { type: 'text', text: 'x' } },
        },
      });
      useStore.getState().flushStreams();
    }
    expect(streamFlushStats().total - before).toBe(260);
  });

  it('measures a stream flush to the frame that follows it', () => {
    // The probe's flush_commit_* series: the store hands the flush's start
    // to perfMetrics, which stops the clock on the next frame.
    let clock = 10_000;
    const nowSpy = vi.spyOn(performance, 'now').mockImplementation(() => clock);
    const queued: Array<() => void> = [];
    const rafSpy = vi
      .spyOn(window, 'requestAnimationFrame')
      .mockImplementation((cb: FrameRequestCallback) => {
        queued.push(() => cb(clock));
        return queued.length;
      });
    const cancelSpy = vi
      .spyOn(window, 'cancelAnimationFrame')
      .mockImplementation(() => {});
    setPerfMetricsEnabled(true);
    try {
      stateRoot.registry
        .get('s-1')
        ?.send({ type: 'RUN_STARTED', runID: 'r-1' });
      bindRun('r-1');
      useStore.getState().handleEvent({
        type: 'stream',
        data: {
          run_id: 'r-1',
          conversation_id: 's-1',
          delta: { type: 'part', part: { type: 'text', text: 'x' } },
        },
      });
      useStore.getState().flushStreams();
      // The commit lands on the next frame: the clock moves to the frame's
      // timestamp before it runs.
      clock += 21;
      while (queued.length > 0) queued.shift()!();
      expect(flushCommitStats().max).toBe(21);
    } finally {
      setPerfMetricsEnabled(false);
      nowSpy.mockRestore();
      rafSpy.mockRestore();
      cancelSpy.mockRestore();
    }
  });

  // The two cadences (see stream.ts): a queue that holds nothing but
  // reasoning waits for its own, calmer beat, and prose joining that
  // queue pulls the commit in to the text beat instead of waiting it out.

  describe('stream cadence', () => {
    // The cadence is measured from the last commit, so the test has to
    // pin that commit rather than inherit one: `stamp()` folds one
    // delta through the queue synchronously, which is what a flush does
    // at the end of a burst. The clock is fake, so the stamp is the fake
    // time and every wait below is exact.
    const startRun = () => {
      stateRoot.registry
        .get('s-1')
        ?.send({ type: 'RUN_STARTED', runID: 'r-1' });
      bindRun('r-1');
      const handle = useStore.getState().handleEvent;
      const stream = (kind: 'text' | 'reasoning', text: string) =>
        handle({
          type: 'stream',
          data: {
            run_id: 'r-1',
            conversation_id: 's-1',
            delta: { type: 'part', part: { type: kind, text } },
          },
        });
      stream('reasoning', 'baseline');
      useStore.getState().flushStreams();
      return stream;
    };

    it('holds a reasoning-only burst past the text cadence', () => {
      vi.useFakeTimers();
      try {
        const stream = startRun();
        let storeUpdates = 0;
        const unsubscribe = useStore.subscribe(() => {
          storeUpdates += 1;
        });
        stream('reasoning', 'weighing the two layouts');
        vi.advanceTimersByTime(150);
        expect(storeUpdates).toBe(0);
        vi.advanceTimersByTime(150);
        unsubscribe();
        expect(storeUpdates).toBe(1);
      } finally {
        vi.useRealTimers();
      }
    });

    it('commits prose on the text cadence when it joins a thought', () => {
      vi.useFakeTimers();
      try {
        const stream = startRun();
        const committed = () => {
          const items = useStore
            .getState()
            .conversations['s-1']!.messages.at(-1)!.items;
          const last = items.at(-1);
          return last?.kind === 'text' ? last.text : undefined;
        };
        stream('reasoning', 'weighing');
        vi.advanceTimersByTime(40);
        stream('text', 'the answer is 42');
        // A reasoning-only queue would still be waiting here: its own
        // beat is 250ms, this queue's is 100ms from the last commit.
        vi.advanceTimersByTime(50);
        expect(committed()).toBeUndefined();
        vi.advanceTimersByTime(20);
        expect(committed()).toBe('the answer is 42');
        const items = useStore
          .getState()
          .conversations['s-1']!.messages.at(-1)!.items;
        // The two reasoning deltas are one block: the thought folds into
        // itself, the prose opens the next item.
        expect(items.map((item) => item.kind)).toEqual(['reasoning', 'text']);
        const thought = items[0];
        expect(thought.kind).toBe('reasoning');
        expect(itemText(thought as { text: string; chunks?: string[] })).toBe(
          'baselineweighing',
        );
      } finally {
        vi.useRealTimers();
      }
    });
  });

  it('stress-flushes a large burst as one transcript update', () => {
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    const handle = useStore.getState().handleEvent;
    const chunks = 5000;
    let storeUpdates = 0;
    const unsubscribe = useStore.subscribe(() => {
      storeUpdates += 1;
    });
    for (let i = 0; i < chunks; i++) {
      handle({
        type: 'stream',
        data: {
          run_id: 'r-1',
          conversation_id: 's-1',
          delta: { type: 'part', part: { type: 'text', text: 'x' } },
        },
      });
    }
    expect(storeUpdates).toBe(0);

    useStore.getState().flushStreams();
    unsubscribe();

    expect(storeUpdates).toBe(1);
    const items = useStore.getState().conversations['s-1']!.messages[0]!.items;
    const text = items
      .filter(
        (
          item,
        ): item is Extract<MessageView['items'][number], { kind: 'text' }> =>
          item.kind === 'text',
      )
      .map((item) => item.text)
      .join('');
    expect(text).toHaveLength(chunks);
  });

  it('measures only the rows a write replaced', () => {
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    const handle = useStore.getState().handleEvent;
    const stream = (text: string) =>
      handle({
        type: 'stream',
        data: {
          run_id: 'r-1',
          conversation_id: 's-1',
          delta: { type: 'part', part: { type: 'text', text } },
        },
      });

    let reads = 0;
    const probe = {
      id: 'm-probe',
      role: 'user',
      items: [],
      attachments: [],
    } as unknown as MessageView;
    Object.defineProperty(probe, 'text', {
      get: () => {
        reads += 1;
        return 'an older row';
      },
    });

    stream('the answer');
    const opened = useStore.getState().conversations['s-1'];
    expect(opened).toBeDefined();
    useStore.setState({
      conversations: {
        ...useStore.getState().conversations,
        's-1': { ...opened!, messages: [probe, ...opened!.messages] },
      },
    });
    // The commit that follows the new row measures the transcript once;
    // from then on a delta only ever replaces the row it grows.
    stream(' grows');
    useStore.getState().flushStreams();
    reads = 0;
    stream(' some more');
    useStore.getState().flushStreams();
    expect(reads).toBe(0);
  });

  // The marker says how much of the block is gone. It is carried outside
  // the chunk array for exactly this reason: a later fold must not count
  // the previous marker as dropped content, nor drop it as if it were.

  it('flushes interleaved streams in arrival order', () => {
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    const handle = useStore.getState().handleEvent;
    const streamEvent = (conversationID: string, runID: string, text: string) =>
      handle({
        type: 'stream',
        data: {
          run_id: runID,
          conversation_id: conversationID,
          delta: {
            type: 'part',
            part: { type: 'text', text },
          },
        },
      });

    const firstText = (conversationID: string) => {
      const message =
        useStore.getState().conversations[conversationID]?.messages[0];
      return (
        message?.items
          .filter(
            (
              item,
            ): item is Extract<
              MessageView['items'][number],
              { kind: 'text' }
            > => item.kind === 'text',
          )
          .map((item) => itemText(item))
          .join('') ?? ''
      );
    };
    const seen: string[] = [];
    let lastSeen = '';
    const unsubscribe = useStore.subscribe(() => {
      const pair = `${firstText('s-1')}|${firstText('s-2')}`;
      if (pair !== lastSeen) {
        seen.push(pair);
        lastSeen = pair;
      }
    });

    streamEvent('s-1', 'r-1', 'a');
    streamEvent('s-2', 'r-2', 'x');
    streamEvent('s-1', 'r-1', 'b');
    useStore.getState().flushStreams();
    unsubscribe();

    expect(seen).toEqual(['a|', 'a|x', 'ab|x']);
    expect(firstText('s-1')).toBe('ab');
    expect(firstText('s-2')).toBe('x');
  });

  it('drops late stream deltas from a previous run while a newer run runs', () => {
    const actor = stateRoot.registry.get('s-1');
    actor?.send({ type: 'RUN_STARTED', runID: 'r-old' });
    actor?.send({
      type: 'TURN_ENDED',
      runID: 'r-old',
      status: 'completed',
    });
    actor?.send({ type: 'SUBMIT', kind: 'send' });
    actor?.send({ type: 'RUN_STARTED', runID: 'r-new' });

    useStore.getState().handleEvent({
      type: 'stream',
      data: {
        run_id: 'r-old',
        conversation_id: 's-1',
        delta: { type: 'part', part: { type: 'text', text: 'stale' } },
      },
    });
    useStore.getState().flushStreams();

    const conv = useStore.getState().conversations['s-1'];
    expect(conv.messages).toEqual([]);
    expect(actorValue('s-1')?.turn).toBe('running');
  });
});

describe('turn end', () => {
  it('reconciles a finished turn from the archive', async () => {
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    bindRun('r-1');
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          messages: [
            {
              id: 'm-user',
              role: 'user',
              text: 'hi',
              items: [],
              attachments: [],
            },
            {
              id: 'm-partial',
              role: 'assistant',
              text: '',
              items: [
                {
                  kind: 'text',
                  id: 't-partial',
                  text: 'partial answer',
                },
              ],
              attachments: [],
            },
          ],
          turnArtifacts: [{ id: 'live-1', start: 0, docs: [], runID: 'r-1' }],
        },
      },
    });
    apiMock.turnByRunID.mockResolvedValue({
      ...historyTurn(1, 'hi', 'complete archived answer'),
      run_id: 'r-1',
      status: 'completed',
    });

    useStore.getState().handleEvent({
      type: 'turn_end',
      data: {
        run_id: 'r-1',
        conversation_id: 's-1',
        status: 'completed',
      },
    });

    await vi.waitFor(() => {
      const conv = useStore.getState().conversations['s-1'];
      expect(conv.turnArtifacts[0]).toMatchObject({
        runID: 'r-1',
        status: 'completed',
      });
      const texts = conv.messages
        .filter((m) => m.role === 'assistant')
        .flatMap((m) =>
          m.items
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
      expect(texts).toContain('complete archived answer');
      expect(texts).not.toContain('partial answer');
    });
    expect(actorValue('s-1')?.turn).toBe('succeeded');
  });

  it('turn_end clears busy and removes the run mapping', () => {
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    bindRun('r-1');
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
        },
      },
    });
    useStore.getState().handleEvent({
      type: 'turn_end',
      data: { run_id: 'r-1', conversation_id: 's-1', status: 'completed' },
    });

    expect(actorValue('s-1')?.turn).toBe('succeeded');
    expect(conversationForRun('r-1')).toBeUndefined();
  });

  it('turn_end stores the backend duration instead of estimating from timestamps', () => {
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          turnArtifacts: [
            {
              id: 'live-1',
              start: 0,
              runID: 'r-1',
              docs: [],
              startedAt: '2026-09-04T12:00:00Z',
            },
          ],
        },
      },
    });
    useStore.getState().handleEvent({
      type: 'turn_end',
      data: {
        run_id: 'r-1',
        conversation_id: 's-1',
        status: 'completed',
        finished_at: '2026-09-04T12:00:00Z',
        duration_ms: 123000,
      },
    });

    expect(
      useStore.getState().conversations['s-1'].turnArtifacts[0],
    ).toMatchObject({
      durationMs: 123000,
      finishedAt: '2026-09-04T12:00:00Z',
    });
  });

  it('failed turn_end stores status on the turn without a message marker', () => {
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    bindRun('r-1');
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          turnArtifacts: [{ id: 'live-1', start: 0, runID: 'r-1', docs: [] }],
          messages: [
            {
              id: 'a-1',
              role: 'assistant',
              text: '',
              items: [{ kind: 'text', id: 't-1', text: 'partial' }],
              attachments: [],
            },
          ],
        },
      },
    });
    useStore.getState().handleEvent({
      type: 'turn_end',
      data: {
        run_id: 'r-1',
        conversation_id: 's-1',
        status: 'failed',
        error: 'engine boom',
        request_id: 'req-1',
        response_id: 'resp-1',
      },
    });

    const conv = useStore.getState().conversations['s-1'];
    expect(actorValue('s-1')?.turn).toBe('failed');
    expect(conv.turnArtifacts[0]).toMatchObject({
      status: 'failed',
      error: 'engine boom',
      requestID: 'req-1',
      responseID: 'resp-1',
    });
    const text = conv.messages[0].items.find(
      (i) => i.kind === 'text',
    ) as Extract<MessageView['items'][number], { kind: 'text' }>;
    expect(text.text).toBe('partial');
  });

  it('canceled turn_end keeps the transcript clean and records the status', () => {
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    bindRun('r-1');
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          turnArtifacts: [{ id: 'live-1', start: 0, runID: 'r-1', docs: [] }],
          messages: [
            {
              id: 'a-1',
              role: 'assistant',
              text: '',
              items: [{ kind: 'text', id: 't-1', text: 'partial' }],
              attachments: [],
            },
          ],
        },
      },
    });
    useStore.getState().handleEvent({
      type: 'turn_end',
      data: {
        run_id: 'r-1',
        conversation_id: 's-1',
        status: 'canceled',
        error: 'context canceled',
      },
    });

    const conv = useStore.getState().conversations['s-1'];
    expect(conv.turnArtifacts[0]).toMatchObject({
      status: 'canceled',
    });
    const text = conv.messages[0].items.find(
      (i) => i.kind === 'text',
    ) as Extract<MessageView['items'][number], { kind: 'text' }>;
    expect(text.text).toBe('partial');
    expect(text.text).not.toContain('context canceled');
    expect(stateRoot.registry.get('s-1')?.getSnapshot().context).toMatchObject({
      failureStatus: 'canceled',
    });
  });

  it('a deadline turn_end records the error kind the notice splits on', () => {
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    bindRun('r-1');
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          turnArtifacts: [{ id: 'live-1', start: 0, runID: 'r-1', docs: [] }],
          messages: [],
        },
      },
    });
    useStore.getState().handleEvent({
      type: 'turn_end',
      data: {
        run_id: 'r-1',
        conversation_id: 's-1',
        status: 'canceled',
        error: 'context deadline exceeded',
        error_kind: 'timeout',
      },
    });

    // The artifact keeps the raw reason for the notice's diagnostics,
    // the actor keeps the class for its words.
    const conv = useStore.getState().conversations['s-1'];
    expect(conv.turnArtifacts[0]).toMatchObject({
      status: 'canceled',
      error: 'context deadline exceeded',
      errorKind: 'timeout',
    });
    expect(stateRoot.registry.get('s-1')?.getSnapshot().context).toMatchObject({
      failureStatus: 'canceled',
      failureErrorKind: 'timeout',
    });
  });
});

describe('start events with no message', () => {
  it('opens the turn of a start event that carries no message', () => {
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
          turnArtifacts: [{ id: 'h-1', start: 0, seq: 1, docs: [] }],
        },
      },
    });

    handle({
      type: 'automation_run_started',
      data: { run_id: 'r-quiet', conversation_id: 's-1' },
    });
    let conv = useStore.getState().conversations['s-1'];
    expect(conv.turnArtifacts).toHaveLength(2);
    expect(conv.turnArtifacts[1]).toMatchObject({ runID: 'r-quiet', start: 2 });
    expect(conv.messages).toHaveLength(3);
    expect(conv.messages[2]).toMatchObject({ role: 'user', text: '' });

    handle({
      type: 'stream',
      data: {
        run_id: 'r-quiet',
        conversation_id: 's-1',
        delta: {
          type: 'part',
          part: { type: 'text', text: 'quiet answer' },
        },
      },
    });
    useStore.getState().flushStreams();
    conv = useStore.getState().conversations['s-1'];
    expect(conv.messages).toHaveLength(4);
    expect(conv.messages[3].items).toEqual([
      expect.objectContaining({ kind: 'text', text: 'quiet answer' }),
    ]);
    // The turn above keeps its own text: the delta stayed below the seam.
    expect(conv.messages[1].items).toEqual([]);
  });

  // The case above starts from a fresh actor (idle). In the app the actor
  // is anything but fresh: the conversation the schedule is pinned to has
  // already run a turn, so it rests on `succeeded`, and that is where the
  // routing gate used to drop the automation's whole turn — the text, the
  // file strip and the running row — until a session_updated happened to
  // bring the archive back.

  it('reaches a conversation whose last turn already succeeded', () => {
    const handle = useStore.getState().handleEvent;
    handle({
      type: 'stream',
      data: {
        run_id: 'r-1',
        conversation_id: 's-1',
        delta: { type: 'part', part: { type: 'text', text: 'first answer' } },
      },
    });
    handle({
      type: 'turn_end',
      data: {
        run_id: 'r-1',
        conversation_id: 's-1',
        status: 'completed',
      },
    });
    useStore.getState().flushStreams();
    expect(actorValue('s-1')?.turn).toBe('succeeded');

    handle({
      type: 'automation_run_started',
      data: {
        run_id: 'r-auto',
        conversation_id: 's-1',
        message: 'write the brief',
      },
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
    useStore.getState().flushStreams();

    expect(actorValue('s-1')?.turn).toBe('running');
    const live = useStore.getState().conversations['s-1'].messages;
    expect(live.some((m) => m.text === 'write the brief')).toBe(true);
    const texts = live.flatMap((m) =>
      m.items.flatMap((it) => (it.kind === 'text' ? [it.text] : [])),
    );
    expect(texts).toContain('writing the brief');
  });
});

describe('context snapshot', () => {
  // The meter's number belongs to the conversation, and turn_end is the
  // only event that carries it. Two rules matter: a turn that measured a
  // prompt replaces the snapshot, and a turn that reported nothing (a
  // failed call, a provider without usage) leaves the previous one alone
  // instead of blanking a meter the user is watching.
  it("turn_end records the turn's context snapshot", () => {
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    bindRun('r-1');
    useStore.getState().handleEvent({
      type: 'turn_end',
      data: {
        run_id: 'r-1',
        conversation_id: 's-1',
        status: 'completed',
        context: {
          measured_tokens: 41000,
          limit_tokens: 200000,
          model: 'claude-sonnet-4',
          categories: [
            { id: 'instructions', tokens: 6000 },
            { id: 'conversation', tokens: 35000 },
          ],
        },
      },
    });

    const conv = useStore.getState().conversations['s-1'];
    expect(conv.contextUsage?.measured_tokens).toBe(41000);
    expect(conv.contextUsage?.limit_tokens).toBe(200000);
    expect(conv.contextUsage?.categories).toHaveLength(2);
  });

  it('turn_end without a snapshot keeps the last measurement', () => {
    const previous = {
      measured_tokens: 1200,
      limit_tokens: 128000,
      categories: [{ id: 'conversation', tokens: 1200 }],
    };
    useStore.setState({
      conversations: {
        ...useStore.getState().conversations,
        's-1': {
          ...useStore.getState().conversations['s-1'],
          contextUsage: previous,
        },
      },
    });
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    bindRun('r-1');
    useStore.getState().handleEvent({
      type: 'turn_end',
      data: { run_id: 'r-1', conversation_id: 's-1', status: 'failed' },
    });

    expect(useStore.getState().conversations['s-1'].contextUsage).toEqual(
      previous,
    );
  });
});
