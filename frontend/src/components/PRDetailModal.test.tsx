import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type {
  FilePreview,
  GitHubPRDetail,
  GitHubPull,
  ResolvedTarget,
} from '../lib/types';
import { PRDetailModal } from './PRDetailModal';

const apiMock = vi.hoisted(() => ({
  gitHubPRDetail: vi.fn(),
  resolveTarget: vi.fn(),
  readPreview: vi.fn(),
  openExternal: vi.fn(),
  revealArtifact: vi.fn(),
  openPath: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

const pull: GitHubPull = {
  number: 7,
  title: 'Add search',
  state: 'open',
  draft: false,
  author: { login: 'ada' },
  base: 'main',
  head: 'search',
  updated_at: '2024-05-01T10:00:00Z',
  html_url: 'https://github.com/acme/app/pull/7',
};

function detail(over: Partial<GitHubPRDetail> = {}): GitHubPRDetail {
  return {
    ...pull,
    head_sha: 'abc1234',
    created_at: '2024-05-01T09:00:00Z',
    body: '',
    mergeable: true,
    mergeable_state: 'clean',
    changed_files: 1,
    additions: 3,
    deletions: 1,
    commits_count: 0,
    commits: [],
    checks: [],
    conversation: [],
    threads: [],
    truncated: false,
    ...over,
  };
}

const reference: ResolvedTarget = {
  path: '/tmp/w/docs/deploy.md',
  rel: 'docs/deploy.md',
  root: 'workspace',
  name: 'deploy.md',
  is_dir: false,
  size: 15,
  media_type: 'text/markdown',
};

function referencePreview(): FilePreview {
  return {
    ...reference,
    media_type: 'text/markdown',
    kind: 'text',
    text: 'Deploy the app',
  };
}

describe('PRDetailModal', () => {
  beforeEach(() => {
    apiMock.gitHubPRDetail.mockReset();
    apiMock.resolveTarget.mockReset();
    apiMock.readPreview.mockReset();
    apiMock.openExternal.mockReset();
  });

  it('opens a description link in a dialog above the PR page', async () => {
    apiMock.gitHubPRDetail.mockResolvedValue(
      detail({ body: '[deploy](docs/deploy.md)' }),
    );
    apiMock.resolveTarget.mockResolvedValue(reference);
    apiMock.readPreview.mockResolvedValue(referencePreview());
    render(<PRDetailModal pr={pull} onClose={() => {}} />);

    fireEvent.click(await screen.findByRole('link', { name: 'deploy' }));

    const dialog = await screen.findByRole('dialog', { name: 'deploy.md' });
    expect(
      await within(dialog).findByText('Deploy the app'),
    ).toBeInTheDocument();
    // A PR body has no directory of its own: the reference resolves
    // against the workspace root.
    expect(apiMock.resolveTarget).toHaveBeenCalledWith('docs/deploy.md', '');
  });

  it('follows review comment links out of the thread block', async () => {
    apiMock.gitHubPRDetail.mockResolvedValue(
      detail({
        threads: [
          {
            path: 'src/search.ts',
            line: 12,
            original_line: 12,
            diff_hunk: '',
            comments: [
              {
                id: 1,
                author: { login: 'ada' },
                body: 'See [deploy](docs/deploy.md).',
                created_at: '2024-05-01T09:30:00Z',
                html_url: 'https://github.com/acme/app/pull/7#discussion_r1',
              },
            ],
          },
        ],
      }),
    );
    apiMock.resolveTarget.mockResolvedValue(reference);
    apiMock.readPreview.mockResolvedValue(referencePreview());
    render(<PRDetailModal pr={pull} onClose={() => {}} />);

    fireEvent.click(await screen.findByRole('link', { name: 'deploy' }));

    const dialog = await screen.findByRole('dialog', { name: 'deploy.md' });
    expect(
      await within(dialog).findByText('Deploy the app'),
    ).toBeInTheDocument();
  });

  it('hands timeline links to the browser instead of the dialog', async () => {
    apiMock.gitHubPRDetail.mockResolvedValue(
      detail({
        conversation: [
          {
            id: 2,
            kind: 'comment',
            author: { login: 'ada' },
            body: '[checks](https://example.com/ci)',
            created_at: '2024-05-01T09:45:00Z',
            html_url: 'https://github.com/acme/app/pull/7#issuecomment-2',
          },
        ],
      }),
    );
    render(<PRDetailModal pr={pull} onClose={() => {}} />);

    fireEvent.click(await screen.findByRole('link', { name: 'checks' }));

    await waitFor(() =>
      expect(apiMock.openExternal).toHaveBeenCalledWith(
        'https://example.com/ci',
      ),
    );
    expect(apiMock.resolveTarget).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog', { name: 'checks' })).toBeNull();
  });

  it('keeps the PR page open when Escape closes the dialog', async () => {
    apiMock.gitHubPRDetail.mockResolvedValue(
      detail({ body: '[deploy](docs/deploy.md)' }),
    );
    apiMock.resolveTarget.mockResolvedValue(reference);
    apiMock.readPreview.mockResolvedValue(referencePreview());
    render(<PRDetailModal pr={pull} onClose={() => {}} />);

    fireEvent.click(await screen.findByRole('link', { name: 'deploy' }));
    await screen.findByRole('dialog', { name: 'deploy.md' });

    fireEvent.keyDown(document, { key: 'Escape' });

    await waitFor(() =>
      expect(screen.queryByRole('dialog', { name: 'deploy.md' })).toBeNull(),
    );
    // Escape peels one layer: the PR page it opened from survives.
    expect(screen.getAllByRole('dialog')).toHaveLength(1);
    expect(screen.getByText('Add search')).toBeInTheDocument();
  });

  it('swallows a backdrop click meant for the dialog only', async () => {
    apiMock.gitHubPRDetail.mockResolvedValue(
      detail({ body: '[deploy](docs/deploy.md)' }),
    );
    apiMock.resolveTarget.mockResolvedValue(reference);
    apiMock.readPreview.mockResolvedValue(referencePreview());
    render(<PRDetailModal pr={pull} onClose={() => {}} />);

    fireEvent.click(await screen.findByRole('link', { name: 'deploy' }));
    const dialog = await screen.findByRole('dialog', { name: 'deploy.md' });
    // The dialog's overlay is the box the panel sits in: clicking it
    // dismisses the dialog, and the click must not travel on to the PR
    // page's own backdrop underneath.
    fireEvent.click(dialog.parentElement as HTMLElement);

    await waitFor(() =>
      expect(screen.queryByRole('dialog', { name: 'deploy.md' })).toBeNull(),
    );
    expect(screen.getAllByRole('dialog')).toHaveLength(1);
    expect(screen.getByText('Add search')).toBeInTheDocument();
  });
});
