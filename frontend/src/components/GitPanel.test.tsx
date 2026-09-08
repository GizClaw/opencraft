import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useStore } from '../lib/store';
import { GitPanel } from './GitPanel';

const sampleDiff = `diff --git a/a.go b/a.go
index 1111111..2222222 100644
--- a/a.go
+++ b/a.go
@@ -1 +1,2 @@
-old line
+new line
`;

const apiMock = vi.hoisted(() => ({
  gitRepo: vi.fn(async () => ({
    in_repo: true,
    root: '/tmp/w',
    branch: 'main',
    upstream: 'origin/main',
    ahead: 1,
    behind: 0,
    workspace: '/tmp/w',
  })),
  gitStatus: vi.fn(async () => ({
    root: '/tmp/w',
    workspace: '/tmp/w',
    branch: 'main',
    truncated: false,
    entries: [
      {
        path: 'internal/a.go',
        kind: 'modified',
        staged: true,
        unstaged: false,
        untracked: false,
        unmerged: false,
        directory: false,
        is_binary: false,
        additions: 1,
        deletions: 1,
        in_workspace: true,
      },
      {
        path: 'scratch/notes.txt',
        kind: 'untracked',
        staged: false,
        unstaged: false,
        untracked: true,
        unmerged: false,
        directory: false,
        is_binary: false,
        additions: 0,
        deletions: 0,
        in_workspace: false,
      },
    ],
  })),
  gitLog: vi.fn(async () => [
    {
      oid: 'abc123',
      short_oid: 'abc123',
      author: 'Alice',
      date: '2026-09-08T00:00:00Z',
      subject: 'feat: add panel',
    },
  ]),
  gitBranches: vi.fn(async () => []),
  gitDiff: vi.fn(async () => ({ content: sampleDiff, truncated: false })),
  gitStage: vi.fn(async () => 'staged'),
  gitUnstage: vi.fn(async () => 'unstaged'),
  gitCommit: vi.fn(async () => 'committed'),
  gitCheckout: vi.fn(async () => 'checked out'),
  gitDiscard: vi.fn(async () => 'discarded'),
  gitClean: vi.fn(async () => 'cleaned'),
  gitNewBranch: vi.fn(async () => 'new branch'),
  gitPull: vi.fn(async () => 'pulled'),
  gitPush: vi.fn(async () => 'pushed'),
  gitHubAvailable: vi.fn(async () => ({ available: false })),
  gitHubPRList: vi.fn(async () => [] as unknown[]),
  gitHubPRDetail: vi.fn(async () => samplePRDetail),
}));

const samplePRDetail = {
  number: 7,
  title: 'ship the panel',
  state: 'open',
  draft: false,
  author: { login: 'octo', avatar_url: '' },
  base: 'main',
  head: 'feat/panel',
  head_sha: 'head-sha',
  created_at: '2026-09-01T00:00:00Z',
  updated_at: '2026-09-08T00:00:00Z',
  html_url: 'https://github.com/o/r/pull/7',
  body: 'Adds the panel.',
  mergeable: true,
  mergeable_state: 'clean',
  changed_files: 1,
  additions: 4,
  deletions: 1,
  commits_count: 1,
  commits: [
    {
      sha: 'c1'.repeat(20),
      short_sha: 'c1c1c1c',
      message: 'feat: panel',
      author: 'Alice',
      date: '2026-09-01T00:00:00Z',
    },
  ],
  checks: [
    {
      name: 'unit',
      kind: 'check_run',
      state: 'failure',
      description: 'tests failed',
      url: '',
    },
  ],
  conversation: [
    {
      id: 1,
      kind: 'comment',
      author: { login: 'carol' },
      body: 'nice work',
      created_at: '2026-09-02T00:00:00Z',
      html_url: '',
    },
  ],
  threads: [
    {
      path: 'a.go',
      side: 'RIGHT',
      line: 2,
      original_line: 1,
      diff_hunk: '@@ -1 +1,2 @@\n-old line\n+new line\n+newest line\n',
      comments: [
        {
          id: 11,
          author: { login: 'erin' },
          body: 'Please handle empty state',
          created_at: '2026-09-02T01:00:00Z',
          html_url: '',
        },
      ],
    },
  ],
  truncated: false,
};

