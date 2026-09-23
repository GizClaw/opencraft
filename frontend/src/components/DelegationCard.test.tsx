import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { DelegationCard } from './DelegationCard';
import type { DelegationState } from '../lib/types';

const apiMock = vi.hoisted(() => ({
  delegationState: vi.fn(),
  saveDelegationSettings: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

const state: DelegationState = {
  max_concurrency: 4,
  max_depth: 8,
  allowed_targets: ['researcher'],
  blocked_targets: [],
  min_max_concurrency: 1,
  max_max_concurrency: 16,
  default_max_concurrency: 4,
  min_max_depth: 1,
  max_max_depth: 16,
  default_max_depth: 8,
  max_targets: 64,
  targets: ['assistant', 'researcher', 'writer-two'],
  targets_available: true,
};

describe('DelegationCard', () => {
  beforeEach(() => {
    apiMock.delegationState.mockReset();
    apiMock.saveDelegationSettings.mockReset();
    apiMock.delegationState.mockResolvedValue(state);
    apiMock.saveDelegationSettings.mockResolvedValue(undefined);
  });

  it('summarises the effective limits on the list item', async () => {
    render(<DelegationCard />);
    await screen.findByText('Delegation');
    expect(screen.getByText('4 × 8')).toBeInTheDocument();
  });

  it('saves the edited limits and target lists', async () => {
    const user = userEvent.setup();
    render(<DelegationCard />);
    await screen.findByText('Delegation');

    await user.click(screen.getByText('Delegation'));
    const dialog = await screen.findByRole('dialog');

    const concurrency = screen.getByLabelText('Concurrent delegations');
    await user.clear(concurrency);
    await user.type(concurrency, '8');

    // A registered target can be added from the suggestion row...
    await user.click(
      within(dialog).getByRole('button', { name: 'Allow writer-two' }),
    );
    // ...and a pattern can be typed into the blocked list by hand.
    const addFields = screen.getAllByLabelText('Add');
    await user.type(addFields[1], 'danger*{Enter}');
    await user.click(
      within(dialog).getByRole('button', { name: 'Save & apply' }),
    );

    await waitFor(() =>
      expect(apiMock.saveDelegationSettings).toHaveBeenCalledWith({
        max_concurrency: 8,
        max_depth: 8,
        allowed_targets: ['researcher', 'writer-two'],
        blocked_targets: ['danger*'],
      }),
    );
  });

  it('removes a target with its chip button', async () => {
    const user = userEvent.setup();
    render(<DelegationCard />);
    await screen.findByText('Delegation');
    await user.click(screen.getByText('Delegation'));

    const dialog = await screen.findByRole('dialog');
    await user.click(
      within(dialog).getByRole('button', { name: 'Remove researcher' }),
    );
    await user.click(
      within(dialog).getByRole('button', { name: 'Save & apply' }),
    );

    await waitFor(() =>
      expect(apiMock.saveDelegationSettings).toHaveBeenCalledWith(
        expect.objectContaining({ allowed_targets: [] }),
      ),
    );
  });
});
