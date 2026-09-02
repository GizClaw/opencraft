import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { MessageView } from './store';
import { useStore } from './store';

const apiMock = vi.hoisted(() => ({
  configStatus: vi.fn(),
  workspace: vi.fn(),
  sessionMode: vi.fn(),
  currentSession: vi.fn(),
  getThink: vi.fn(),
  getModel: vi.fn(),
  modelOptions: vi.fn(),
  listSessions: vi.fn(),
  loadWorkspaces: vi.fn(),
  loadAutomations: vi.fn(),
  startTurn: vi.fn(),
  cancelTurn: vi.fn(),
  deleteSession: vi.fn(),
  replyPrompt: vi.fn(),
  undoState: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

function resetStore() {
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
    current: 's-1',
    conversations: {
      's-1': {
        messages: [],
        turnArtifacts: [],
        busy: false,
        activeRunID: null,
        stage: '',
        mode: 'workspace',
        think: 'medium',
        model: '',
        pendingInteracts: [],
        lastFailed: false,
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
  apiMock.listSessions.mockResolvedValue([]);
  apiMock.undoState.mockResolvedValue({ can_undo: false, can_redo: false });
});

describe('store: send and stream', () => {
  it('send appends the user message and registers the run', async () => {
    await useStore.getState().send('hello');

    const conv = useStore.getState().conversations['s-1'];
    expect(conv.busy).toBe(true);
    expect(conv.activeRunID).toBe('r-1');
    expect(conv.messages[0]).toMatchObject({
      role: 'user',
      text: 'hello',
    });
    expect(useStore.getState().runConvs['r-1']).toBe('s-1');
    expect(apiMock.startTurn).toHaveBeenCalled();
  });

  it('ignores send while busy or unconfigured', async () => {
    useStore.setState({
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          busy: true,
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
          busy: false,
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

  it('folds stream deltas into one assistant message', () => {
    useStore.setState({
      runConvs: { 'r-1': 's-1' },
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          busy: true,
          activeRunID: 'r-1',
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
    expect(conv.stage).toBe('text');
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
          busy: true,
          activeRunID: 'r-1',
        },
      },
    });
    useStore.getState().handleEvent({
      type: 'turn_end',
      data: { run_id: 'r-1', conversation_id: 's-1', status: 'completed' },
    });

    const conv = useStore.getState().conversations['s-1'];
    expect(conv.busy).toBe(false);
    expect(conv.activeRunID).toBeNull();
    expect(conv.lastFailed).toBe(false);
    expect(useStore.getState().runConvs['r-1']).toBeUndefined();
  });

  it('failed turn_end appends an error marker and sets lastFailed', () => {
    useStore.setState({
      runConvs: { 'r-1': 's-1' },
      conversations: {
        's-1': {
          ...useStore.getState().conversations['s-1'],
          busy: true,
          activeRunID: 'r-1',
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
    expect(conv.lastFailed).toBe(true);
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
