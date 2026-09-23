import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useStore } from '../lib/store';
import type { SessionMeta, WorkspaceMeta } from '../lib/types';
import { stateRoot } from '../state/app';
import { Sidebar } from './Sidebar';

const apiMock = vi.hoisted(() => ({
  listSessionsInWorkspace: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

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
});
