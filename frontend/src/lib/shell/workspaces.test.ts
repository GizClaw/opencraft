// shell/workspaces.ts: workspace history, first-message attribution,
// and restoring the saved session after a switch.

import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { MessageView } from '../store';
import type { WorkspaceMeta } from '../types';
import { stateRoot } from '../../state/app';
import { useStore } from '../store';
import { apiMock } from '../../test/apiMock';
import { attachmentPreviews } from '../attachmentPreviews';
import { markdownTrees } from '../markdownCache';
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

describe('first-message workspace attribution', () => {
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

describe('workspace history refresh', () => {
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

describe('workspace switch session restore', () => {
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

  it('a workspace switch hands the window back to a fresh page', async () => {
    // The switch is the memory boundary: the transcript the reader left
    // (its decoded images, its text objects, the high-water mark behind
    // them) only goes away with the document that holds it.
    apiMock.openWorkspace.mockResolvedValue(undefined);

    await useStore.getState().openWorkspace('/tmp/b');

    expect(apiMock.openWorkspace).toHaveBeenCalledWith('/tmp/b');
    expect(apiMock.reloadWindow).toHaveBeenCalledTimes(1);
  });

  it('a switch a flow keeps working through does not reload', async () => {
    // openSessionInWorkspace and sendFirstMessage switch the backend and
    // then keep going through it (resume the session, send the first
    // message). A reload would land a new page after the binding returned
    // and drop that work, so only the plain switch reloads.
    apiMock.openWorkspace.mockResolvedValue(undefined);
    apiMock.workspace.mockResolvedValue('/tmp/other');

    await useStore.getState().sendFirstMessage('/tmp/other', 'hello');

    expect(apiMock.openWorkspace).toHaveBeenCalledWith('/tmp/other');
    expect(apiMock.startTurn).toHaveBeenCalledWith(
      's-new',
      expect.objectContaining({ role: 'user' }),
      '/tmp/other',
    );
    expect(apiMock.reloadWindow).not.toHaveBeenCalled();
  });

  it('a workspace switch drops the window caches', async () => {
    // The previews belong to the workspace the window is leaving: those
    // paths name files it is no longer looking at, and a cache that
    // outlived every switch was the window's largest unbounded map. The
    // parsed markdown blocks go with them: the transcript that asked for
    // them is gone, and a tree is not addressed by anything the new
    // workspace would ask for again.
    await attachmentPreviews.load('/tmp/left-behind.png', async () => 'x');
    expect(attachmentPreviews.size).toBe(1);
    markdownTrees.set('# left behind', 'tree' as never);
    expect(markdownTrees.size).toBe(1);

    apiMock.currentSession.mockResolvedValue('');
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
    });
    expect(attachmentPreviews.size).toBe(0);
    expect(markdownTrees.size).toBe(0);
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
