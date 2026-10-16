import { beforeEach, describe, expect, it, vi } from 'vitest';
import { COMPACT_SUMMARY_PREFIX } from './compact';
import { flushCommitStats, setPerfMetricsEnabled } from './perfMetrics';
import type { FileViewerState, MessageView } from './store';
import type { AutomationRun, AutomationTask, WorkspaceMeta } from './types';
import i18n from '../i18n';
import { stateRoot } from '../state/app';
import {
  firstMessageTitle,
  friendlyFailure,
  friendlyInterruption,
  itemText,
  pendingConversationIDs,
  streamFlushStats,
  storeBytes,
  useStore,
} from './store';

const apiMock = vi.hoisted(() => ({
  profile: vi.fn(),
  configStatus: vi.fn(),
  workspace: vi.fn(),
  sessionMode: vi.fn(),
  currentSession: vi.fn(),
  resumeSession: vi.fn(),
  getThink: vi.fn(),
  getModel: vi.fn(),
  modelOptions: vi.fn(),
  sessionDefaults: vi.fn(),
  saveSessionDefaults: vi.fn(),
  uiSettings: vi.fn(),
  listSessions: vi.fn(),
  sessionTurns: vi.fn(),
  turnsSince: vi.fn(),
  turnByRunID: vi.fn(),
  loadWorkspaces: vi.fn(),
  loadAutomations: vi.fn(),
  automations: vi.fn(),
  automationRuns: vi.fn(),
  openWorkspace: vi.fn(),
  workspaces: vi.fn(),
  setSessionMode: vi.fn(),
  setThink: vi.fn(),
  setModel: vi.fn(),
  newChat: vi.fn(),
  startTurn: vi.fn(),
  steerTurn: vi.fn(),
  forkTurn: vi.fn(),
  cancelTurn: vi.fn(),
  deleteSession: vi.fn(),
  replyPrompt: vi.fn(),
  resolveTarget: vi.fn(),
  openExternal: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));
// The frontend error bridge is the only place a missed switch leaves a
// trace: every caller turns the failure into a flash the reader sees
// once, and the click path catches it before it can reach the log.
const diagnosticsMock = vi.hoisted(() => ({
  ReportFrontendError: vi.fn(
    async (_source: string, _message: string, _stack: string) => {},
  ),
}));
vi.mock(
  '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/diagnostics',
  () => diagnosticsMock,
);

function resetStore() {
  stateRoot.resetWorkspace();
  stateRoot.sendFocus({ type: 'RESTORE_FOCUS', sessionID: 's-1' });
  stateRoot.registry.ensure('s-1', {
    workspaceGeneration: stateRoot.generation(),
    readyEmpty: true,
    workspace: '/tmp/w',
  });
  useStore.setState({
    status: {
      needed: false,
      default_model: 'm',
      default_reasoning: true,
      work_dir: '/tmp/w',
      user_dir: '/tmp/u',
      version: 'test',
      agents: 0,
    },
    configured: true,
    fatal: null,
    configOpen: false,
    configTab: 'general',
    toolsView: null,
    workspace: '/tmp/w',
    agents: [],
    sessions: [],
    automations: [],
    automationRuns: {},
    conversations: {
      's-1': {
        messages: [],
        turnArtifacts: [],
        mode: 'workspace',
        think: 'medium',
        model: '',
        pendingInteracts: [],
      },
    },
    pendingPromptConvs: {},
    composerDraft: '',
    statusText: '',
    lastUsage: null,
    modelOptions: [],
    sessionDefaults: { mode: 'workspace', think: 'medium' },
    yoloOnly: false,
    theme: 'dark',
    workspaces: [],
    toasts: [],
    sessionsLoading: false,
  });
}

function historyTurn(seq: number, userText: string, assistantText: string) {
  return {
    seq,
    at: '2026-09-03T00:00:00Z',
    messages: [
      {
        role: 'user',
        content: { parts: [{ type: 'text', text: userText }] },
      },
      {
        role: 'assistant',
        content: { parts: [{ type: 'text', text: assistantText }] },
      },
    ],
    artifacts: [],
  };
}

// assistantTexts reads every assistant row's visible text the way the
// transcript does: streamed blocks live in chunks, archived ones in
// text.
function assistantTexts(messages: MessageView[]): string[] {
  return messages.flatMap((m) =>
    m.items
      .filter(
        (it): it is Extract<MessageView['items'][number], { kind: 'text' }> =>
          it.kind === 'text',
      )
      .map((it) => itemText(it)),
  );
}

function actorValue(conversationID: string) {
  const actor = stateRoot.registry.get(conversationID);
  if (!actor) return undefined;
  return actor.getSnapshot().value as {
    lifecycle: string;
    transcript: string;
    turn: string;
  };
}

// A bound run is registry state, not store state (S1): the production
// paths bind a run where its id becomes known, so the cases below seed
// and read the index through the same door.
function bindRun(runID: string, conversationID = 's-1') {
  stateRoot.registry.bindRun(runID, conversationID);
}

function conversationForRun(runID: string) {
  return stateRoot.registry.conversationForRun(runID);
}

beforeEach(() => {
  resetStore();
  vi.clearAllMocks();
  apiMock.profile.mockResolvedValue({ yolo_only: false });
  apiMock.sessionDefaults.mockResolvedValue({
    mode: 'workspace',
    think: 'medium',
  });
  apiMock.startTurn.mockResolvedValue({
    run_id: 'r-1',
    context_id: 's-1',
  });
  apiMock.steerTurn.mockResolvedValue(undefined);
  apiMock.forkTurn.mockResolvedValue('s-fork');
  apiMock.deleteSession.mockResolvedValue({
    conversation_id: '',
    mode: '',
    think: '',
    model: '',
  });
  apiMock.newChat.mockResolvedValue({
    conversation_id: 's-new',
    mode: 'workspace',
    think: 'medium',
    model: '',
  });
  apiMock.resumeSession.mockResolvedValue({
    conversation_id: 's-2',
    mode: 'workspace',
    think: 'medium',
    model: '',
  });
  apiMock.listSessions.mockResolvedValue([]);
  apiMock.sessionTurns.mockResolvedValue([]);
  apiMock.turnsSince.mockResolvedValue([]);
  apiMock.turnByRunID.mockRejectedValue(new Error('archive turn not found'));
});

describe('store: send and stream', () => {
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
      lastEndedRunID: 'r-old',
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

  it('resuming the active session closes the tool page', async () => {
    useStore.setState({ toolsView: 'plugins' });
    await useStore.getState().resume('s-1');

    expect(useStore.getState().toolsView).toBeNull();
    expect(apiMock.resumeSession).not.toHaveBeenCalled();
  });

  it('resume refuses an id this process already deleted', async () => {
    stateRoot.registry.markDeleted('s-2');

    await useStore.getState().resume('s-2');

    // The backend never reuses a session id, so there is nothing to
    // resume: the click is answered instead of focusing a conversation
    // whose actor can never be created.
    expect(apiMock.resumeSession).not.toHaveBeenCalled();
    expect(stateRoot.focusSnapshot.context.sessionID).toBe('s-1');
    expect(useStore.getState().toasts).toHaveLength(1);
    expect(useStore.getState().toasts[0].kind).toBe('warning');
    expect(useStore.getState().toasts[0].text).toBe(
      i18n.t('chat.sessionDeleted'),
    );
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

  it('deletes the active conversation and opens the replacement minted by the backend', async () => {
    apiMock.deleteSession.mockResolvedValue({
      conversation_id: 's-next',
      mode: 'workspace',
      think: 'medium',
      model: '',
    });

    await useStore.getState().deleteSession('s-1');

    expect(apiMock.deleteSession).toHaveBeenCalledTimes(1);
    expect(apiMock.newChat).not.toHaveBeenCalled();
    expect(useStore.getState().statusText).toBe('');
    expect(useStore.getState().conversations['s-1']).toBeUndefined();
    const focus = stateRoot.focusSnapshot as {
      value: string;
      context: { sessionID: string };
    };
    expect(focus.value).toBe('active');
    expect(focus.context.sessionID).toBe('s-next');
    expect(useStore.getState().conversations['s-next']).toMatchObject({
      mode: 'workspace',
      think: 'medium',
      model: '',
    });
  });

  it('deletes the backend-current session while the UI stays on a draft', async () => {
    // The UI is on an unsent draft, but the backend still tracks the
    // previous conversation as current. The backend delete mints a
    // replacement; since the draft never pointed at the deleted
    // conversation, the UI stays in the draft state.
    stateRoot.sendFocus({ type: 'OPEN_DRAFT' });
    apiMock.deleteSession.mockResolvedValue({
      conversation_id: 's-next',
      mode: 'workspace',
      think: 'medium',
      model: '',
    });

    await useStore.getState().deleteSession('s-1');

    expect(apiMock.deleteSession).toHaveBeenCalledTimes(1);
    expect(apiMock.newChat).not.toHaveBeenCalled();
    expect(stateRoot.focusSnapshot.value).toBe('no-session');
    expect(useStore.getState().statusText).toBe('');
    expect(useStore.getState().conversations['s-1']).toBeUndefined();
  });

  it('a delete tombstones the id it deleted, and resume answers for it', async () => {
    apiMock.deleteSession.mockResolvedValue({
      conversation_id: 's-next',
      mode: 'workspace',
      think: 'medium',
      model: '',
    });

    await useStore.getState().deleteSession('s-1');

    // The tombstone comes from the delete itself, not from the test: a
    // producer that stopped marking the id would leave resume believing
    // the conversation is still openable.
    expect(stateRoot.registry.isDeleted('s-1')).toBe(true);
    expect(
      stateRoot.registry.ensure('s-1', {
        workspaceGeneration: stateRoot.generation(),
      }),
    ).toBeUndefined();

    await useStore.getState().resume('s-1');

    expect(apiMock.resumeSession).not.toHaveBeenCalled();
    expect(stateRoot.focusSnapshot.context.sessionID).toBe('s-next');
    expect(useStore.getState().toasts.at(-1)?.text).toBe(
      i18n.t('chat.sessionDeleted'),
    );
  });

  it('takes the deleted conversation\u2019s run mapping and viewer with it', async () => {
    apiMock.deleteSession.mockResolvedValue({
      conversation_id: 's-next',
      mode: 'workspace',
      think: 'medium',
      model: '',
    });
    bindRun('r-1');
    bindRun('r-2', 's-2');
    useStore.setState({
      viewers: {
        's-1': { filesOpen: true } as FileViewerState,
        's-2': { filesOpen: true } as FileViewerState,
      },
    });

    await useStore.getState().deleteSession('s-1');

    // The tombstone takes the deleted conversation's run mappings with
    // it, and only those: the other conversation's run keeps its owner.
    expect(conversationForRun('r-1')).toBeUndefined();
    expect(conversationForRun('r-2')).toBe('s-2');
    expect(useStore.getState().viewers['s-1']).toBeUndefined();
    expect(useStore.getState().viewers['s-2']).toBeDefined();
  });

  it('routes a late terminal event of a deleted conversation nowhere', async () => {
    apiMock.deleteSession.mockResolvedValue({
      conversation_id: '',
      mode: '',
      think: '',
      model: '',
    });
    bindRun('r-1');
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });

    await useStore.getState().deleteSession('s-1');

    // The run's terminal event lands after the delete. It carries no
    // conversation id of its own — the id used to be recovered from the
    // run mapping, which is the mapping the tombstone just dropped, so
    // the event has nowhere to route and the deleted chat stays off
    // screen.
    useStore.getState().handleEvent({
      type: 'turn_end',
      data: { run_id: 'r-1', status: 'completed' },
    });

    const state = useStore.getState();
    expect(state.conversations['s-1']).toBeUndefined();
    expect(conversationForRun('r-1')).toBeUndefined();
  });

  it('a listing that raced the delete cannot put the row back on the sidebar', async () => {
    apiMock.deleteSession.mockResolvedValue({
      conversation_id: 's-next',
      mode: 'workspace',
      think: 'medium',
      model: '',
    });
    // The backend refuses every writer of a deleted id, so a listing
    // that still holds one was computed before the delete settled. It
    // is filtered on the way in, by the tombstone resume answers with.
    apiMock.listSessions.mockResolvedValue([
      {
        id: 's-1',
        title: 'deleted',
        created_at: '2026-09-25T08:00:00Z',
        updated_at: '2026-09-25T08:00:00Z',
        turns: 1,
        messages: 2,
        total_tokens: 0,
      },
      {
        id: 's-next',
        title: 'replacement',
        created_at: '2026-09-25T08:01:00Z',
        updated_at: '2026-09-25T08:01:00Z',
        turns: 0,
        messages: 0,
        total_tokens: 0,
      },
    ]);

    await useStore.getState().deleteSession('s-1');

    expect(useStore.getState().sessions.map((s) => s.id)).toEqual(['s-next']);
  });

  it('deleting an id twice asks the backend twice and keeps it refused', async () => {
    // The second call is the same request: the backend purges whatever
    // came back under the retired id and reports success either way,
    // and the frontend keeps answering for the id.
    await useStore.getState().deleteSession('s-1');
    await useStore.getState().deleteSession('s-1');

    expect(apiMock.deleteSession).toHaveBeenCalledTimes(2);
    expect(useStore.getState().statusText).toBe('');
    expect(stateRoot.registry.isDeleted('s-1')).toBe(true);
  });

  it('deleting a session prunes its file viewer state', async () => {
    apiMock.deleteSession.mockResolvedValue({
      conversation_id: 's-next',
      mode: 'workspace',
      think: 'medium',
      model: '',
    });
    useStore.setState({
      viewers: {
        's-1': {
          filesOpen: true,
          panelMode: 'files',
          fileTabs: [
            {
              key: '/tmp/w/a.go',
              path: '/tmp/w/a.go',
              rel: 'a.go',
              root: 'workspace',
              name: 'a.go',
              media_type: 'text/plain',
            },
          ],
          fileActive: '/tmp/w/a.go',
          fileTreeDir: '.',
        },
      },
    });

    await useStore.getState().deleteSession('s-1');

    expect(useStore.getState().viewers['s-1']).toBeUndefined();
  });

  it('treats Windows drive paths as local viewer targets', async () => {
    apiMock.resolveTarget.mockResolvedValue({
      path: 'C:\\Users\\me\\report.md',
      rel: '',
      root: 'workspace',
      name: 'report.md',
      is_dir: false,
      size: 10,
      media_type: 'text/markdown',
    });

    await useStore.getState().openFileTarget('C:\\Users\\me\\report.md');

    expect(apiMock.resolveTarget).toHaveBeenCalledWith(
      'C:\\Users\\me\\report.md',
      '',
    );
    expect(apiMock.openExternal).not.toHaveBeenCalled();
    const viewer = useStore.getState().viewers['s-1'];
    expect(viewer?.fileActive).toBe('C:\\Users\\me\\report.md');
  });

  it('routes url-like targets to the system browser instead of the viewer', async () => {
    await useStore.getState().openFileTarget('https://example.com');

    expect(apiMock.openExternal).toHaveBeenCalledWith('https://example.com');
    expect(apiMock.resolveTarget).not.toHaveBeenCalled();
  });

  it('replaces only the active placeholder tab when a file opens', () => {
    useStore.setState({
      viewers: {
        's-1': {
          filesOpen: true,
          panelMode: 'files',
          fileTabs: [
            {
              key: 'blank-1',
              path: '',
              rel: '',
              root: 'workspace',
              name: 'New file',
              media_type: '',
            },
            {
              key: 'blank-2',
              path: '',
              rel: '',
              root: 'workspace',
              name: 'New file',
              media_type: '',
            },
          ],
          fileActive: 'blank-1',
          fileTreeDir: '.',
        },
      },
    });

    useStore.getState().openResolvedTarget({
      path: '/tmp/w/a.go',
      rel: 'a.go',
      root: 'workspace',
      name: 'a.go',
      is_dir: false,
      size: 10,
      media_type: 'text/plain',
    });

    const viewer = useStore.getState().viewers['s-1'];
    expect(viewer?.fileTabs.map((t) => t.key)).toEqual([
      'blank-2',
      '/tmp/w/a.go',
    ]);
    expect(viewer?.fileActive).toBe('/tmp/w/a.go');
  });

  it('new chat switches to an empty conversation without disturbing active runs', async () => {
    bindRun('r-old', 's-old');
    useStore.setState({
      conversations: {
        's-old': {
          ...useStore.getState().conversations['s-1'],
          messages: [
            {
              id: 'm-old',
              role: 'user',
              text: 'old history',
              items: [],
              attachments: [],
            },
          ],
        },
      },
    });

    await useStore.getState().newChat();

    const state = useStore.getState();
    expect(stateRoot.focusSnapshot.value).toBe('active');
    expect(stateRoot.focusSnapshot.context.sessionID).toBe('s-new');
    expect(conversationForRun('r-old')).toBe('s-old');
    expect(state.conversations['s-old']).toBeDefined();
    expect(state.conversations['s-new']).toBeDefined();
    expect(state.conversations['s-new'].messages).toEqual([]);
  });

  it('evicts an idle background conversation after switching', async () => {
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          messages: [
            {
              id: 'm-old',
              role: 'user',
              text: 'finished conversation',
              items: [],
              attachments: [],
            },
          ],
        },
      },
    });

    await useStore.getState().newChat();

    const state = useStore.getState();
    expect(state.conversations['s-1']).toBeUndefined();
    expect(state.conversations['s-new']).toBeDefined();
    expect(state.conversations['s-new'].messages).toEqual([]);
    expect(stateRoot.registry.get('s-1')).toBeUndefined();
  });

  it('serializes session switches so a stale resume cannot override new chat', async () => {
    let resolveOldResume!: () => void;
    apiMock.resumeSession.mockReturnValue(
      new Promise((resolve) => {
        resolveOldResume = () =>
          resolve({
            conversation_id: 's-old',
            mode: 'workspace',
            think: 'medium',
            model: '',
          });
      }),
    );

    const oldResume = useStore.getState().resume('s-old');
    const newChat = useStore.getState().newChat();

    // NewChat is queued behind the in-flight resume, so it must not hit
    // the backend first and let the older resume win afterwards.
    expect(apiMock.newChat).not.toHaveBeenCalled();
    resolveOldResume();
    await oldResume;
    await newChat;

    expect(stateRoot.focusSnapshot.value).toBe('active');
    expect(stateRoot.focusSnapshot.context.sessionID).toBe('s-new');
  });

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

  it('resume failure leaves focus failed with the previous session available', async () => {
    apiMock.resumeSession.mockRejectedValue(new Error('switch failed'));

    await useStore.getState().resume('s-2');

    const snapshot = stateRoot.focusSnapshot;
    expect(snapshot.value).toBe('failed');
    expect(snapshot.context.error).toBe('switch failed');
    expect(snapshot.context.from).toEqual({
      kind: 'session',
      id: 's-1',
    });
  });

  it('retryTranscript reloads history after an archive failure', async () => {
    apiMock.sessionTurns
      .mockRejectedValueOnce(new Error('archive down'))
      .mockResolvedValueOnce([
        historyTurn(1, 'history user', 'history answer'),
      ]);

    await useStore.getState().resume('s-2');
    expect(actorValue('s-2')?.transcript).toBe('failed');

    await useStore.getState().retryTranscript('s-2');

    expect(actorValue('s-2')?.transcript).toBe('ready');
    const conv = useStore.getState().conversations['s-2'];
    expect(conv.messages.map((m) => m.text || '')).toContain('history user');
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

  it('maps pending prompt owners to unique conversation ids', () => {
    expect(
      pendingConversationIDs({
        'p-1': 's-1',
        'p-2': 's-1',
        'p-3': 's-2',
      }),
    ).toEqual(['s-1', 's-2']);
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

  it('friendlyFailure maps the error kind the backend classified', () => {
    for (const kind of [
      'provider_failure',
      'invalid_provider_response',
      'unknown_model',
      'invalid_request',
      'timeout',
    ]) {
      const friendly = friendlyFailure(kind);
      expect(friendly).toBeTruthy();
      // The copy never leaks the enum or the graph wiring.
      expect(friendly ?? '').not.toContain(kind);
      expect(friendly ?? '').not.toContain('graph "');
    }
    // A failure the engine did not classify keeps the raw error, which
    // is what the notice falls back to.
    expect(friendlyFailure('')).toBeNull();
    expect(friendlyFailure(undefined)).toBeNull();
    expect(friendlyFailure('some_future_kind')).toBeTruthy();
  });

  it('friendlyInterruption maps the interrupt cause, not the text', () => {
    expect(friendlyInterruption('user_cancel')).toBeTruthy();
    expect(friendlyInterruption('host_shutdown')).toBeTruthy();
    expect(friendlyInterruption('user_input')).toBeTruthy();
    // An unknown cause still reads as an interruption; no cause at all
    // means the turn did not end as one.
    expect(friendlyInterruption('custom')).toBeTruthy();
    expect(friendlyInterruption('')).toBeNull();
    expect(friendlyInterruption(undefined)).toBeNull();
  });
});

describe('store: first-message workspace attribution', () => {
  it('openDraftChat enters the composer draft without minting', () => {
    useStore.getState().openDraftChat();
    expect(stateRoot.focusSnapshot.value).toBe('no-session');
    expect(apiMock.newChat).not.toHaveBeenCalled();
  });

  it('mints the new session only when the first message sends', async () => {
    apiMock.openWorkspace.mockResolvedValue(undefined);
    await useStore.getState().sendFirstMessage('/tmp/w', 'hello');

    expect(apiMock.openWorkspace).not.toHaveBeenCalled();
    expect(apiMock.newChat).toHaveBeenCalledTimes(1);
    expect(apiMock.startTurn).toHaveBeenCalledWith(
      's-new',
      expect.objectContaining({ role: 'user' }),
      '/tmp/w',
    );
    expect(stateRoot.focusSnapshot.value).toBe('active');
    expect(stateRoot.focusSnapshot.context.sessionID).toBe('s-new');
    const conv = useStore.getState().conversations['s-new'];
    expect(conv.messages[0]).toMatchObject({ role: 'user', text: 'hello' });
  });

  it('applies draft mode/think/model before the first run', async () => {
    apiMock.openWorkspace.mockResolvedValue(undefined);
    await useStore.getState().sendFirstMessage('/tmp/w', 'hello', [], {
      mode: 'read-only',
      think: 'high',
      model: 'provider/m-1',
    });

    expect(apiMock.setSessionMode).toHaveBeenCalledWith('read-only');
    expect(apiMock.setThink).toHaveBeenCalledWith('high');
    expect(apiMock.setModel).toHaveBeenCalledWith('provider/m-1');
    const conv = useStore.getState().conversations['s-new'];
    expect(conv.mode).toBe('read-only');
    expect(conv.think).toBe('high');
    expect(conv.model).toBe('provider/m-1');
  });

  it('uses configured session defaults for a fresh mint', async () => {
    useStore
      .getState()
      .setSessionDefaults({ mode: 'read-only', think: 'high' });
    apiMock.newChat.mockResolvedValue({
      conversation_id: 's-new',
      mode: 'read-only',
      think: 'high',
      model: '',
    });
    apiMock.openWorkspace.mockResolvedValue(undefined);

    await useStore.getState().sendFirstMessage('/tmp/w', 'hello');

    const conv = useStore.getState().conversations['s-new'];
    expect(conv.mode).toBe('read-only');
    expect(conv.think).toBe('high');
    expect(conv.model).toBe('');
    expect(apiMock.setSessionMode).not.toHaveBeenCalled();
    expect(apiMock.setThink).not.toHaveBeenCalled();
  });

  it('switches workspace before creating the session when the bookmark differs', async () => {
    apiMock.currentSession.mockResolvedValue('');
    apiMock.modelOptions.mockResolvedValue([]);
    apiMock.openWorkspace.mockImplementation(async (path: string) => {
      useStore.getState().handleEvent({
        type: 'ready',
        data: {
          needed: false,
          default_model: 'm',
          default_reasoning: true,
          work_dir: path,
          user_dir: '/tmp/u',
          version: 'test',
          agents: 0,
        },
      });
    });

    useStore.setState({ workspace: '/tmp/a' });
    stateRoot.resetWorkspace();
    await useStore.getState().sendFirstMessage('/tmp/b', 'hello b');

    expect(useStore.getState().workspace).toBe('/tmp/b');
    expect(apiMock.currentSession).not.toHaveBeenCalled();
    expect(apiMock.newChat).toHaveBeenCalledTimes(1);
    expect(apiMock.startTurn).toHaveBeenCalledWith(
      's-new',
      expect.objectContaining({ role: 'user' }),
      '/tmp/b',
    );
    expect(stateRoot.focusSnapshot.value).toBe('active');
    const conv = useStore.getState().conversations['s-new'];
    expect(conv.messages[0]).toMatchObject({ role: 'user', text: 'hello b' });
  });
});

describe('store: workspace history refresh', () => {
  const workspaceA: WorkspaceMeta = {
    id: 'w-a',
    path: '/tmp/a',
    title: 'a',
    last_opened: '2026-09-01T00:00:00Z',
  };
  const workspaceB: WorkspaceMeta = {
    id: 'w-b',
    path: '/tmp/b',
    title: 'b',
    last_opened: '2026-09-02T00:00:00Z',
  };

  it('keeps the newest snapshot when an older refresh resolves late', async () => {
    let resolveStale: (rows: WorkspaceMeta[]) => void = () => {};
    apiMock.workspaces
      .mockImplementationOnce(
        () =>
          new Promise<WorkspaceMeta[]>((resolve) => {
            resolveStale = resolve;
          }),
      )
      .mockResolvedValueOnce([workspaceB, workspaceA]);

    const stale = useStore.getState().loadWorkspaces();
    await useStore.getState().loadWorkspaces();
    resolveStale([workspaceA, workspaceB]);
    await stale;

    expect(useStore.getState().workspaces).toEqual([workspaceB, workspaceA]);
  });

  it('keeps the applied array when a refresh returns identical rows', async () => {
    apiMock.workspaces.mockResolvedValueOnce([workspaceA]);
    await useStore.getState().loadWorkspaces();
    const applied = useStore.getState().workspaces;

    // Same content, fresh array: the sidebar must not re-flatten its
    // history tree for a no-op refresh.
    apiMock.workspaces.mockResolvedValueOnce([{ ...workspaceA }]);
    await useStore.getState().loadWorkspaces();

    expect(useStore.getState().workspaces).toBe(applied);
  });

  it('refreshes history after the switch binding resolves', async () => {
    const calls: string[] = [];
    apiMock.openWorkspace.mockImplementation(async () => {
      calls.push('open');
    });
    apiMock.workspaces.mockImplementation(async () => {
      calls.push('list');
      return [];
    });

    await useStore.getState().openWorkspace('/tmp/b');

    // The binding records last_opened before it returns, so the
    // caller-side refresh is the one that observes the new order.
    expect(calls).toEqual(['open', 'list']);
    apiMock.openWorkspace.mockReset();
    apiMock.workspaces.mockReset();
  });
});

describe('store: interactions and artifacts', () => {
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

describe('store: transcript cap', () => {
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

describe('store: memory budgets', () => {
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

describe('store: cross-workspace session clicks', () => {
  const readyEvent = (workDir: string) => ({
    type: 'ready',
    data: {
      needed: false,
      default_model: 'm',
      default_reasoning: true,
      work_dir: workDir,
      user_dir: '/tmp/u',
      version: 'test',
      agents: 0,
    },
  });

  // The backend announces a switch with a ready event, and it skips the
  // announcement when the workspace asked for is the one it already
  // announced — that comparison is on canonical paths, while the window
  // compares the raw strings — so a window holding the same directory
  // under another spelling never hears back. The click must not be lost
  // to that: the binding has already applied the switch when it
  // answered, so the store asks the backend which workspace is active
  // and lands that one.
  it('lands a switch the backend never announced', async () => {
    apiMock.openWorkspace.mockResolvedValue(undefined);
    apiMock.workspace.mockResolvedValue('/tmp/b');
    apiMock.currentSession.mockResolvedValue('');
    apiMock.resumeSession.mockResolvedValue({
      conversation_id: 's-b',
      mode: 'workspace',
      think: 'medium',
      model: '',
    });
    useStore.setState({ workspace: '/tmp/a' });

    await useStore.getState().openSessionInWorkspace('s-b', '/tmp/b');

    expect(useStore.getState().workspace).toBe('/tmp/b');
    expect(apiMock.openWorkspace).toHaveBeenCalledWith('/tmp/b');
    await vi.waitFor(() =>
      expect(apiMock.resumeSession).toHaveBeenCalledWith('s-b'),
    );
    expect(stateRoot.focusSnapshot.value).toBe('active');
    expect(stateRoot.focusSnapshot.context.sessionID).toBe('s-b');
  });

  // A click whose switch is still settling when the window moves on
  // again must not resume its session: the resume bindings read the
  // workspace that is active *now*, so the click that is still on
  // screen wins and the older one is dropped.
  it('drops a click whose workspace was left while the switch settled', async () => {
    apiMock.modelOptions.mockResolvedValue([]);
    apiMock.openWorkspace.mockImplementation(async (path: string) => {
      useStore.getState().handleEvent(readyEvent(path));
    });
    // The restore that follows the switch is where a second switch can
    // land: it awaits the backend, and by the time it answers the window
    // is on another workspace.
    apiMock.currentSession.mockImplementation(async () => {
      useStore.setState({ workspace: '/tmp/c' });
      return '';
    });
    useStore.setState({ workspace: '/tmp/a' });

    await useStore.getState().openSessionInWorkspace('s-b', '/tmp/b');

    expect(useStore.getState().workspace).toBe('/tmp/c');
    expect(apiMock.resumeSession).not.toHaveBeenCalled();
  });

  // The failure is rare and timing-dependent, and every caller turns it
  // into a flash the reader sees once: the report is what keeps the next
  // occurrence diagnosable from the log.
  it('reports a switch that never settled', async () => {
    apiMock.openWorkspace.mockResolvedValue(undefined);
    apiMock.workspace.mockResolvedValue('');
    useStore.setState({ workspace: '/tmp/a' });
    vi.useFakeTimers();
    try {
      const click = useStore.getState().openSessionInWorkspace('s-b', '/tmp/b');
      const failed = expect(click).rejects.toThrow(
        'workspace switch did not complete',
      );
      await vi.advanceTimersByTimeAsync(6000);
      await failed;
    } finally {
      vi.useRealTimers();
    }

    expect(diagnosticsMock.ReportFrontendError).toHaveBeenCalledWith(
      'workspace-switch',
      expect.stringContaining('did not complete'),
      expect.any(String),
    );
    // The report names both strings, so a future failure says which
    // workspace the click asked for and which one the window kept. Only
    // the folder names travel: the telemetry copy redacts absolute paths
    // (F-32), so a full path would arrive as `<path>` and name neither.
    const [, message] = diagnosticsMock.ReportFrontendError.mock.calls[0] ?? [];
    expect(message).toContain('wanted b, active a');
  });

  // A click whose workspace a newer switch already took is dropped
  // silently: the reader is on the workspace they picked last, and
  // waiting out this click's deadline would only report a failure where
  // the window did what was asked of it.
  it('drops a click a newer switch took without waiting out its deadline', async () => {
    apiMock.openWorkspace.mockResolvedValue(undefined);
    apiMock.workspace.mockResolvedValue('/tmp/c');
    useStore.setState({ workspace: '/tmp/c' });
    vi.useFakeTimers();
    try {
      const click = useStore.getState().openSessionInWorkspace('s-b', '/tmp/b');
      const settled = expect(click).resolves.toBeUndefined();
      // Only the grace window has to pass: the backend's answer settles
      // the wait, so the 5s deadline is never reached.
      await vi.advanceTimersByTimeAsync(300);
      await settled;
    } finally {
      vi.useRealTimers();
    }

    expect(diagnosticsMock.ReportFrontendError).not.toHaveBeenCalled();
    expect(apiMock.resumeSession).not.toHaveBeenCalled();
    expect(useStore.getState().workspace).toBe('/tmp/c');
  });
});

describe('store: init session bootstrap', () => {
  const status = {
    needed: false,
    default_model: 'm',
    default_reasoning: true,
    work_dir: '/tmp/w',
    user_dir: '/tmp/u',
    version: 'test',
    agents: 0,
  };

  function stubInitApi(options: {
    currentSession?: string;
    workspace?: string;
  }) {
    apiMock.configStatus.mockResolvedValue(status);
    apiMock.workspace.mockResolvedValue(options.workspace ?? '/tmp/w');
    apiMock.sessionMode.mockResolvedValue('workspace');
    apiMock.currentSession.mockResolvedValue(options.currentSession ?? '');
    apiMock.getThink.mockResolvedValue('medium');
    apiMock.getModel.mockResolvedValue('');
    apiMock.modelOptions.mockResolvedValue([]);
    // The appearance binding is cosmetic: a null payload keeps whatever the
    // localStorage mirror painted.
    apiMock.uiSettings.mockResolvedValue(null);
  }

  it('starts a fresh conversation when no current session exists', async () => {
    stateRoot.resetWorkspace();
    stubInitApi({ currentSession: '', workspace: '/tmp/w' });

    await useStore.getState().init();

    expect(apiMock.newChat).toHaveBeenCalledTimes(1);
    expect(stateRoot.focusSnapshot.value).toBe('active');
    expect(stateRoot.focusSnapshot.context.sessionID).toBe('s-new');
    expect(useStore.getState().conversations['s-new']).toBeDefined();
    expect(useStore.getState().sessionDefaults).toEqual({
      mode: 'workspace',
      think: 'medium',
    });
  });

  it('does not mint twice when init runs concurrently', async () => {
    stateRoot.resetWorkspace();
    stubInitApi({ currentSession: '', workspace: '/tmp/w' });

    await Promise.all([useStore.getState().init(), useStore.getState().init()]);

    expect(apiMock.newChat).toHaveBeenCalledTimes(1);
    expect(stateRoot.focusSnapshot.value).toBe('active');
    expect(stateRoot.focusSnapshot.context.sessionID).toBe('s-new');
  });

  it('restores an existing session instead of minting another', async () => {
    stateRoot.resetWorkspace();
    stubInitApi({ currentSession: 's-1', workspace: '/tmp/w' });

    await useStore.getState().init();

    expect(apiMock.newChat).not.toHaveBeenCalled();
    expect(stateRoot.focusSnapshot.value).toBe('active');
    expect(stateRoot.focusSnapshot.context.sessionID).toBe('s-1');
  });

  it('stays on no-session while no workspace is open', async () => {
    stateRoot.resetWorkspace();
    stubInitApi({ currentSession: '', workspace: '' });

    await useStore.getState().init();

    expect(apiMock.newChat).not.toHaveBeenCalled();
    expect(stateRoot.focusSnapshot.value).toBe('no-session');
  });

  // The theme mirror is a localStorage preference, so it is what the first
  // frame paints from — before the desktop document reconciles the rest of
  // the appearance.
  // init seeds the current conversation, and its round trips take a
  // while: by the time it resolves the store can hold conversations of
  // its own (a retry after a failed init runs against a shell that has
  // been in use). A fresh table would drop them.
  it('seeds the current conversation without dropping the others', async () => {
    stateRoot.resetWorkspace();
    stubInitApi({ currentSession: 's-1', workspace: '/tmp/w' });
    const seeded = useStore.getState().conversations['s-1'];
    useStore.setState({ conversations: { 's-9': { ...seeded } } });

    await useStore.getState().init();

    expect(useStore.getState().conversations['s-9']).toBeDefined();
    expect(useStore.getState().conversations['s-1']?.think).toBe('medium');
  });

  it('paints the theme the mirror carries', async () => {
    stateRoot.resetWorkspace();
    stubInitApi({ currentSession: 's-1', workspace: '/tmp/w' });
    window.localStorage.setItem('opencraft.theme', 'light');

    await useStore.getState().init();

    expect(useStore.getState().theme).toBe('light');
    expect(useStore.getState().resolvedTheme).toBe('light');
    expect(document.documentElement.classList.contains('theme-light')).toBe(
      true,
    );
  });
});

describe('store: workspace switch session restore', () => {
  it('mints a fresh session for a new workspace and keeps a running conversation alive', async () => {
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    bindRun('r-1');
    useStore.setState({
      workspace: '/tmp/a',
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          messages: [
            {
              id: 'm-1',
              role: 'user',
              text: 'question from workspace a',
              items: [],
              attachments: [],
            },
          ],
        },
      },
    });
    apiMock.currentSession.mockResolvedValue('');

    useStore.setState({ workspace: '/tmp/b' });
    await useStore.getState().restoreWorkspaceSession('/tmp/b');

    const state = useStore.getState();
    expect(apiMock.newChat).toHaveBeenCalledTimes(1);
    expect(stateRoot.focusSnapshot.value).toBe('active');
    expect(stateRoot.focusSnapshot.context.sessionID).toBe('s-new');
    expect(state.conversations['s-1']).toBeDefined();
    expect(state.conversations['s-1'].messages[0].text).toBe(
      'question from workspace a',
    );
    expect(conversationForRun('r-1')).toBe('s-1');
    expect(actorValue('s-1')?.turn).toBe('running');
  });

  it('switches back to the workspace saved session and hydrates it', async () => {
    apiMock.currentSession.mockResolvedValue('s-a');
    apiMock.resumeSession.mockResolvedValue({
      conversation_id: 's-a',
      mode: 'workspace',
      think: 'medium',
      model: '',
    });
    apiMock.sessionTurns.mockResolvedValue([
      historyTurn(1, 'previous question', 'previous answer'),
    ]);

    useStore.setState({ workspace: '/tmp/a' });
    await useStore.getState().restoreWorkspaceSession('/tmp/a');

    const state = useStore.getState();
    expect(apiMock.newChat).not.toHaveBeenCalled();
    expect(apiMock.resumeSession).toHaveBeenCalledWith('s-a');
    expect(stateRoot.focusSnapshot.value).toBe('active');
    expect(stateRoot.focusSnapshot.context.sessionID).toBe('s-a');
    const conv = state.conversations['s-a'];
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
    expect(texts).toContain('previous question');
    expect(texts).toContain('previous answer');
  });

  it('ready after a workspace change restores the saved session', async () => {
    apiMock.currentSession.mockResolvedValue('s-a');
    apiMock.resumeSession.mockResolvedValue({
      conversation_id: 's-a',
      mode: 'workspace',
      think: 'medium',
      model: '',
    });
    apiMock.sessionTurns.mockResolvedValue([]);

    useStore.setState({ workspace: '/tmp/b' });
    useStore.getState().handleEvent({
      type: 'ready',
      data: {
        needed: false,
        default_model: 'm',
        default_reasoning: true,
        work_dir: '/tmp/a',
        user_dir: '/tmp/u',
        version: 'test',
        agents: 0,
      },
    });

    await vi.waitFor(() => {
      expect(useStore.getState().workspace).toBe('/tmp/a');
      expect(stateRoot.focusSnapshot.value).toBe('active');
      expect(stateRoot.focusSnapshot.context.sessionID).toBe('s-a');
    });
  });

  it('ready refreshes the model list the composer offers', async () => {
    // Saving inference settings rewrites the deployment document; the
    // backend signals that with the same ready event a runtime reload
    // uses, so the composer's model list follows the saved instances.
    apiMock.modelOptions.mockResolvedValue([
      {
        id: 'openai-1/gpt-6-astra',
        label: 'primary · gpt-6-astra',
        reasoning: true,
      },
    ]);
    useStore.setState({ workspace: '/tmp/a', modelOptions: [] });

    useStore.getState().handleEvent({
      type: 'ready',
      data: {
        needed: false,
        default_model: 'openai-1/gpt-6-astra',
        default_reasoning: true,
        work_dir: '/tmp/a',
        user_dir: '/tmp/u',
        version: 'test',
        agents: 0,
      },
    });

    await vi.waitFor(() => {
      expect(useStore.getState().modelOptions).toEqual([
        {
          id: 'openai-1/gpt-6-astra',
          label: 'primary · gpt-6-astra',
          reasoning: true,
        },
      ]);
    });
  });

  it('keeps a running conversation visible after a workspace round trip', async () => {
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    bindRun('r-1');
    useStore.setState({
      workspace: '/tmp/a',
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          messages: [
            {
              id: 'm-1',
              role: 'user',
              text: 'question before switch',
              items: [],
              attachments: [],
            },
          ],
        },
      },
    });
    apiMock.currentSession
      .mockResolvedValueOnce('')
      .mockResolvedValueOnce('s-1');
    apiMock.resumeSession.mockResolvedValue({
      conversation_id: 's-1',
      mode: 'workspace',
      think: 'medium',
      model: '',
    });
    apiMock.sessionTurns.mockResolvedValue([]);

    useStore.getState().handleEvent({
      type: 'ready',
      data: {
        needed: false,
        default_model: 'm',
        default_reasoning: true,
        work_dir: '/tmp/b',
        user_dir: '/tmp/u',
        version: 'test',
        agents: 0,
      },
    });
    await vi.waitFor(() => {
      expect(stateRoot.focusSnapshot.context.sessionID).toBe('s-new');
    });

    useStore.getState().handleEvent({
      type: 'stream',
      data: {
        run_id: 'r-1',
        conversation_id: 's-1',
        delta: {
          type: 'part',
          part: { type: 'text', text: 'answer after switch' },
        },
      },
    });
    useStore.getState().flushStreams();

    useStore.getState().handleEvent({
      type: 'ready',
      data: {
        needed: false,
        default_model: 'm',
        default_reasoning: true,
        work_dir: '/tmp/a',
        user_dir: '/tmp/u',
        version: 'test',
        agents: 0,
      },
    });
    await vi.waitFor(() => {
      expect(stateRoot.focusSnapshot.context.sessionID).toBe('s-1');
    });

    const conv = useStore.getState().conversations['s-1'];
    expect(conv.messages[0].text).toBe('question before switch');
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
    expect(texts).toContain('answer after switch');
  });

  // A run that outlives a workspace switch keeps streaming into the
  // conversation the window left it in, and the transcript the switch
  // hydrates has to keep that run's rows apart from the archive: they
  // belong to the live strip at the seam, not to the turn above it,
  // whose fold header and steps are its own finished record.
  it('opens the run that outlived the switch its own turn at the seam', async () => {
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
              id: 'm-q',
              role: 'user',
              text: 'the question',
              items: [],
              attachments: [],
            },
            {
              id: 'm-a',
              role: 'assistant',
              text: '',
              items: [{ kind: 'text', id: 't-a', text: 'half an answer' }],
              attachments: [],
            },
          ],
          turnArtifacts: [
            {
              id: 'live',
              start: 0,
              docs: [{ path: '/tmp/w/notes.md', bytes: 12 }],
              runID: 'r-2',
              requestedAt: '2026-10-15T00:01:00Z',
            },
          ],
        },
      },
    });
    apiMock.sessionTurns.mockResolvedValue([
      historyTurn(1, 'earlier question', 'earlier answer'),
    ]);

    await useStore.getState().resume('s-2');

    const conv = useStore.getState().conversations['s-2'];
    // The archived page, then the rows the run produced while the window
    // was elsewhere — never the other way around.
    expect(conv.messages).toHaveLength(4);
    expect(conv.messages[0].text).toBe('earlier question');
    expect(conv.messages[2].text).toBe('the question');
    expect(conv.turnArtifacts).toHaveLength(2);
    const [archived, live] = conv.turnArtifacts;
    expect(archived).toMatchObject({ seq: 1, start: 0 });
    expect(archived.finishedAt).toBeTruthy();
    // The live strip starts exactly where the archive ends and carries
    // the run the actor is tracking, which is what the run's own events
    // look their turn up by.
    expect(live).toMatchObject({
      start: 2,
      runID: 'r-2',
    });
    expect(live.seq).toBeUndefined();
    expect(live.finishedAt).toBeUndefined();
    // The strip the window already had is grown, not replaced: the files
    // it collected, the stamp it was requested at and the id its fold
    // state hangs off all survive the re-hydrate.
    expect(live.id).toBe('live');
    expect(live.requestedAt).toBe('2026-10-15T00:01:00Z');
    expect(live.docs).toEqual([{ path: '/tmp/w/notes.md', bytes: 12 }]);

    useStore.getState().handleEvent({
      type: 'artifact',
      data: {
        conversation_id: 's-2',
        run_id: 'r-2',
        path: '/tmp/w/report.md',
        bytes: 40,
      },
    });
    const strips = useStore.getState().conversations['s-2'].turnArtifacts;
    expect(strips[0].docs).toEqual([]);
    expect(strips[1].docs).toEqual([
      { path: '/tmp/w/notes.md', bytes: 12 },
      { path: '/tmp/w/report.md', bytes: 40 },
    ]);
  });

  // The merge appends the shell's rows whole, so a strip the shell
  // already had keeps its rows by shifting with them. A strip that
  // snapped back to the seam would swallow whatever the shell holds
  // above its own rows — here the row of a run a barge-in replaced.
  it('shifts a carried run with its rows instead of re-anchoring it', async () => {
    stateRoot.registry.ensure('s-2', {
      workspaceGeneration: stateRoot.generation(),
      workspace: '/tmp/w',
    });
    stateRoot.registry.get('s-2')?.send({ type: 'RUN_STARTED', runID: 'r-b' });
    useStore.setState({
      conversations: {
        ...useStore.getState().conversations,
        's-2': {
          ...useStore.getState().conversations['s-1'],
          messages: [
            {
              id: 'm-a',
              role: 'assistant',
              text: '',
              items: [
                { kind: 'text', id: 't-a', text: 'row of the replaced run' },
              ],
              attachments: [],
            },
            {
              id: 'm-q',
              role: 'user',
              text: 'follow up',
              items: [],
              attachments: [],
            },
            {
              id: 'm-b',
              role: 'assistant',
              text: '',
              items: [{ kind: 'text', id: 't-b', text: 'live answer' }],
              attachments: [],
            },
          ],
          turnArtifacts: [
            {
              id: 'older',
              start: 0,
              docs: [],
              runID: 'r-a',
              finishedAt: '2026-10-15T00:00:10Z',
            },
            { id: 'live-b', start: 1, docs: [], runID: 'r-b' },
          ],
        },
      },
    });
    apiMock.sessionTurns.mockResolvedValue([
      historyTurn(1, 'earlier question', 'earlier answer'),
    ]);

    await useStore.getState().resume('s-2');

    const conv = useStore.getState().conversations['s-2'];
    // The archived page is two rows, so the shell's rows begin at 2 and
    // the carried strip — which began at 1 inside the shell — begins at
    // 3, the first row of the run it owns.
    expect(conv.turnArtifacts[1]).toMatchObject({ id: 'live-b', start: 3 });
    expect(conv.messages[2].items[0]).toMatchObject({
      kind: 'text',
      text: 'row of the replaced run',
    });
    expect(conv.messages[3].text).toBe('follow up');
  });

  // A run can outlive the window that watches it: the app was restarted,
  // or the session was opened while its run was already going. Nothing
  // of that run is in the transcript the hydrate rebuilt — the archive
  // does not hold a live turn — and the machine adopts the run off the
  // wire, so its rows need a turn of their own. Without one the first
  // delta grows the last archived answer (lastAssistant appends to the
  // trailing row) and the strip the run's own events look it up by is
  // missing.
  it('opens a turn for a run the window only met on the wire', async () => {
    stateRoot.registry.ensure('s-2', {
      workspaceGeneration: stateRoot.generation(),
      workspace: '/tmp/w',
    });
    apiMock.sessionTurns.mockResolvedValue([
      historyTurn(1, 'earlier question', 'earlier answer'),
    ]);

    await useStore.getState().resume('s-2');

    // Nothing knows the run yet: the transcript is the archive alone.
    expect(actorValue('s-2')?.turn).toBe('idle');
    const hydrated = useStore.getState().conversations['s-2'];
    expect(hydrated.messages).toHaveLength(2);
    expect(hydrated.turnArtifacts).toHaveLength(1);

    const handle = useStore.getState().handleEvent;
    handle({
      type: 'stream',
      data: {
        run_id: 'r-2',
        conversation_id: 's-2',
        delta: {
          type: 'part',
          part: { type: 'text', text: 'the new answer' },
        },
      },
    });
    useStore.getState().flushStreams();

    expect(actorValue('s-2')?.turn).toBe('running');
    const conv = useStore.getState().conversations['s-2'];
    // The delta opened a row of the run's own; the archived answer kept
    // its text, and the run got the strip that owns it.
    expect(assistantTexts(conv.messages)).toEqual([
      'earlier answer',
      'the new answer',
    ]);
    expect(conv.turnArtifacts).toHaveLength(2);
    const live = conv.turnArtifacts[1];
    expect(live).toMatchObject({ start: 2, runID: 'r-2' });
    expect(live.seq).toBeUndefined();
    expect(live.finishedAt).toBeUndefined();

    // The run keeps that turn: the next delta folds into the row the run
    // opened, and no second strip appears.
    handle({
      type: 'stream',
      data: {
        run_id: 'r-2',
        conversation_id: 's-2',
        delta: { type: 'part', part: { type: 'text', text: ' and more' } },
      },
    });
    useStore.getState().flushStreams();
    const after = useStore.getState().conversations['s-2'];
    expect(assistantTexts(after.messages)).toEqual([
      'earlier answer',
      'the new answer and more',
    ]);
    expect(after.turnArtifacts).toHaveLength(2);
    expect(after.turnArtifacts[1].id).toBe(live.id);
  });

  // The same seam before the run's first row exists: the hydrate opened
  // the strip where the archive ends, and the delta has to open a row it
  // owns rather than fold into the archived answer above the seam.
  it('keeps a delta out of the rows above a live strip with no rows yet', async () => {
    stateRoot.registry.ensure('s-2', {
      workspaceGeneration: stateRoot.generation(),
      workspace: '/tmp/w',
    });
    stateRoot.registry.get('s-2')?.send({ type: 'RUN_STARTED', runID: 'r-2' });
    // A shell the window holds for this conversation, with no rows of
    // its own yet.
    useStore.setState({
      conversations: {
        ...useStore.getState().conversations,
        's-2': { ...useStore.getState().conversations['s-1'] },
      },
    });
    apiMock.sessionTurns.mockResolvedValue([
      historyTurn(1, 'earlier question', 'earlier answer'),
    ]);

    await useStore.getState().resume('s-2');

    const hydrated = useStore.getState().conversations['s-2'];
    expect(hydrated.messages).toHaveLength(2);
    expect(hydrated.turnArtifacts[1]).toMatchObject({
      start: 2,
      runID: 'r-2',
    });

    useStore.getState().handleEvent({
      type: 'stream',
      data: {
        run_id: 'r-2',
        conversation_id: 's-2',
        delta: {
          type: 'part',
          part: { type: 'text', text: 'the live answer' },
        },
      },
    });
    useStore.getState().flushStreams();

    const conv = useStore.getState().conversations['s-2'];
    expect(assistantTexts(conv.messages)).toEqual([
      'earlier answer',
      'the live answer',
    ]);
    expect(conv.turnArtifacts).toHaveLength(2);
    expect(conv.turnArtifacts[1].start).toBe(2);
  });

  // A strip that already finished is not a live entry: a send that never
  // started leaves its turn stamped but unnamed, and a run met on the
  // wire must not fold into that turn.
  it('does not take a failed send for a run it meets on the wire', () => {
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          messages: [
            {
              id: 'm-failed',
              role: 'user',
              text: 'the send that never started',
              items: [],
              attachments: [],
            },
          ],
          turnArtifacts: [
            {
              id: 'failed',
              start: 0,
              docs: [],
              status: 'failed',
              error: 'Error: start boom',
              finishedAt: '2026-10-15T00:00:01Z',
            },
          ],
        },
      },
    });

    useStore.getState().handleEvent({
      type: 'stream',
      data: {
        run_id: 'r-new',
        conversation_id: 's-1',
        delta: {
          type: 'part',
          part: { type: 'text', text: 'the real answer' },
        },
      },
    });
    useStore.getState().flushStreams();

    const conv = useStore.getState().conversations['s-1'];
    expect(conv.turnArtifacts).toHaveLength(2);
    expect(conv.turnArtifacts[0]).toMatchObject({
      id: 'failed',
      status: 'failed',
    });
    expect(conv.turnArtifacts[1]).toMatchObject({ start: 1, runID: 'r-new' });
    expect(assistantTexts(conv.messages)).toEqual(['the real answer']);
    expect(conversationForRun('r-new')).toBe('s-1');
  });

  // While a send's response is in flight its strip is the trailing live
  // entry with no run id, and the first deltas of that very run fold into
  // it: nothing is adopted (the response names this strip when it lands)
  // and the run is not mapped before then.
  it("folds a pending send's deltas into its unnamed strip", () => {
    stateRoot.registry.get('s-1')?.send({ type: 'SUBMIT', kind: 'send' });
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          messages: [
            {
              id: 'm-send',
              role: 'user',
              text: 'the send in flight',
              items: [],
              attachments: [],
            },
          ],
          turnArtifacts: [{ id: 'pending', start: 0, docs: [] }],
        },
      },
    });

    useStore.getState().handleEvent({
      type: 'stream',
      data: {
        run_id: 'r-late',
        conversation_id: 's-1',
        delta: {
          type: 'part',
          part: { type: 'text', text: 'streaming early' },
        },
      },
    });
    useStore.getState().flushStreams();

    const conv = useStore.getState().conversations['s-1'];
    expect(conv.turnArtifacts).toEqual([
      expect.objectContaining({ id: 'pending', start: 0 }),
    ]);
    expect(assistantTexts(conv.messages)).toEqual(['streaming early']);
    expect(conversationForRun('r-late')).toBeUndefined();
  });

  // A result only ever stamps a call the run itself opened: the walk
  // stops at the run's rows, so a call above the seam (an interrupted
  // turn's, left running) is not rewritten by a stream naming its id.
  it("does not stamp a tool call above the run's rows", () => {
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          messages: [
            {
              id: 'm-call',
              role: 'assistant',
              text: '',
              items: [
                {
                  kind: 'tool_call',
                  id: 'part-1',
                  tool: {
                    id: 'call-1',
                    name: 'exec_command',
                    args: '{}',
                    status: 'running',
                  },
                },
              ],
              attachments: [],
            },
          ],
          turnArtifacts: [
            {
              id: 'old',
              start: 0,
              docs: [],
              seq: 1,
              finishedAt: '2026-10-15T00:00:01Z',
            },
          ],
        },
      },
    });

    const handle = useStore.getState().handleEvent;
    handle({
      type: 'stream',
      data: {
        run_id: 'r-new',
        conversation_id: 's-1',
        delta: { type: 'part', part: { type: 'text', text: 'the answer' } },
      },
    });
    handle({
      type: 'stream',
      data: {
        run_id: 'r-new',
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
    useStore.getState().flushStreams();

    const call = useStore.getState().conversations['s-1'].messages[0].items[0];
    if (call.kind !== 'tool_call') throw new Error('expected a tool call');
    expect(call.tool.status).toBe('running');
    expect(call.tool.result).toBeUndefined();
  });

  // The app can be restarted between a tool call and its result: the
  // result is then the first thing this window sees of the run. It still
  // opens the run's turn (the strip is what later deltas, files and the
  // archived copy hang off), and a result no row holds stamps nothing.
  it("takes a tool result as the first delta of a run's stream", () => {
    useStore.getState().handleEvent({
      type: 'stream',
      data: {
        run_id: 'r-new',
        conversation_id: 's-1',
        delta: {
          type: 'part',
          part: {
            type: 'tool_result',
            result: {
              call_id: 'call-gone',
              content: { parts: [{ type: 'text', text: '{"content":"ok"}' }] },
              is_error: false,
            },
          },
        },
      },
    });
    useStore.getState().flushStreams();

    const conv = useStore.getState().conversations['s-1'];
    expect(conv.messages).toHaveLength(0);
    expect(conv.turnArtifacts).toHaveLength(1);
    expect(conv.turnArtifacts[0]).toMatchObject({ start: 0, runID: 'r-new' });

    // The run's text lands in the turn that result opened.
    useStore.getState().handleEvent({
      type: 'stream',
      data: {
        run_id: 'r-new',
        conversation_id: 's-1',
        delta: {
          type: 'part',
          part: { type: 'text', text: 'the answer' },
        },
      },
    });
    useStore.getState().flushStreams();
    const after = useStore.getState().conversations['s-1'];
    expect(assistantTexts(after.messages)).toEqual(['the answer']);
    expect(after.turnArtifacts).toHaveLength(1);
  });

  // The adopted run's turn is written to the archive when it ends — the
  // question included, which this window never had — and the reconcile
  // replaces the live rows with that copy.
  it('replaces an adopted run with its archived turn once it ends', async () => {
    stateRoot.registry.ensure('s-2', {
      workspaceGeneration: stateRoot.generation(),
      workspace: '/tmp/w',
    });
    apiMock.sessionTurns.mockResolvedValue([
      historyTurn(1, 'earlier question', 'earlier answer'),
    ]);

    await useStore.getState().resume('s-2');
    useStore.getState().handleEvent({
      type: 'stream',
      data: {
        run_id: 'r-2',
        conversation_id: 's-2',
        delta: {
          type: 'part',
          part: { type: 'text', text: 'half an answer' },
        },
      },
    });
    useStore.getState().flushStreams();

    apiMock.turnByRunID.mockResolvedValue({
      ...historyTurn(2, 'the question', 'the whole answer'),
      run_id: 'r-2',
      status: 'completed',
    });
    useStore.getState().handleEvent({
      type: 'turn_end',
      data: {
        run_id: 'r-2',
        conversation_id: 's-2',
        status: 'completed',
        finished_at: '2026-10-15T00:02:00Z',
        duration_ms: 1000,
      },
    });

    await vi.waitFor(() => {
      expect(
        assistantTexts(useStore.getState().conversations['s-2'].messages),
      ).toEqual(['earlier answer', 'the whole answer']);
    });
    const conv = useStore.getState().conversations['s-2'];
    expect(
      conv.messages.some((m) => m.role === 'user' && m.text === 'the question'),
    ).toBe(true);
    expect(conv.turnArtifacts).toHaveLength(2);
    expect(conv.turnArtifacts[1]).toMatchObject({ seq: 2, runID: 'r-2' });
    expect(actorValue('s-2')?.turn).toBe('succeeded');
  });
});

