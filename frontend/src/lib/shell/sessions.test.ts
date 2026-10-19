// shell/sessions.ts: resume, delete, new chat, the cross-workspace
// switch a sidebar click starts, and the init bootstrap.

import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { FileViewerState } from '../store';
import i18n from '../../i18n';
import { stateRoot } from '../../state/app';
import { useStore } from '../store';
import { apiMock, diagnosticsMock } from '../../test/apiMock';
import {
  actorValue,
  bindRun,
  conversationForRun,
  historyTurn,
  primeStoreTest,
} from '../../test/storeHarness';
import { installMemoryLocalStorage } from '../../test/storage';

vi.mock('../api', async () => ({
  api: (await import('../../test/apiMock')).apiMock,
}));
vi.mock(
  '../../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/diagnostics',
  async () => (await import('../../test/apiMock')).diagnosticsMock,
);

beforeEach(primeStoreTest);

describe('resume, delete and new chat', () => {
  it('resuming the active session closes the tool page', async () => {
    useStore.setState({ toolsView: 'plugins' });
    await useStore.getState().resume('s-1');

    expect(useStore.getState().toolsView).toBeNull();
    expect(apiMock.resumeSession).not.toHaveBeenCalled();
  });

  it('opening the row clears the mark its failed turn left', async () => {
    // Clicking the row is the reader answering it. Both branches clear:
    // the early return for the conversation already on screen is still a
    // click on the row that was asking.
    useStore.setState({ turnAttention: { 's-1': 'r-1' } });

    await useStore.getState().resume('s-1');
    expect(useStore.getState().turnAttention).toEqual({});

    // And another row's mark is not collateral.
    useStore.setState({ turnAttention: { 's-2': 'r-2' } });
    await useStore.getState().resume('s-1');
    expect(useStore.getState().turnAttention).toEqual({ 's-2': 'r-2' });
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

  it('a delete forgets the mark too, since the row never comes back', async () => {
    apiMock.deleteSession.mockResolvedValue({
      conversation_id: 's-next',
      mode: 'workspace',
      think: 'medium',
      model: '',
    });
    useStore.setState({ turnAttention: { 's-1': 'r-1', 's-2': 'r-2' } });

    await useStore.getState().deleteSession('s-1');

    // Session ids are never reused (deleted_conversations), so a mark
    // left behind would only ever be a leak.
    expect(useStore.getState().turnAttention).toEqual({ 's-2': 'r-2' });
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
});

describe('cross-workspace session clicks', () => {
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

// A system banner is a click from outside the app: the window may be on
// another workspace and covered by a tool page, and the conversation the
// banner is about is the one the reader asked for.

describe('notification click', () => {
  it('opens the conversation in the workspace the banner named', async () => {
    apiMock.openWorkspace.mockResolvedValue(undefined);
    apiMock.workspace.mockResolvedValue('/tmp/b');
    apiMock.currentSession.mockResolvedValue('');
    apiMock.resumeSession.mockResolvedValue({
      conversation_id: 's-b',
      mode: 'workspace',
      think: 'medium',
      model: '',
    });
    useStore.setState({
      workspace: '/tmp/a',
      toolsView: 'plugins',
      configOpen: true,
    });

    await useStore.getState().openFromNotification('s-b', '/tmp/b');

    expect(apiMock.openWorkspace).toHaveBeenCalledWith('/tmp/b');
    expect(useStore.getState().workspace).toBe('/tmp/b');
    expect(apiMock.resumeSession).toHaveBeenCalledWith('s-b');
    expect(stateRoot.focusSnapshot.context.sessionID).toBe('s-b');
    // The click lands on the conversation, not on whatever page covered
    // it when the window lost focus.
    expect(useStore.getState().toolsView).toBeNull();
    expect(useStore.getState().configOpen).toBe(false);
  });

  it('opens a banner that lost its workspace where the window is', async () => {
    await useStore.getState().openFromNotification('s-2', '');

    expect(apiMock.openWorkspace).not.toHaveBeenCalled();
    expect(apiMock.resumeSession).toHaveBeenCalledWith('s-2');
    expect(stateRoot.focusSnapshot.context.sessionID).toBe('s-2');
  });

  it('stops at the refusal when the conversation lives elsewhere', async () => {
    // A producer that predates the workspace field names the
    // conversation only. When that conversation belongs to another
    // workspace, the backend refuses it: the window stays on the chat it
    // had (the failure says why) instead of being pointed at an id its
    // own store can never fill.
    apiMock.resumeSession.mockRejectedValue(
      new Error('conversation: session "s-x" is not in workspace "/tmp/w"'),
    );

    await useStore.getState().openFromNotification('s-x', '');

    expect(apiMock.openWorkspace).not.toHaveBeenCalled();
    expect(stateRoot.focusSnapshot.value).toBe('failed');
    expect(stateRoot.focusSnapshot.context.sessionID).not.toBe('s-x');
  });

  it('says so when the workspace would not open', async () => {
    apiMock.openWorkspace.mockRejectedValue(new Error('no such workspace'));

    await useStore.getState().openFromNotification('s-b', '/tmp/b');

    expect(apiMock.resumeSession).not.toHaveBeenCalled();
    expect(useStore.getState().toasts.at(-1)).toMatchObject({
      kind: 'warning',
      text: i18n.t('chat.switchFailed'),
    });
  });
});

describe('init session bootstrap', () => {
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
    // jsdom's localStorage is Node's stub on newer runtimes (its setItem
    // never round-trips), so the preference needs the in-memory one.
    installMemoryLocalStorage();
    window.localStorage.setItem('opencraft.theme', 'light');

    await useStore.getState().init();

    expect(useStore.getState().theme).toBe('light');
    expect(useStore.getState().resolvedTheme).toBe('light');
    expect(document.documentElement.classList.contains('theme-light')).toBe(
      true,
    );
  });
});
