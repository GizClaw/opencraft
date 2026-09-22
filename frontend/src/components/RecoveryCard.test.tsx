import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { RecoveryCard } from './RecoveryCard';
import { formatDateTime } from '../lib/datetime';
import type { Recovery } from '../lib/types';

const apiMock = vi.hoisted(() => ({
  recovery: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

const REPORT: Recovery = {
  workspace: '/Users/me/projects/opencraft',
  ran: true,
  at: '2026-09-21T10:00:00Z',
  recovered: 2,
  archived: 1,
  discarded: 0,
  skipped_live: 3,
  failed: 0,
  pending: 0,
  checkpoint_rows: 5,
  checkpoint_runs: 4,
  checkpoint_bytes: 4096,
};

beforeEach(() => {
  vi.clearAllMocks();
});

describe('RecoveryCard', () => {
  it('renders the last pass and the checkpoint table', async () => {
    apiMock.recovery.mockResolvedValue(REPORT);

    render(<RecoveryCard />);

    expect(await screen.findByText('Recovered')).toBeInTheDocument();
    expect(
      screen.getByText(
        `Last pass ${formatDateTime(REPORT.at ?? '')} · recovered 2`,
      ),
    ).toBeInTheDocument();
    // Every counter the pass keeps is shown, including the ones that mean
    // work was left on the table.
    expect(screen.getByText('Already archived')).toBeInTheDocument();
    expect(screen.getByText('Discarded')).toBeInTheDocument();
    expect(screen.getByText('Skipped, still running')).toBeInTheDocument();
    expect(screen.getByText('Failed, retried next pass')).toBeInTheDocument();
    expect(screen.getByText('Not examined')).toBeInTheDocument();
    expect(
      screen.getByText(
        'Checkpoints: 5 rows, 4 of them run checkpoints · 4.0 KB',
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText('/Users/me/projects/opencraft'),
    ).toBeInTheDocument();
  });

  it('says so when no pass has run yet', async () => {
    apiMock.recovery.mockResolvedValue({
      ...REPORT,
      ran: false,
      at: undefined,
      recovered: 0,
      archived: 0,
      skipped_live: 0,
    });

    render(<RecoveryCard />);

    expect(
      await screen.findByText('This process has not run a recovery pass yet.'),
    ).toBeInTheDocument();
    // The checkpoint counts still describe the open store, so they stay
    // on screen next to the missing pass.
    expect(
      screen.getByText(
        'Checkpoints: 5 rows, 4 of them run checkpoints · 4.0 KB',
      ),
    ).toBeInTheDocument();
  });

  it('names the live process holding the workspace', async () => {
    apiMock.recovery.mockResolvedValue({
      ...REPORT,
      ran: false,
      workspace_holder: 'pid 4242 (gui)',
    });

    render(<RecoveryCard />);

    expect(
      await screen.findByText(
        'Another live process owns this workspace (pid 4242 (gui)); this process ran no recovery.',
      ),
    ).toBeInTheDocument();
  });

  it('reports a binding failure instead of a clean bill of health', async () => {
    apiMock.recovery.mockRejectedValue(new Error('store busy'));

    render(<RecoveryCard />);

    expect(await screen.findByText(/store busy/)).toBeInTheDocument();
  });
});
