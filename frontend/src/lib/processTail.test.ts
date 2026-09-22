import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useProcessTail } from './processTail';
import type { SandboxProcess } from './types';

const apiMock = vi.hoisted(() => ({ processes: vi.fn() }));

vi.mock('./api', () => ({ api: apiMock }));

const proc = (over: Partial<SandboxProcess> = {}): SandboxProcess => ({
  process_id: 'p-1',
  argv: ['npm', 'run', 'dev'],
  tty: false,
  pid: 42,
  started_at: '2026-01-01T00:00:00Z',
  running: true,
  tail: 'vite v7 building…\n',
  truncated: false,
  seq: 10,
  ...over,
});

beforeEach(() => {
  vi.useFakeTimers();
  vi.clearAllMocks();
});

afterEach(() => {
  vi.useRealTimers();
});

describe('useProcessTail', () => {
  it('reads once and stops when nothing is running', async () => {
    apiMock.processes.mockResolvedValue([proc({ running: false })]);
    const { result } = renderHook(() => useProcessTail('c-1', false));
    await act(async () => {});
    expect(result.current).toHaveLength(1);
    expect(apiMock.processes).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000);
    });
    expect(apiMock.processes).toHaveBeenCalledTimes(1);
  });

  it('polls a live process and stops after it exits', async () => {
    apiMock.processes.mockResolvedValue([proc()]);
    const { result } = renderHook(() => useProcessTail('c-1', false));
    await act(async () => {});
    expect(apiMock.processes).toHaveBeenCalledTimes(1);

    // A process that outlives its turn is polled at the idle rate.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5_000);
    });
    expect(apiMock.processes).toHaveBeenCalledTimes(2);

    // Once it exits, that read is the last one: nothing is running and
    // no turn is, so the poll loop ends.
    apiMock.processes.mockResolvedValue([
      proc({ running: false, exit_code: 0, exit_reason: 'exited' }),
    ]);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5_000);
    });
    expect(result.current[0].running).toBe(false);
    const settled = apiMock.processes.mock.calls.length;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000);
    });
    expect(apiMock.processes.mock.calls.length).toBe(settled);
  });

  it('polls faster while the conversation runs a turn', async () => {
    apiMock.processes.mockResolvedValue([proc()]);
    renderHook(() => useProcessTail('c-1', true));
    await act(async () => {});
    expect(apiMock.processes).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_500);
    });
    expect(apiMock.processes).toHaveBeenCalledTimes(2);
  });

  it('reads once when the turn ends, catching a process started in the last poll gap', async () => {
    // While the turn runs every read so far came back empty: the
    // session's row landed after the most recent poll.
    apiMock.processes.mockResolvedValue([]);
    const { result, rerender } = renderHook(
      ({ busy }) => useProcessTail('c-1', busy),
      { initialProps: { busy: true } },
    );
    await act(async () => {});
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_500);
    });
    expect(apiMock.processes).toHaveBeenCalledTimes(2);

    // The turn ends. The read that lands with it is the one that must
    // find the row: nothing running is known yet, so without it the
    // poll loop would end here and the card would never show a server
    // that is in fact running.
    apiMock.processes.mockResolvedValue([proc()]);
    rerender({ busy: false });
    await act(async () => {});
    expect(result.current.map((p) => p.process_id)).toEqual(['p-1']);

    // From there the idle pace keeps the tail moving.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5_000);
    });
    expect(apiMock.processes.mock.calls.length).toBeGreaterThan(3);
  });

  it('never shows another conversation’s rows', async () => {
    let answer: (rows: SandboxProcess[]) => void = () => {};
    apiMock.processes.mockImplementation(
      () =>
        new Promise<SandboxProcess[]>((resolve) => {
          answer = resolve;
        }),
    );
    const { result, rerender } = renderHook(
      ({ id }) => useProcessTail(id, true),
      { initialProps: { id: 'c-1' } },
    );
    answer([proc()]);
    await act(async () => {});
    expect(result.current).toHaveLength(1);

    // The switch drops the old rows immediately: the new conversation
    // has not answered yet.
    rerender({ id: 'c-2' });
    expect(result.current).toHaveLength(0);

    answer([proc({ process_id: 'p-2' })]);
    await act(async () => {});
    expect(result.current.map((p) => p.process_id)).toEqual(['p-2']);
  });
});
