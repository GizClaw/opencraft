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
});
