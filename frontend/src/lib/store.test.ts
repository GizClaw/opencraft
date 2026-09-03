import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { MessageView } from './store';
import { stateRoot } from '../state/app';
import { useStore } from './store';

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
  loadWorkspaces: vi.fn(),
  loadAutomations: vi.fn(),
  newChat: vi.fn(),
  startTurn: vi.fn(),
  cancelTurn: vi.fn(),
  deleteSession: vi.fn(),
  replyPrompt: vi.fn(),
  undoState: vi.fn(),
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
    navigation: { name: 'ready', sessionID: 's-1', epoch: 0 },
    conversations: {
      's-1': {
        content: { name: 'empty' },
        turn: { name: 'idle' },
        messages: [],
        turnArtifacts: [],
        mode: 'workspace',
        think: 'medium',
        model: '',
        pendingInteracts: [],
      },
    },
    runConvs: {},
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
  apiMock.undoState.mockResolvedValue({ can_undo: false, can_redo: false });
});

describe('store: send and stream', () => {
  it('send appends the user message and registers the run', async () => {
    await useStore.getState().send('hello');

    const conv = useStore.getState().conversations['s-1'];
    expect(conv.turn).toMatchObject({ name: 'running', runID: 'r-1' });
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
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          turn: { name: 'starting' },
        },
      },
    });
    await useStore.getState().send('ignored');
    expect(apiMock.startTurn).not.toHaveBeenCalled();

    useStore.setState({ configured: false });
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          turn: { name: 'idle' },
        },
      },
    });
    await useStore.getState().send('also ignored');
    expect(apiMock.startTurn).not.toHaveBeenCalled();
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
      navigation: { name: 'ready', sessionID: 's-old', epoch: 0 },
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
    expect(state.navigation).toMatchObject({
      name: 'ready',
      sessionID: 's-new',
    });
    expect(state.runConvs).toEqual({ 'r-old': 's-old' });
    expect(state.conversations['s-old']).toBeDefined();
    expect(state.conversations['s-new']).toBeDefined();
    expect(state.conversations['s-new'].messages).toEqual([]);
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

    expect(useStore.getState().navigation).toMatchObject({
      name: 'ready',
      sessionID: 's-new',
    });
  });

  it('resume applies the snapshot and loads history into a ready conversation', async () => {
    apiMock.sessionTurns.mockResolvedValue([
      historyTurn(1, 'history user', 'history answer'),
    ]);

    await useStore.getState().resume('s-2');

    const state = useStore.getState();
    expect(state.navigation).toMatchObject({
      name: 'ready',
      sessionID: 's-2',
    });
    const conv = state.conversations['s-2'];
    expect(conv.content).toEqual({ name: 'ready' });
    expect(conv.mode).toBe('workspace');
    expect(conv.think).toBe('medium');
    expect(conv.messages.map((m) => m.text || '')).toContain('history user');
    expect(apiMock.sessionTurns).toHaveBeenCalledWith('s-2');
  });

  it('resume merges history with an active live shell', async () => {
    useStore.setState({
      conversations: {
        ...useStore.getState().conversations,
        's-2': {
          ...useStore.getState().conversations['s-1'],
          content: { name: 'live-shell' },
          turn: { name: 'running', runID: 'r-live', stage: 'text' },
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
    expect(conv.content).toEqual({ name: 'ready' });
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
    expect(useStore.getState().navigation).toMatchObject({
      name: 'ready',
      sessionID: 's-2',
    });
  });

  it('resume failure leaves navigation failed with the previous session visible', async () => {
    apiMock.resumeSession.mockRejectedValue(new Error('switch failed'));

    await useStore.getState().resume('s-2');

    const nav = useStore.getState().navigation;
    expect(nav).toMatchObject({
      name: 'failed',
      previousSessionID: 's-1',
      error: 'switch failed',
    });
  });

  it('folds stream deltas into one assistant message', () => {
    useStore.setState({
      runConvs: { 'r-1': 's-1' },
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          turn: { name: 'running', runID: 'r-1', stage: '' },
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

    const conv = useStore.getState().conversations['s-1'];
    expect(conv.turn).toMatchObject({ name: 'running', stage: 'text' });
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

  it('turn_end clears busy and removes the run mapping', () => {
    useStore.setState({
      runConvs: { 'r-1': 's-1' },
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          turn: { name: 'running', runID: 'r-1', stage: '' },
        },
      },
    });
    useStore.getState().handleEvent({
      type: 'turn_end',
      data: { run_id: 'r-1', conversation_id: 's-1', status: 'completed' },
    });

    const conv = useStore.getState().conversations['s-1'];
    expect(conv.turn).toEqual({ name: 'finished' });
    expect(useStore.getState().runConvs['r-1']).toBeUndefined();
  });

  it('failed turn_end appends an error marker and sets lastFailed', () => {
    useStore.setState({
      runConvs: { 'r-1': 's-1' },
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          turn: { name: 'running', runID: 'r-1', stage: '' },
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
    expect(conv.turn).toMatchObject({ name: 'failed', error: 'engine boom' });
    const text = conv.messages[0].items.find(
      (i) => i.kind === 'text',
    ) as Extract<MessageView['items'][number], { kind: 'text' }>;
    expect(text.text).toContain('engine boom');
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

    handle({ type: 'resolved', data: { id: 'p-1' } });
    expect(
      useStore.getState().conversations['s-1'].pendingInteracts,
    ).toHaveLength(0);
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
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          turn: { name: 'running', runID: 'r-x', stage: '' },
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
