import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { mountOutsideAct, pressEscape } from '../test/outsideAct';
import type { GitCommitFiles, GitDiff, GitLogEntry } from '../lib/types';
import { CommitDetailModal } from './CommitDetailModal';

const apiMock = vi.hoisted(() => ({
  gitCommitFiles: vi.fn(),
  gitCommitDiff: vi.fn(),
  gitDiff: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

const entry: GitLogEntry = {
  oid: 'abc123',
  short_oid: 'abc123',
  author: 'Alice',
  date: '2026-09-08T00:00:00Z',
  subject: 'feat: add panel',
};

const files: GitCommitFiles = {
  files: [
    {
      path: 'internal/a.go',
      kind: 'modified',
      additions: 1,
      deletions: 1,
      is_binary: false,
    },
  ],
  truncated: false,
};

const diff: GitDiff = {
  content:
    'diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1,2 @@\n-old line\n+new line\n',
  truncated: false,
};

describe('CommitDetailModal', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMock.gitCommitFiles.mockResolvedValue(files);
    apiMock.gitCommitDiff.mockResolvedValue(diff);
  });

  it('lists the commit files and closes on Escape', async () => {
    const onClose = vi.fn();
    render(<CommitDetailModal entry={entry} onClose={onClose} />);

    expect(screen.getByText('feat: add panel')).toBeInTheDocument();
    expect(await screen.findByText('internal/a.go')).toBeInTheDocument();
    expect(await screen.findByText('new line')).toBeInTheDocument();

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('owns Escape from the commit that shows the dialog', async () => {
    const onClose = vi.fn();
    const mount = mountOutsideAct(
      <CommitDetailModal entry={entry} onClose={onClose} />,
    );
    try {
      await mount.waitForDom(
        () => mount.container.querySelector('[role="dialog"]') !== null,
      );
      pressEscape();
      // Registered with useEffect, the dialog is on screen while Escape
      // still belongs to whatever is underneath (the Git panel), so this
      // call lands on a dialog that cannot hear it.
      expect(onClose).toHaveBeenCalledTimes(1);
    } finally {
      await mount.unmount();
    }
  });

  it('navigates between files with the arrow keys', async () => {
    apiMock.gitCommitFiles.mockResolvedValue({
      files: [
        files.files[0],
        {
          path: 'internal/b.go',
          kind: 'added',
          additions: 2,
          deletions: 0,
          is_binary: false,
        },
      ],
      truncated: false,
    });
    render(<CommitDetailModal entry={entry} onClose={() => {}} />);
    await screen.findByText('internal/a.go');

    fireEvent.keyDown(window, { key: 'ArrowDown' });

    await waitFor(() =>
      expect(apiMock.gitCommitDiff).toHaveBeenCalledWith(
        'abc123',
        'internal/b.go',
      ),
    );
  });
});