// The cost of switching models mid-conversation is invisible in the
// transcript: provider prompt caches are scoped to the model, so the next
// request re-reads everything at undiscounted input price. The store says
// so once, and only when there is a cache to lose.
describe('store: model switch cost notice', () => {
  it('warns when a conversation with content switches model', async () => {
    useStore.setState({
      conversations: {
        's-1': {
          messages: [
            {
              id: 'm-1',
              role: 'user',
              text: 'hello',
              parts: [],
              at: '2026-09-03T00:00:00Z',
            },
          ] as never,
          turnArtifacts: [],
          mode: 'workspace',
          think: 'medium',
          model: 'provider/m-1',
          pendingInteracts: [],
        },
      },
    });
    await useStore.getState().setModel('provider/m-2');
    const toasts = useStore.getState().toasts;
    expect(toasts).toHaveLength(1);
    expect(toasts[0].kind).toBe('warning');
    expect(toasts[0].text).toContain('provider/m-2');
  });

  it('stays quiet for an empty conversation, a re-pick, and no model', async () => {
    await useStore.getState().setModel('provider/m-1');
    expect(useStore.getState().toasts).toHaveLength(0);

    useStore.setState({
      conversations: {
        's-1': {
          messages: [] as never,
          turnArtifacts: [],
          mode: 'workspace',
          think: 'medium',
          model: 'provider/m-1',
          pendingInteracts: [],
        },
      },
    });
    await useStore.getState().setModel('provider/m-2');
    expect(useStore.getState().toasts).toHaveLength(0);
  });
});

