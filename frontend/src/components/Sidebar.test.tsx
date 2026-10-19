import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  useStore,
  type ConversationState,
  type MessageView,
} from '../lib/store';
import type { SessionMeta, WorkspaceMeta } from '../lib/types';
import { stateRoot } from '../state/app';
import { Sidebar } from './Sidebar';

const apiMock = vi.hoisted(() => ({
  listSessionsInWorkspace: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

// useVirtualizer is called once per Sidebar render, so counting its calls
// counts the sidebar's renders. React's own <Profiler onRender> only
// reports in the react-dom/profiling build, which unit tests do not load.
const sidebarRenders = vi.hoisted(() => ({ count: 0 }));

vi.mock('@tanstack/react-virtual', async (importOriginal) => {
  const actual =
    await importOriginal<typeof import('@tanstack/react-virtual')>();
  return {
    ...actual,
    useVirtualizer: (options: Parameters<typeof actual.useVirtualizer>[0]) => {
      sidebarRenders.count += 1;
      return actual.useVirtualizer(options);
    },
  };
});

const expandedKey = 'oc.sidebarExpandedWorkspaces';
const workspaceA: WorkspaceMeta = {
  id: 'w-a',
  path: '/tmp/a',
  title: 'Workspace A',
  last_opened: '2026-09-06T00:00:00Z',
};
const workspaceB: WorkspaceMeta = {
  id: 'w-b',
  path: '/tmp/b',
  title: 'Workspace B',
  last_opened: '2026-09-06T00:00:00Z',
};

function meta(id: string, title: string): SessionMeta {
  return {
    id,
    title,
    created_at: '2026-09-06T00:00:00Z',
    updated_at: '2026-09-06T00:00:00Z',
    total_tokens: 0,
    turns: 1,
    messages: 1,
  };
}

function installLocalStorage() {
  const store = new Map<string, string>();
  Object.defineProperty(window, 'localStorage', {
    configurable: true,
    value: {
      getItem: (key: string) => store.get(key) ?? null,
      setItem: (key: string, value: string) => store.set(key, value),
      removeItem: (key: string) => store.delete(key),
      clear: () => store.clear(),
    },
  });
}

function switchWorkspace(path: string, sessions: SessionMeta[]) {
  useStore.setState({ workspace: path, sessions });
}

/** startRuns turns the given conversations into turns in flight, so the
 * sidebar draws them as running rows in `ids` order. */
function startRuns(workspacePath: string, ids: string[]) {
  for (const id of ids) {
    const actor = stateRoot.registry.ensure(id, {
      workspaceGeneration: stateRoot.generation(),
      workspace: workspacePath,
    });
    actor?.send({ type: 'NEW_CHAT_READY' });
    actor?.send({ type: 'RUN_STARTED', runID: `run-${id}` });
  }
}

/** liveConversation is the store's view of one running conversation. A
 * stream flush writes exactly this shape: one patch settles the
 * conversation and replaces the whole conversations map with it
 * (store.ts, updateConv), which is the identity change this suite
 * follows. */
function liveConversation(messages: MessageView[]): ConversationState {
  return {
    messages,
    turnArtifacts: [],
    mode: '',
    think: '',
    model: '',
    pendingInteracts: [],
  };
}

function userMessage(id: string, text: string): MessageView {
  return { id, role: 'user', text, items: [], attachments: [] };
}

/** onlyActiveExpanded keeps the neighbouring workspace collapsed, so the
 * sidebar has no session-list fetch of its own in flight while a test
 * counts its renders. */
function onlyActiveExpanded() {
  window.localStorage.setItem(expandedKey, JSON.stringify([workspaceB.path]));
  switchWorkspace(workspaceB.path, []);
}

/**
 * hiddenSlots returns the slot badges the reader cannot see. The reveal is
 * a class (lib/modifierHeld.ts) and jsdom is not asked to resolve the
 * stylesheet, so the class is the readable answer here; the browser's own
 * verdict is asserted in e2e/shortcuts.spec.ts.
 */
function hiddenSlots() {
  return screen
    .getAllByTestId('session-slot')
    .filter((badge) => badge.classList.contains('invisible'));
}

describe('Sidebar workspace history', () => {
  beforeEach(() => {
    stateRoot.resetWorkspace();
    vi.clearAllMocks();
    sidebarRenders.count = 0;
    installLocalStorage();
    window.localStorage.setItem(
      expandedKey,
      JSON.stringify([workspaceA.path, workspaceB.path]),
    );
    // The virtualizer reads the scroll container size; give jsdom a
    // tall sidebar so history rows are mounted.
    Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
      configurable: true,
      value: 900,
    });
    useStore.setState({
      workspace: workspaceA.path,
      sessions: [],
      workspaces: [workspaceA, workspaceB],
      conversations: {},
      viewers: {},
      turnAttention: {},
      configured: true,
    });
  });

  it('refetches a workspace list after leaving it, so deleted sessions disappear', async () => {
    const sessionA = meta('s-a1', 'stale session in A');
    const sessionB = meta('s-b1', 'session in B');
    let aFetches = 0;
    apiMock.listSessionsInWorkspace.mockImplementation(async (path: string) => {
      if (path === workspaceA.path) {
        aFetches += 1;
        // The first fetch seeds A's stale snapshot; after the
        // deletion it must return the current (empty) list.
        return aFetches === 1 ? [sessionA] : [];
      }
      return [sessionB];
    });

    render(<Sidebar isMac={false} />);

    // While A is active it renders the live store list; leaving it for
    // B seeds the sidebar cache with A's history.
    switchWorkspace(workspaceB.path, [sessionB]);
    await waitFor(() => expect(aFetches).toBe(1));
    expect(screen.getByText('stale session in A')).toBeInTheDocument();

    // Back in A, delete every session (the live list empties), then
    // switch to B. The A branch must show the refetched empty list
    // instead of the stale snapshot cached while A was non-active.
    switchWorkspace(workspaceA.path, [sessionA]);
    await waitFor(() => expect(aFetches).toBe(1));
    switchWorkspace(workspaceA.path, []);
    switchWorkspace(workspaceB.path, [sessionB]);
    await waitFor(() => expect(aFetches).toBe(2));
    expect(screen.queryByText('stale session in A')).not.toBeInTheDocument();
    expect(screen.getByText('session in B')).toBeInTheDocument();
  });

  it('renders session rows with the title only and turns in the hover card', async () => {
    const sessionB = meta('s-b1', 'session in B');
    switchWorkspace(workspaceB.path, [sessionB]);
    render(<Sidebar isMac={false} />);

    const rowButton = screen.getByRole('button', { name: 'session in B' });
    // The row button overrides the UA default centered button text:
    // without text-left the full-width title span would center.
    expect(rowButton.className).toContain('text-left');
    // No leading icon and no meta second line on the row itself.
    expect(rowButton.querySelector('svg')).toBeNull();
    expect(
      within(rowButton).queryByText(/turn|回合|just now|刚刚/i),
    ).not.toBeInTheDocument();

    const row = rowButton.closest('[data-session-id]');
    expect(row).not.toBeNull();
    fireEvent.mouseEnter(row!);
    const turnsLabel = await screen.findByText('Turns');
    expect(
      within(turnsLabel.closest('div')!).getByText('1'),
    ).toBeInTheDocument();
  });

  it('shows a running spinner on the right side of the row', () => {
    const sessionB = meta('s-run', 'running session');
    switchWorkspace(workspaceB.path, [sessionB]);
    stateRoot.sendFocus({ type: 'RESTORE_FOCUS', sessionID: 's-run' });
    const actor = stateRoot.registry.ensure('s-run', {
      workspaceGeneration: stateRoot.generation(),
      workspace: workspaceB.path,
    });
    actor?.send({ type: 'NEW_CHAT_READY' });
    actor?.send({ type: 'RUN_STARTED', runID: 'r-run' });
    render(<Sidebar isMac={false} />);

    const rowButton = screen.getByRole('button', { name: 'running session' });
    expect(rowButton.querySelector('svg.animate-spin')).not.toBeNull();
    // The spinner is the only row icon; there is no leading icon.
    expect(rowButton.querySelectorAll('svg')).toHaveLength(1);
  });

  it('blinks the row of a run that is waiting on the reader', () => {
    switchWorkspace(workspaceB.path, [meta('s-wait', 'waits on you')]);
    startRuns(workspaceB.path, ['s-wait']);
    useStore.setState({ pendingPromptConvs: { 'p-1': 's-wait' } });
    render(<Sidebar isMac={false} />);

    const row = screen
      .getByRole('button', { name: 'waits on you' })
      .closest('[data-session-id]')!;
    // The pulse is a class: jsdom does not resolve the stylesheet, and
    // the browser's own verdict on the animation lives in
    // e2e/sidebar-attention.spec.ts.
    expect(row.className).toContain('sidebar-row-attention');
    // The question mark replaces the spinner instead of sitting beside
    // it: blocked on the reader is not the same story as still working,
    // and the row can only tell one.
    const rowButton = screen.getByRole('button', { name: 'waits on you' });
    expect(rowButton.querySelector('svg.animate-spin')).toBeNull();
    expect(
      rowButton.querySelector('[data-tip="Waiting for you"]'),
    ).not.toBeNull();
  });

  it('stops blinking once the prompt is answered', () => {
    switchWorkspace(workspaceB.path, [meta('s-wait', 'waits on you')]);
    startRuns(workspaceB.path, ['s-wait']);
    useStore.setState({ pendingPromptConvs: { 'p-1': 's-wait' } });
    const { rerender } = render(<Sidebar isMac={false} />);
    const row = () =>
      screen
        .getByRole('button', { name: 'waits on you' })
        .closest('[data-session-id]')!;
    expect(row().className).toContain('sidebar-row-attention');

    act(() => useStore.setState({ pendingPromptConvs: {} }));
    rerender(<Sidebar isMac={false} />);

    // The turn is still in flight, so the row goes back to what it was
    // before the prompt: a spinner, no pulse.
    expect(row().className).not.toContain('sidebar-row-attention');
    expect(
      screen
        .getByRole('button', { name: 'waits on you' })
        .querySelector('svg.animate-spin'),
    ).not.toBeNull();
  });

  it('marks the row of a conversation whose last turn failed', () => {
    switchWorkspace(workspaceB.path, [meta('s-bad', 'broke on me')]);
    useStore.setState({ turnAttention: { 's-bad': 'r-bad' } });
    render(<Sidebar isMac={false} />);

    const rowButton = screen.getByRole('button', { name: 'broke on me' });
    expect(rowButton.querySelector('svg.animate-spin')).toBeNull();
    expect(rowButton.querySelector('[data-tip="Turn failed"]')).not.toBeNull();
  });

  it('shows a new run instead of the mark its predecessor left', () => {
    // The mark is about the last turn that *ended*. While the next one
    // runs, the row's job is to say the work is happening.
    switchWorkspace(workspaceB.path, [meta('s-1', 'retrying')]);
    startRuns(workspaceB.path, ['s-1']);
    useStore.setState({ turnAttention: { 's-1': 'r-bad' } });
    render(<Sidebar isMac={false} />);

    const rowButton = screen.getByRole('button', { name: 'retrying' });
    expect(rowButton.querySelector('svg.animate-spin')).not.toBeNull();
    expect(rowButton.querySelector('[data-tip="Turn failed"]')).toBeNull();
  });

  it('folds a failed row like any other, and draws it once unfolded', async () => {
    // D2: a failure does not push its row to the top of the list or make
    // it survive the fold. The failure marks a row; it does not reorder
    // the workspace.
    const rows = [1, 2, 3, 4, 5, 6].map((n) => meta(`s-${n}`, `session ${n}`));
    apiMock.listSessionsInWorkspace.mockResolvedValue(rows);
    useStore.setState({ turnAttention: { 's-6': 'r-6' } });
    render(<Sidebar isMac={false} />);

    await screen.findByTestId('more-sessions');
    // session 6 is the oldest, so it sits behind "More sessions" with
    // session 5: the failure did not buy it a place in the preview.
    expect(screen.getByText('session 4')).toBeInTheDocument();
    expect(screen.queryByText('session 5')).not.toBeInTheDocument();
    expect(screen.queryByText('session 6')).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId('more-sessions'));
    const rowButton = await screen.findByRole('button', { name: 'session 6' });
    expect(rowButton.querySelector('[data-tip="Turn failed"]')).not.toBeNull();
  });

  it('numbers the visible rows the session slots name', () => {
    // The digits come from the key table's own formatter, so the badge
    // reads the same on every platform as the shortcut sheet does.
    switchWorkspace(
      workspaceB.path,
      [1, 2, 3].map((n) => meta(`s-${n}`, `session ${n}`)),
    );
    render(<Sidebar isMac />);

    const badges = screen.getAllByTestId('session-slot');
    // The numbers are hints, so they wait to be asked for
    // (lib/modifierHeld.ts) — and they keep their place in the row while
    // they wait: hidden, not unmounted, so the title never re-truncates.
    expect(hiddenSlots()).toHaveLength(3);

    fireEvent.keyDown(window, { key: 'Meta', metaKey: true });
    expect(badges.map((badge) => badge.textContent)).toEqual([
      '⌘1',
      '⌘2',
      '⌘3',
    ]);
    expect(hiddenSlots()).toHaveLength(0);

    fireEvent.keyUp(window, { key: 'Meta' });
    expect(hiddenSlots()).toHaveLength(3);
  });

  it('reveals the numbers only for the modifier its platform binds', () => {
    switchWorkspace(workspaceB.path, [meta('s-1', 'session 1')]);
    render(<Sidebar isMac={false} />);
    const badge = screen.getByTestId('session-slot');

    // Elsewhere the slots are Control+1 …: holding ⌘ is somebody else's
    // gesture and reveals nothing.
    fireEvent.keyDown(window, { key: 'Meta', metaKey: true });
    expect(hiddenSlots()).toHaveLength(1);
    fireEvent.keyDown(window, { key: 'Control', ctrlKey: true });
    expect(hiddenSlots()).toHaveLength(0);

    // And the hints do not outlive the press: ⌘-Tab takes the keyup with
    // it, so losing the window is what ends the reveal.
    fireEvent.blur(window);
    expect(hiddenSlots()).toHaveLength(1);
    expect(badge).toHaveClass('invisible');
  });

  it('moves the numbers onto a running row and pushes the rest down', () => {
    // The running conversation leads the list, so the digits follow it:
    // the number is where the key goes (lib/sessionSlots.ts).
    switchWorkspace(workspaceB.path, [meta('s-1', 'session 1')]);
    const actor = stateRoot.registry.ensure('s-run', {
      workspaceGeneration: stateRoot.generation(),
      workspace: workspaceB.path,
    });
    actor?.send({ type: 'NEW_CHAT_READY' });
    actor?.send({ type: 'RUN_STARTED', runID: 'r-run' });
    render(<Sidebar isMac />);

    const rows = screen.getAllByTestId('session-slot').map((badge) => ({
      combo: badge.textContent,
      session: badge
        .closest('[data-session-id]')
        ?.getAttribute('data-session-id'),
    }));
    expect(rows).toEqual([
      { combo: '⌘1', session: 's-run' },
      { combo: '⌘2', session: 's-1' },
    ]);
  });

  it('numbers only the active workspace, not an expanded neighbour', async () => {
    switchWorkspace(workspaceB.path, [meta('s-b1', 'session in B')]);
    apiMock.listSessionsInWorkspace.mockResolvedValue([
      meta('s-a1', 'session in A'),
    ]);
    render(<Sidebar isMac />);

    const badges = await waitFor(() => {
      expect(screen.getByText('session in A')).toBeInTheDocument();
      return screen.getAllByTestId('session-slot');
    });
    expect(badges).toHaveLength(1);
    expect(
      badges[0].closest('[data-session-id]')?.getAttribute('data-session-id'),
    ).toBe('s-b1');
  });

  it('names the row its slot in the hover card, for readers who never hold the key', async () => {
    // The number on the row is a hint, and a hint has to be known before
    // it can be looked for; the card carries it for whoever only uses the
    // mouse.
    apiMock.listSessionsInWorkspace.mockResolvedValue([
      meta('s-a1', 'session in A'),
    ]);
    switchWorkspace(
      workspaceB.path,
      [1, 2].map((n) => meta(`s-${n}`, `session ${n}`)),
    );
    render(<Sidebar isMac />);

    const row = screen
      .getByRole('button', { name: 'session 2' })
      .closest('[data-session-id]');
    fireEvent.mouseEnter(row!);
    const card = await screen.findByTestId('session-hover-card');
    const label = within(card).getByText('Shortcut');
    expect(within(label.closest('div')!).getByText('⌘2')).toBeInTheDocument();

    // A row the digits do not name — here another workspace's — says
    // nothing about keys.
    const neighbour = await screen.findByRole('button', {
      name: 'session in A',
    });
    fireEvent.mouseEnter(neighbour.closest('[data-session-id]')!);
    await waitFor(() =>
      expect(screen.getByTestId('session-hover-card')).not.toHaveTextContent(
        'Shortcut',
      ),
    );
  });

  it('folds a workspace past the 4-row preview behind "More sessions"', async () => {
    const rows = [1, 2, 3, 4, 5, 6].map((n) => meta(`s-${n}`, `session ${n}`));
    apiMock.listSessionsInWorkspace.mockResolvedValue(rows);
    render(<Sidebar isMac={false} />);

    const more = await screen.findByTestId('more-sessions');
    for (const n of [1, 2, 3, 4]) {
      expect(screen.getByText(`session ${n}`)).toBeInTheDocument();
    }
    for (const n of [5, 6]) {
      expect(screen.queryByText(`session ${n}`)).not.toBeInTheDocument();
    }
    expect(within(more).getByText('+2')).toBeInTheDocument();

    // "More sessions" swaps the preview window for the whole list; the
    // row goes away because nothing is folded any more.
    fireEvent.click(more);
    await waitFor(() =>
      expect(screen.queryByTestId('more-sessions')).not.toBeInTheDocument(),
    );
    for (const n of [1, 2, 3, 4, 5, 6]) {
      expect(screen.getByText(`session ${n}`)).toBeInTheDocument();
    }
  });

  it('keeps every running session when the fold would cut the list', () => {
    // Five turns in flight: the folded list draws all of them — a running
    // row is never hidden behind "More sessions" — and the stored rows
    // wait, because the running ones already fill the window.
    const rows = [1, 2, 3, 4, 5, 6, 7, 8].map((n) =>
      meta(`s-${n}`, `session ${n}`),
    );
    switchWorkspace(workspaceB.path, rows);
    startRuns(workspaceB.path, ['s-1', 's-2', 's-3', 's-4', 's-5']);
    render(<Sidebar isMac={false} />);

    for (const n of [1, 2, 3, 4, 5]) {
      expect(screen.getByText(`session ${n}`)).toBeInTheDocument();
    }
    for (const n of [6, 7, 8]) {
      expect(screen.queryByText(`session ${n}`)).not.toBeInTheDocument();
    }
    expect(
      within(screen.getByTestId('more-sessions')).getByText('+3'),
    ).toBeInTheDocument();
  });

  it('tops the folded window back up with stored sessions under four running', () => {
    // Two turns in flight lead the list; the window still draws four rows,
    // so the two newest stored sessions fill the rest.
    const rows = [1, 2, 3, 4, 5, 6].map((n) => meta(`s-${n}`, `session ${n}`));
    switchWorkspace(workspaceB.path, rows);
    startRuns(workspaceB.path, ['s-1', 's-2']);
    render(<Sidebar isMac={false} />);

    for (const n of [1, 2, 3, 4]) {
      expect(screen.getByText(`session ${n}`)).toBeInTheDocument();
    }
    for (const n of [5, 6]) {
      expect(screen.queryByText(`session ${n}`)).not.toBeInTheDocument();
    }
    expect(
      within(screen.getByTestId('more-sessions')).getByText('+2'),
    ).toBeInTheDocument();
  });

  it('numbers the drawn running rows only as far as the digits go', () => {
    // Five running rows, no stored ones: the numbers stay on the first
    // four — the fifth row is drawn, it just outranks the digits' reach.
    const rows = [1, 2, 3, 4, 5].map((n) => meta(`s-${n}`, `session ${n}`));
    switchWorkspace(workspaceB.path, rows);
    startRuns(workspaceB.path, ['s-1', 's-2', 's-3', 's-4', 's-5']);
    render(<Sidebar isMac />);
    fireEvent.keyDown(window, { key: 'Meta', metaKey: true });

    expect(
      screen.getAllByTestId('session-slot').map((badge) => ({
        combo: badge.textContent,
        session: badge
          .closest('[data-session-id]')
          ?.getAttribute('data-session-id'),
      })),
    ).toEqual([
      { combo: '⌘1', session: 's-1' },
      { combo: '⌘2', session: 's-2' },
      { combo: '⌘3', session: 's-3' },
      { combo: '⌘4', session: 's-4' },
    ]);
    expect(screen.getByText('session 5')).toBeInTheDocument();
  });

  it('numbers the rows the collapsed list draws, and stops at its fold', async () => {
    // sessionPreviewCount and SESSION_SLOTS have to keep agreeing: a digit
    // past the last drawn row would resume a session hiding behind "More
    // sessions", with no number on screen to say so.
    const rows = [1, 2, 3, 4, 5, 6].map((n) => meta(`s-${n}`, `session ${n}`));
    apiMock.listSessionsInWorkspace.mockResolvedValue([]);
    switchWorkspace(workspaceB.path, rows);
    render(<Sidebar isMac />);
    fireEvent.keyDown(window, { key: 'Meta', metaKey: true });

    expect(
      screen.getAllByTestId('session-slot').map((badge) => ({
        combo: badge.textContent,
        session: badge
          .closest('[data-session-id]')
          ?.getAttribute('data-session-id'),
      })),
    ).toEqual([
      { combo: '⌘1', session: 's-1' },
      { combo: '⌘2', session: 's-2' },
      { combo: '⌘3', session: 's-3' },
      { combo: '⌘4', session: 's-4' },
    ]);
    // The fifth row is behind the fold, and so is its number.
    expect(screen.queryByText('session 5')).not.toBeInTheDocument();
  });

  it('leads with the active workspace even when the backend ranks it lower', async () => {
    // History is still [A, B] from the backend: A was the last one
    // recorded, so its last_opened is newer. The user has just switched
    // to B, which is exactly the window where the stored order lags.
    switchWorkspace(workspaceB.path, []);
    const { container } = render(<Sidebar isMac={false} />);

    const headers = Array.from(
      container.querySelectorAll('[role="button"][data-tip]'),
    ).map((el) => el.getAttribute('data-tip'));
    expect(headers).toEqual([workspaceB.path, workspaceA.path]);
  });

  it('keeps the remaining rows in backend order behind the active one', async () => {
    const workspaceC: WorkspaceMeta = {
      id: 'w-c',
      path: '/tmp/c',
      title: 'Workspace C',
      last_opened: '2026-09-07T00:00:00Z',
    };
    // Backend ranks C newest; the promoted active workspace B must not
    // disturb the relative order of the rows that follow it.
    useStore.setState({
      workspaces: [workspaceC, workspaceA, workspaceB],
      workspace: workspaceB.path,
    });
    const { container } = render(<Sidebar isMac={false} />);

    const headers = Array.from(
      container.querySelectorAll('[role="button"][data-tip]'),
    ).map((el) => el.getAttribute('data-tip'));
    expect(headers).toEqual([
      workspaceB.path,
      workspaceC.path,
      workspaceA.path,
    ]);
  });

  it('holds its rows still when a stream delta moves nothing it shows', async () => {
    // store.updateConv replaces the conversations map for one patch, so a
    // subscription to the map itself re-rendered every row on every
    // streamed token. The sidebar now reads one derived title per running
    // row: a delta that leaves those titles alone must commit nothing.
    onlyActiveExpanded();
    startRuns(workspaceB.path, ['s-live']);
    const first = userMessage('m-1', 'first words');
    const assistant = (text: string): MessageView => ({
      id: `m-${text.length}`,
      role: 'assistant',
      text,
      items: [],
      attachments: [],
    });
    const stream = (text: string) =>
      useStore.setState({
        conversations: { 's-live': liveConversation([first, assistant(text)]) },
      });
    useStore.setState({
      conversations: { 's-live': liveConversation([first]) },
    });

    render(<Sidebar isMac={false} />);
    await screen.findByText('first words');
    // The first delta is the warm-up: whatever the first paint left in the
    // scheduler's queue lands with it, so the count below starts on a tree
    // that has nothing else to do.
    await act(async () => {
      stream('first answer');
      await new Promise((resolve) => setTimeout(resolve, 50));
    });
    const settled = sidebarRenders.count;

    await act(async () => {
      stream('first answer grows');
      await new Promise((resolve) => setTimeout(resolve, 50));
    });

    expect(sidebarRenders.count).toBe(settled);
  });

  it('still retitles a running row from its live conversation', async () => {
    // The other half of the same subscription: the row has no archive
    // title yet, so its first user message is what it shows — and it has
    // to arrive when the message does. It also holds the render counter
    // above down: a counter that never moves proves nothing.
    onlyActiveExpanded();
    startRuns(workspaceB.path, ['s-live']);
    useStore.setState({
      conversations: { 's-live': liveConversation([]) },
    });
    render(<Sidebar isMac={false} />);
    expect(await screen.findByText('New session')).toBeInTheDocument();
    const before = sidebarRenders.count;

    act(() => {
      useStore.setState({
        conversations: {
          's-live': liveConversation([userMessage('m-1', 'first words')]),
        },
      });
    });

    expect(screen.getByText('first words')).toBeInTheDocument();
    expect(sidebarRenders.count).toBeGreaterThan(before);
  });
});
