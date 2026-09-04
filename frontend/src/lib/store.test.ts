import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { MessageView } from './store';
import { stateRoot } from '../state/app';
import { friendlyFailure, pendingConversationIDs, useStore } from './store';

const apiMock = vi.hoisted(() => ({
  configStatus: vi.fn(),
  workspace: vi.fn(),
  sessionMode: vi.fn(),
  currentSession: vi.fn(),
  resumeSession: vi.fn(),
  getThink: vi.fn(),
  getModel: vi.fn(),
  modelOptions: vi.fn(),
  listSessions: vi.fn(),
  sessionTurns: vi.fn(),
  turnByRunID: vi.fn(),
  loadWorkspaces: vi.fn(),
  loadAutomations: vi.fn(),
  newChat: vi.fn(),
  startTurn: vi.fn(),
  cancelTurn: vi.fn(),
  deleteSession: vi.fn(),
  replyPrompt: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

function resetStore() {
  stateRoot.resetWorkspace();
  stateRoot.sendFocus({ type: 'RESTORE_FOCUS', sessionID: 's-1' });
  stateRoot.registry.ensure('s-1', {
    workspaceGeneration: stateRoot.generation(),
    readyEmpty: true,
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
    configTab: 'ui',
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
    runConvs: {},
    pendingPromptConvs: {},
    subagentStreams: {},
    subagentStreamAt: {},
    composerDraft: '',
    statusText: '',
    lastUsage: null,
    cards: [],
    subagentCards: [],
    subagentPanelOpen: true,
    modelOptions: [],
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

function actorValue(conversationID: string) {
  const actor = stateRoot.registry.get(conversationID);
  if (!actor) return undefined;
  return actor.getSnapshot().value as {
    lifecycle: string;
    transcript: string;
    turn: string;
  };
}

beforeEach(() => {
  resetStore();
  vi.clearAllMocks();
  apiMock.startTurn.mockResolvedValue({
    run_id: 'r-1',
    context_id: 's-1',
  });
  apiMock.deleteSession.mockRejectedValue(
    new Error('cannot delete the active conversation'),
  );
  apiMock.newChat.mockResolvedValue({
    session_id: 's-new',
    mode: 'workspace',
    think: 'medium',
    model: '',
  });
  apiMock.resumeSession.mockResolvedValue({
    session_id: 's-2',
    mode: 'workspace',
    think: 'medium',
    model: '',
  });
  apiMock.listSessions.mockResolvedValue([]);
  apiMock.sessionTurns.mockResolvedValue([]);
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
    expect(useStore.getState().runConvs['r-1']).toBe('s-1');
    expect(apiMock.startTurn).toHaveBeenCalledWith(
      's-1',
      expect.objectContaining({ role: 'user' }),
    );
  });

  it('ignores send while busy or unconfigured', async () => {
    stateRoot.registry.get('s-1')?.send({ type: 'SEND_STARTED' });
    await useStore.getState().send('ignored');
    expect(apiMock.startTurn).not.toHaveBeenCalled();

    useStore.setState({ configured: false });
    await useStore.getState().send('also ignored');
    expect(apiMock.startTurn).not.toHaveBeenCalled();
  });

  it('resuming the active session closes the tool page', async () => {
    useStore.setState({ toolsView: 'plugins' });
    await useStore.getState().resume('s-1');

    expect(useStore.getState().toolsView).toBeNull();
    expect(apiMock.resumeSession).not.toHaveBeenCalled();
  });

  it('surfaces active-conversation deletion errors as a warning toast', async () => {
    await useStore.getState().deleteSession('s-1');

    expect(useStore.getState().statusText).toBe('');
    expect(useStore.getState().toasts).toMatchObject([
      {
        kind: 'warning',
        text: expect.any(String),
      },
    ]);
  });

  it('new chat switches to an empty conversation without disturbing active runs', async () => {
    useStore.setState({
      runConvs: { 'r-old': 's-old' },
      subagentStreams: { 'r-old': [] },
      subagentStreamAt: { 'r-old': 123 },
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
    expect(state.runConvs).toEqual({ 'r-old': 's-old' });
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
            session_id: 's-old',
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
    expect(apiMock.sessionTurns).toHaveBeenCalledWith('s-2');
  });

  it('resume keeps archived turn status on the turn artifact', async () => {
    apiMock.sessionTurns.mockResolvedValue([
      {
        ...historyTurn(1, 'history user', 'partial answer'),
        status: 'failed',
        error: 'engine boom',
      },
    ]);

    await useStore.getState().resume('s-2');

    const conv = useStore.getState().conversations['s-2'];
    expect(conv.turnArtifacts[0]).toMatchObject({
      status: 'failed',
      error: 'engine boom',
    });
  });

  it('resume strips legacy marker text from older failures', async () => {
    apiMock.sessionTurns.mockResolvedValue([
      {
        seq: 1,
        at: '2026-09-03T00:00:00Z',
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
    expect(assistantTexts).toEqual(['partial']);
    expect(conv.turnArtifacts[0].status).toBe('failed');
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
    useStore.setState({
      runConvs: { 'r-1': 's-1' },
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
              content: '{"content":"ok"}',
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

  it('coalesces queued text deltas into one store update per flush', () => {
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    useStore.setState({
      runConvs: { 'r-1': 's-1' },
    });
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
          .map((item) => item.text)
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
    actor?.send({ type: 'SEND_STARTED' });
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
    useStore.setState({
      runConvs: { 'r-1': 's-1' },
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
    useStore.setState({
      runConvs: { 'r-1': 's-1' },
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

    const conv = useStore.getState().conversations['s-1'];
    expect(actorValue('s-1')?.turn).toBe('succeeded');
    expect(useStore.getState().runConvs['r-1']).toBeUndefined();
  });

  it('failed turn_end stores status on the turn without a message marker', () => {
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    useStore.setState({
      runConvs: { 'r-1': 's-1' },
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
      },
    });

    const conv = useStore.getState().conversations['s-1'];
    expect(actorValue('s-1')?.turn).toBe('failed');
    expect(conv.turnArtifacts[0]).toMatchObject({
      status: 'failed',
      error: 'engine boom',
    });
    const text = conv.messages[0].items.find(
      (i) => i.kind === 'text',
    ) as Extract<MessageView['items'][number], { kind: 'text' }>;
    expect(text.text).toBe('partial');
  });

  it('canceled turn_end keeps the transcript clean and records the status', () => {
    stateRoot.registry.get('s-1')?.send({ type: 'RUN_STARTED', runID: 'r-1' });
    useStore.setState({
      runConvs: { 'r-1': 's-1' },
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

  it('friendlyFailure hides graph internals for provider errors', () => {
    const friendly = friendlyFailure(
      'graph "opencraft-assistant" node "llm": provider_failure during generate',
    );
    expect(friendly).toBeTruthy();
    expect(friendly ?? '').not.toContain('graph "opencraft-assistant"');
    expect(friendly ?? '').not.toContain('provider_failure');
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

  it('artifact events merge docs into the latest turn strip', () => {
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          turnArtifacts: [
            { id: 'turn-1', start: 0, docs: [{ path: 'a.md', bytes: 1 }] },
          ],
        },
      },
    });
    useStore.getState().handleEvent({
      type: 'artifact',
      data: { conversation_id: 's-1', path: 'a.md', bytes: 42 },
    });
    expect(
      useStore.getState().conversations['s-1'].turnArtifacts[0].docs,
    ).toEqual([{ path: 'a.md', bytes: 42 }]);
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

  it('caps conversation messages at 800 and re-bases turn starts', () => {
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
    expect(conv.messages).toHaveLength(800);
    expect(conv.messages[0].text).toBe('message-200');
    // start 100 is below the drop window (200) and is removed; start 900
    // re-bases to 700.
    expect(conv.turnArtifacts.map((t) => t.start)).toEqual([700]);
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
  }

  it('starts a fresh conversation when no current session exists', async () => {
    stateRoot.resetWorkspace();
    stubInitApi({ currentSession: '', workspace: '/tmp/w' });

    await useStore.getState().init();

    expect(apiMock.newChat).toHaveBeenCalledTimes(1);
    expect(stateRoot.focusSnapshot.value).toBe('active');
    expect(stateRoot.focusSnapshot.context.sessionID).toBe('s-new');
    expect(useStore.getState().conversations['s-new']).toBeDefined();
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
});
