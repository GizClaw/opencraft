import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ExecPoolCard } from './ExecPoolCard';
import type { ExecPool } from '../lib/types';

const apiMock = vi.hoisted(() => ({
  execPool: vi.fn(),
  setExecPool: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

const POOL: ExecPool = {
  prewarm: 1,
  maxIdle: 4,
  maxActive: 16,
  idleMinutes: 5,
  idle: 1,
  active: 0,
};

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  vi.useRealTimers();
});

describe('ExecPoolCard', () => {
  it('renders the knobs and the live child counts', async () => {
    apiMock.execPool.mockResolvedValue(POOL);

    render(<ExecPoolCard />);

    expect(await screen.findByLabelText('Pre-warm')).toHaveValue(1);
    expect(screen.getByLabelText('Max idle')).toHaveValue(4);
    expect(screen.getByLabelText('Max active')).toHaveValue(16);
    expect(screen.getByLabelText('Idle minutes')).toHaveValue(5);
    expect(screen.getByText('1 idle · 0 active')).toBeInTheDocument();
    expect(screen.getByText(/Ranges:/)).toBeInTheDocument();
  });

  it('saves the edited knobs through the binding and adopts the answer', async () => {
    apiMock.execPool.mockResolvedValue(POOL);
    apiMock.setExecPool.mockImplementation(
      async (
        prewarm: number,
        maxIdle: number,
        maxActive: number,
        idleMinutes: number,
      ) => ({
        prewarm,
        maxIdle,
        maxActive,
        idleMinutes,
        idle: 0,
        active: 1,
      }),
    );

    render(<ExecPoolCard />);
    const prewarm = await screen.findByLabelText('Pre-warm');
    fireEvent.change(prewarm, { target: { value: '3' } });
    expect(screen.getByText('Unsaved changes')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() =>
      expect(apiMock.setExecPool).toHaveBeenCalledWith(3, 4, 16, 5),
    );
    expect(await screen.findByDisplayValue('3')).toBeInTheDocument();
    expect(screen.getByText('0 idle · 1 active')).toBeInTheDocument();
  });

  it('refreshes the live counts without clobbering the draft', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    apiMock.execPool
      .mockResolvedValueOnce(POOL)
      .mockResolvedValue({ ...POOL, idle: 2, active: 3 });

    render(<ExecPoolCard />);
    expect(await screen.findByText('1 idle · 0 active')).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('Pre-warm'), {
      target: { value: '7' },
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });

    expect(await screen.findByText('2 idle · 3 active')).toBeInTheDocument();
    // The timer only refreshes the counters: the pending edit survives.
    expect(screen.getByLabelText('Pre-warm')).toHaveValue(7);
    expect(screen.getByText('Unsaved changes')).toBeInTheDocument();
  });
});