describe('store: mid-turn steer', () => {
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

describe('store: steered rows across transcript rebuilds', () => {
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

// A delegation note is written when its subagent finishes, which can be
// long after the turn that spawned it ended. Nothing streams it, so a
// conversation that is already open reads the tail of the archive for
// it; these tests pin when that read is allowed to fold rows in.
describe('store: transcript tail sync', () => {
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

// The automation views refresh their lists from three directions at once
// (a start, a settlement, the panel's own poll), and the answer that
// arrives last is the one the list shows — unless the store refuses a
// stale one the way it already does for the workspace list.
describe('store: automation lists', () => {
  const task = (id: string): AutomationTask => ({
    id,
    name: id,
    prompt: '',
    schedule: { type: 'daily', time: '09:00' },
    workspace: '/tmp/w',
    mode: 'workspace',
    model: '',
    think: 'medium',
    notify: '',
    timeout: '15m',
    enabled: true,
    created_at: '',
    updated_at: '',
    last_run_at: '',
    last_status: '',
    next_run_at: '',
  });
  const run = (id: string): AutomationRun => ({
    id,
    task_id: 't-1',
    at: '',
    status: 'succeeded',
    error: '',
    conversation_id: '',
    run_id: '',
    duration_ms: 0,
    summary: '',
  });

  it('keeps only the newest task list', async () => {
    let releaseFirst: ((list: AutomationTask[]) => void) | undefined;
    const first = new Promise<AutomationTask[]>((resolve) => {
      releaseFirst = resolve;
    });
    apiMock.automations.mockReturnValueOnce(first);
    apiMock.automations.mockResolvedValueOnce([task('t-2')]);

    const older = useStore.getState().loadAutomations();
    const newer = useStore.getState().loadAutomations();
    await newer;
    releaseFirst?.([task('t-1')]);
    await older;

    // The stale answer describes the list as it was before the newer
    // refresh: a task that has just started would flip back to idle.
    expect(useStore.getState().automations.map((t) => t.id)).toEqual(['t-2']);
  });

  it('keeps only the newest run list of one task', async () => {
    let releaseFirst: ((list: AutomationRun[]) => void) | undefined;
    const first = new Promise<AutomationRun[]>((resolve) => {
      releaseFirst = resolve;
    });
    apiMock.automationRuns.mockReturnValueOnce(first);
    apiMock.automationRuns.mockResolvedValueOnce([run('run-2')]);

    const older = useStore.getState().loadAutomationRuns('t-1');
    const newer = useStore.getState().loadAutomationRuns('t-1');
    await newer;
    releaseFirst?.([run('run-1')]);
    await older;

    expect(
      (useStore.getState().automationRuns['t-1'] ?? []).map((r) => r.id),
    ).toEqual(['run-2']);
  });
});
