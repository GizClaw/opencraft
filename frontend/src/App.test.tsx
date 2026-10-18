// Where the surface boundaries sit, and what that buys.
//
// The boundary itself is covered in components/ErrorBoundary.test.tsx; the
// question here is the shell: a view that throws on render must leave the
// rail, the status bar and the way to another view alive. Before F-48 the
// only boundary was the root one, so any of these crashes replaced the
// whole window with the crash card.
import { render, screen } from '@testing-library/react';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from './i18n';

const shell = vi.hoisted(() => ({
  state: {
    init: async () => {},
    handleEvent: () => {},
    flushStreams: () => {},
    status: { running: true },
    fatal: '',
    configOpen: false,
    toolsView: false,
    workspace: '/tmp/w',
  },
}));

vi.mock('./lib/store', () => {
  const useStore = (select: (state: unknown) => unknown) => select(shell.state);
  useStore.getState = () => shell.state;
  return { useStore };
});

vi.mock('./plugins/store', () => ({
  usePluginStore: { getState: () => ({ load: async () => {} }) },
}));

vi.mock('./lib/api', () => ({ api: { setLanguage: vi.fn(async () => {}) } }));

vi.mock('@wailsio/runtime', () => ({
  Events: { On: () => () => {} },
  System: { Environment: async () => ({ OS: 'linux', Debug: false }) },
}));

vi.mock('./lib/shellCommands', () => ({ useShellCommands: () => () => {} }));
vi.mock('./lib/useShortcuts', () => ({ useShortcuts: () => {} }));

// The shell around the views: each one says it rendered, so the assertion
// "the rest of the window survived" has something to look at.
vi.mock('./components/Sidebar', () => ({
  Sidebar: () => <span>sidebar</span>,
}));
vi.mock('./components/SidebarResizeHandle', () => ({
  SidebarResizeHandle: () => null,
}));
vi.mock('./components/TopBar', () => ({ TopBar: () => <span>topbar</span> }));
vi.mock('./components/StatusBar', () => ({
  StatusBar: () => <span>statusbar</span>,
}));
vi.mock('./components/SubagentDock', () => ({
  SubagentDock: () => <span>dock</span>,
}));
vi.mock('./components/Toaster', () => ({ Toaster: () => null }));
vi.mock('./components/CommandPalette', () => ({
  CommandPalette: () => null,
}));
vi.mock('./components/ShortcutSheet', () => ({ ShortcutSheet: () => null }));
vi.mock('./components/ui/Tooltip', () => ({ TooltipLayer: () => null }));
vi.mock('./components/WelcomeView', () => ({
  WelcomeView: () => <span>welcome</span>,
}));

// The three surfaces under test. Chat throws as soon as it renders; the
// other two are reachable only through the store flags.
vi.mock('./components/ChatView', () => ({
  ChatView: () => {
    throw new Error('transcript row is missing its kind');
  },
}));
vi.mock('./components/ToolsPanel', () => ({
  ToolsPanel: () => {
    throw new Error('agent graph could not be laid out');
  },
}));
vi.mock('./components/ConfigPage', () => ({
  ConfigPage: () => {
    throw new Error('driver fields are not an object');
  },
}));

// React prints every caught render error; the boundary reports through the
// console it captured when it was imported, so silence both before any
// import runs.
vi.hoisted(() => {
  console.error = () => {};
});

import App from './App';

beforeAll(async () => {
  await i18n.changeLanguage('en');
});

beforeEach(() => {
  shell.state.configOpen = false;
  shell.state.toolsView = false;
  shell.state.workspace = '/tmp/w';
});

const shellAlive = () => {
  expect(screen.getByText('sidebar')).toBeInTheDocument();
  expect(screen.getByText('topbar')).toBeInTheDocument();
  expect(screen.getByText('statusbar')).toBeInTheDocument();
};

describe('App surface boundaries', () => {
  it('keeps the shell when the chat view throws', () => {
    render(<App />);

    shellAlive();
    expect(screen.getByText(i18n.t('app.surfaceFailed'))).toBeInTheDocument();
    // The window-sized card is what this replaced.
    expect(screen.queryByText(i18n.t('app.crashed'))).not.toBeInTheDocument();
  });

  it('keeps the shell when the tools panel throws', async () => {
    shell.state.toolsView = true;
    render(<App />);

    // The panel is a lazy chunk: the failure arrives after it resolves.
    expect(
      await screen.findByText(i18n.t('app.surfaceFailed')),
    ).toBeInTheDocument();
    shellAlive();
  });

  it('keeps the shell when the settings page throws', async () => {
    // No workspace: the settings page is the only surface mounted, so its
    // own frame is what the card has to replace.
    shell.state.workspace = '';
    shell.state.configOpen = true;
    const { container } = render(<App />);

    expect(
      await screen.findByText(i18n.t('app.surfaceFailed')),
    ).toBeInTheDocument();
    shellAlive();
    // The settings page owns the whole window; its failure has to sit in
    // the same frame instead of falling back into the column below.
    expect(container.querySelector('.fixed.inset-0')).not.toBeNull();
  });

  it('shows the welcome view when no workspace is open', () => {
    shell.state.workspace = '';
    render(<App />);

    shellAlive();
    expect(screen.getByText('welcome')).toBeInTheDocument();
    expect(
      screen.queryByText(i18n.t('app.surfaceFailed')),
    ).not.toBeInTheDocument();
  });
});
