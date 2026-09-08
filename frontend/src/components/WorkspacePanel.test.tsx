import { render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useStore } from '../lib/store';
import { WorkspacePanel } from './WorkspacePanel';

const apiMock = vi.hoisted(() => ({
  gitRepo: vi.fn(),
  gitStatus: vi.fn(async () => ({
    root: '/tmp/w',
    workspace: '/tmp/w',
    branch: 'main',
    truncated: false,
    entries: [],
  })),
  gitLog: vi.fn(async () => []),
  gitBranches: vi.fn(async () => []),
  gitDiff: vi.fn(async () => ({ content: '', truncated: false })),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));
vi.mock('@wailsio/runtime', () => ({
  Events: { On: vi.fn(() => vi.fn()) },
}));

function setState(mode: 'files' | 'git') {
  useStore.setState({
    workspace: '/tmp/w',
    viewers: {
      's-1': {
        filesOpen: true,
        panelMode: mode,
        fileTabs: [],
        fileActive: null,
        fileTreeDir: '.',
      },
    },
  });
}

describe('WorkspacePanel', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('shows Files and Git segments inside a repository', async () => {
    apiMock.gitRepo.mockResolvedValue({
      in_repo: true,
      root: '/tmp/w',
      branch: 'main',
      ahead: 0,
      behind: 0,
      workspace: '/tmp/w',
    });
    setState('files');
    render(<WorkspacePanel sessionID="s-1" />);
    expect(
      await screen.findByRole('button', { name: 'Files' }),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Git' })).toBeInTheDocument();
  });

  it('hides the Git segment outside a repository and defaults to files', async () => {
    apiMock.gitRepo.mockResolvedValue({
      in_repo: false,
      ahead: 0,
      behind: 0,
      workspace: '/tmp/w',
    });
    setState('git');
    render(<WorkspacePanel sessionID="s-1" />);
    await waitFor(() => expect(apiMock.gitRepo).toHaveBeenCalled());
    expect(
      screen.queryByRole('button', { name: 'Git' }),
    ).not.toBeInTheDocument();
    expect(screen.getByText('Browse workspace')).toBeInTheDocument();
  });
});