vi.mock('../lib/api', () => ({ api: apiMock }));
vi.mock('../../wailsjs/runtime/runtime', () => ({
  EventsOn: vi.fn(() => vi.fn()),
}));

describe('GitPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useStore.setState({
      workspace: '/tmp/w',
      viewers: {},
    });
  });

  it('lists staged and untracked changes and previews a diff', async () => {
    render(<GitPanel sessionID="s-1" />);

    await screen.findByText('internal/a.go');
    expect(screen.getByText('scratch/notes.txt')).toBeInTheDocument();
    expect(screen.getByText('outside')).toBeInTheDocument();

    (await screen.findByText('internal/a.go')).click();
    await waitFor(() =>
      expect(apiMock.gitDiff).toHaveBeenCalledWith('internal/a.go', true),
    );
    expect(await screen.findByText('new line')).toBeInTheDocument();
  });

  it('switches to commit history', async () => {
    render(<GitPanel sessionID="s-1" />);
    await screen.findByText('internal/a.go');
    (await screen.findByRole('button', { name: 'History' })).click();
    expect(await screen.findByText('feat: add panel')).toBeInTheDocument();
  });

  it('hides the PR segment when no GitHub provider exists', async () => {
    render(<GitPanel sessionID="s-1" />);
    await screen.findByText('internal/a.go');
    expect(
      screen.queryByRole('button', { name: 'PR' }),
    ).not.toBeInTheDocument();
    expect(apiMock.gitHubAvailable).toHaveBeenCalled();
  });

  it('lists pull requests and opens the full detail modal', async () => {
    apiMock.gitHubAvailable.mockResolvedValue({ available: true });
    apiMock.gitHubPRList.mockResolvedValue([
      {
        number: 7,
        title: 'ship the panel',
        state: 'open',
        draft: false,
        author: { login: 'octo', avatar_url: '' },
        base: 'main',
        head: 'feat/panel',
        updated_at: '2026-09-08T00:00:00Z',
        html_url: 'https://github.com/o/r/pull/7',
      },
    ]);
    render(<GitPanel sessionID="s-1" />);

    (await screen.findByRole('button', { name: 'PR' })).click();
    expect(await screen.findByText('ship the panel')).toBeInTheDocument();

    screen.getByText('ship the panel').click();
    await waitFor(() => expect(apiMock.gitHubPRDetail).toHaveBeenCalledWith(7));
    expect(await screen.findByText('Adds the panel.')).toBeInTheDocument();
    expect(screen.getByText('unit')).toBeInTheDocument();
    expect(screen.getByText('Please handle empty state')).toBeInTheDocument();
    expect(screen.getByText('newest line')).toBeInTheDocument();
    expect(screen.getByText('nice work')).toBeInTheDocument();
  });

  it('stages an untracked file', async () => {
    render(<GitPanel sessionID="s-1" />);
    await screen.findByText('scratch/notes.txt');
    (await screen.findByRole('button', { name: 'Stage' })).click();
    await waitFor(() =>
      expect(apiMock.gitStage).toHaveBeenCalledWith(['scratch/notes.txt']),
    );
  });

  it('commits staged changes with the message', async () => {
    render(<GitPanel sessionID="s-1" />);
    await screen.findByText('internal/a.go');
    const input = screen.getByPlaceholderText('Commit message');
    await userEvent.type(input, 'feat: ship');
    (await screen.findByRole('button', { name: 'Commit' })).click();
    await waitFor(() =>
      expect(apiMock.gitCommit).toHaveBeenCalledWith('feat: ship'),
    );
  });

  it('confirms discarding a staged change', async () => {
    render(<GitPanel sessionID="s-1" />);
    await screen.findByText('internal/a.go');
    (await screen.findByRole('button', { name: 'Discard' })).click();
    expect(
      await screen.findByText(
        /Discard staged changes and working-tree changes/,
      ),
    ).toBeTruthy();
    // Both the row action and the dialog confirm share the Discard
    // label; pick the visible dialog action.
    const buttons = await screen.findAllByRole('button', { name: 'Discard' });
    buttons[buttons.length - 1].click();
    await waitFor(() =>
      expect(apiMock.gitDiscard).toHaveBeenCalledWith(['internal/a.go'], true),
    );
  });

  it('creates and switches to a new branch', async () => {
    render(<GitPanel sessionID="s-1" />);
    await screen.findByText('internal/a.go');
    (await screen.findByRole('button', { name: 'Switch branch' })).click();
    (await screen.findByRole('button', { name: 'New branch' })).click();
    const input = await screen.findByPlaceholderText('Branch name');
    await userEvent.type(input, 'feat/panel');
    (await screen.findByRole('button', { name: 'Create & switch' })).click();
    await waitFor(() =>
      expect(apiMock.gitNewBranch).toHaveBeenCalledWith('feat/panel'),
    );
  });

  it('requires typing the branch name to force push', async () => {
    render(<GitPanel sessionID="s-1" />);
    await screen.findByText('internal/a.go');
    (await screen.findByRole('button', { name: 'Force push' })).click();
    const dialog = await screen.findByRole('alertdialog');
    const confirm = within(dialog).getByRole('button', {
      name: 'Force push',
    });
    expect(confirm).toBeDisabled();
    await userEvent.type(screen.getByPlaceholderText('main'), 'main');
    expect(confirm).toBeEnabled();
    confirm.click();
    await waitFor(() => expect(apiMock.gitPush).toHaveBeenCalledWith(true));
  });

  it('switches changes to a file tree with folders', async () => {
    render(<GitPanel sessionID="s-1" />);
    await screen.findByText('internal/a.go');
    (await screen.findByRole('button', { name: 'Tree' })).click();
    expect(await screen.findByText('internal')).toBeInTheDocument();
    expect(screen.getByText('scratch')).toBeInTheDocument();
    expect(screen.getByText('a.go')).toBeInTheDocument();
    expect(screen.getByText('notes.txt')).toBeInTheDocument();
  });

  it('navigates between changes inside the diff modal', async () => {
    render(<GitPanel sessionID="s-1" />);
    await screen.findByText('internal/a.go');
    (await screen.findByText('internal/a.go')).click();
    expect(await screen.findByText('new line')).toBeInTheDocument();
    (await screen.findByRole('button', { name: 'Next change' })).click();
    const dialog = await screen.findByRole('dialog');
    expect(
      await within(dialog).findByText('scratch/notes.txt'),
    ).toBeInTheDocument();
    expect(
      await screen.findByText('Untracked file: nothing to diff yet.'),
    ).toBeInTheDocument();
    expect(await screen.findByText('2/2')).toBeInTheDocument();
  });

  it('refreshes status when the window regains focus', async () => {
    render(<GitPanel sessionID="s-1" />);
    await screen.findByText('internal/a.go');
    const before = apiMock.gitStatus.mock.calls.length;
    fireEvent.focus(window);
    await waitFor(() =>
      expect(apiMock.gitStatus.mock.calls.length).toBeGreaterThan(before),
    );
  });

  it('configures the auto refresh interval from the split button', async () => {
    render(<GitPanel sessionID="s-1" />);
    await screen.findByText('internal/a.go');
    expect(screen.getByText('5 s')).toBeInTheDocument();
    (
      await screen.findByRole('button', {
        name: 'Auto refresh interval',
      })
    ).click();
    (await screen.findByRole('option', { name: '30 s' })).click();
    expect(screen.getByText('30 s')).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.queryByText('5 s')).not.toBeInTheDocument(),
    );
  });
});
