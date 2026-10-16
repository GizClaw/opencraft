import { act, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { GitHubPull } from '../lib/types';
import { PRView } from './PRView';

const apiMock = vi.hoisted(() => ({
  gitHubPRList: vi.fn(),
  gitHubPRDetail: vi.fn(),
  openExternal: vi.fn(),
  resolveTarget: vi.fn(),
  readPreview: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

function pull(number: number, title: string): GitHubPull {
  return {
    number,
    title,
    state: 'open',
    draft: false,
    author: { login: 'ada' },
    base: 'main',
    head: 'search',
    updated_at: '2024-05-01T10:00:00Z',
    html_url: `https://github.com/acme/app/pull/${number}`,
  };
}

describe('PRView', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('lists the pull requests of the repository', async () => {
    apiMock.gitHubPRList.mockResolvedValue([pull(7, 'ship the panel')]);

    render(<PRView nonce={0} />);

    expect(await screen.findByText('ship the panel')).toBeInTheDocument();
    expect(screen.getByText('#7')).toBeInTheDocument();
  });

  it('keeps the newest list when an older refresh answers late', async () => {
    // The nonce moves on every git_changed/turn_end/focus, so two list
    // loads can overlap; the slower one must not win.
    let answerFirst: (rows: GitHubPull[]) => void = () => {};
    apiMock.gitHubPRList
      .mockImplementationOnce(
        () =>
          new Promise<GitHubPull[]>((resolve) => {
            answerFirst = resolve;
          }),
      )
      .mockResolvedValueOnce([pull(2, 'newer title')]);

    const { rerender } = render(<PRView nonce={0} />);
    await waitFor(() => expect(apiMock.gitHubPRList).toHaveBeenCalledTimes(1));

    rerender(<PRView nonce={1} />);
    expect(await screen.findByText('newer title')).toBeInTheDocument();

    await act(async () => {
      answerFirst([pull(1, 'stale title')]);
    });
    expect(screen.queryByText('stale title')).not.toBeInTheDocument();
    expect(screen.getByText('newer title')).toBeInTheDocument();
  });
});
