import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { PerfProbeCard } from './PerfProbeCard';

const apiMock = vi.hoisted(() => ({
  perfProbe: vi.fn(),
  setPerfProbe: vi.fn(),
}));
const probeMock = vi.hoisted(() => ({
  setPerfProbeEnabled: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));
vi.mock('../lib/perfProbe', () => probeMock);

beforeEach(() => {
  vi.clearAllMocks();
});

describe('PerfProbeCard', () => {
  it('shows the persisted switch and stops the sampler when flipped off', async () => {
    apiMock.perfProbe.mockResolvedValue(true);
    apiMock.setPerfProbe.mockResolvedValue(undefined);

    render(<PerfProbeCard />);

    const sample = await screen.findByRole('button', { name: 'Sample' });
    expect(sample).toHaveAttribute('aria-pressed', 'true');

    fireEvent.click(screen.getByRole('button', { name: 'Stop' }));

    await waitFor(() =>
      expect(apiMock.setPerfProbe).toHaveBeenCalledWith(false),
    );
    expect(probeMock.setPerfProbeEnabled).toHaveBeenCalledWith(false);
    expect(screen.getByRole('button', { name: 'Stop' })).toHaveAttribute(
      'aria-pressed',
      'true',
    );
  });

  it('keeps the switch where it was when the host rejects the change', async () => {
    apiMock.perfProbe.mockResolvedValue(true);
    apiMock.setPerfProbe.mockRejectedValue(new Error('save failed'));

    render(<PerfProbeCard />);

    fireEvent.click(await screen.findByRole('button', { name: 'Stop' }));

    expect(await screen.findByText(/save failed/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Sample' })).toHaveAttribute(
      'aria-pressed',
      'true',
    );
    expect(probeMock.setPerfProbeEnabled).not.toHaveBeenCalled();
  });
});
