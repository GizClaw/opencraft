// The marks hook owns the refresh contract of the viewer's gutter: when
// to read, what to ignore, and when an answer is "the same answer" and
// must not touch state. These cases drive it with a mocked bridge so the
// event, poll and switch paths are all exercised without a browser.
import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { FileTab, GitFileMarks } from '../../lib/types';
import { useFileMarks } from './useFileMarks';

const apiMock = vi.hoisted(() => ({ gitFileMarks: vi.fn() }));
vi.mock('../../lib/api', () => ({ api: apiMock }));

// The bridge is only used for its event stream here, so the mock keeps
// the listeners the hook registers and lets a test fire one.
const bus = vi.hoisted(() => ({
  listeners: [] as ((event: { data: unknown }) => void)[],
}));
vi.mock('@wailsio/runtime', () => ({
  Events: {
    On: (_name: string, listener: (event: { data: unknown }) => void) => {
      bus.listeners.push(listener);
      return () => {
        bus.listeners = bus.listeners.filter((l) => l !== listener);
      };
    },
  },
}));

function emit(data: unknown) {
  for (const listener of [...bus.listeners]) listener({ data });
}

const TAB: FileTab = {
  key: 'k1',
  path: '/tmp/w/internal/a.go',
  rel: 'internal/a.go',
  root: 'workspace',
  name: 'a.go',
  media_type: 'text/plain',
};

function marks(over: Partial<GitFileMarks> = {}): GitFileMarks {
  return {
    in_repo: true,
    path: 'internal/a.go',
    kind: 'modified',
    staged: false,
    unstaged: true,
    untracked: false,
    unmerged: false,
    binary: false,
    truncated: false,
    additions: 1,
    deletions: 1,
    mtime_ns: 10,
    size: 20,
    mods: [{ start: 2, count: 1 }],
    ...over,
  };
}

beforeEach(() => {
  apiMock.gitFileMarks.mockReset();
  bus.listeners = [];
  vi.useFakeTimers({ shouldAdvanceTime: true });
});

afterEach(() => {
  vi.useRealTimers();
});

describe('useFileMarks', () => {
  it('reads the tab on mount and returns its marks', async () => {
    apiMock.gitFileMarks.mockResolvedValue(marks());
    const { result } = renderHook(() => useFileMarks(TAB, true));
    await waitFor(() => expect(result.current).toEqual(marks()));
    expect(apiMock.gitFileMarks).toHaveBeenCalledWith('/tmp/w/internal/a.go');
  });

  it('never asks for a file outside the workspace root', async () => {
    const skillTab = { ...TAB, root: 'skill', path: '/tmp/skill/SKILL.md' };
    const { result } = renderHook(() => useFileMarks(skillTab, true));
    await act(async () => {});
    expect(apiMock.gitFileMarks).not.toHaveBeenCalled();
    expect(result.current).toBeNull();
  });

  it('stays silent while the switch is off', async () => {
    const { result } = renderHook(() => useFileMarks(TAB, false));
    await act(async () => {});
    expect(apiMock.gitFileMarks).not.toHaveBeenCalled();
    expect(result.current).toBeNull();
  });

  it('keeps the previous object when the poll answers the same', async () => {
    apiMock.gitFileMarks.mockResolvedValue(marks());
    const { result } = renderHook(() => useFileMarks(TAB, true));
    await waitFor(() => expect(result.current).not.toBeNull());
    const first = result.current;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(apiMock.gitFileMarks).toHaveBeenCalledTimes(2);
    expect(result.current).toBe(first);
  });

  it('re-reads after the events that can change a file', async () => {
    apiMock.gitFileMarks.mockResolvedValue(marks());
    renderHook(() => useFileMarks(TAB, true));
    await waitFor(() => expect(apiMock.gitFileMarks).toHaveBeenCalledTimes(1));
    await act(async () => {
      emit({ type: 'git_changed' });
      await vi.advanceTimersByTimeAsync(200);
    });
    expect(apiMock.gitFileMarks).toHaveBeenCalledTimes(2);
    // A turn end carries no payload: every open tab is stale.
    await act(async () => {
      emit({ type: 'turn_end' });
      await vi.advanceTimersByTimeAsync(200);
    });
    expect(apiMock.gitFileMarks).toHaveBeenCalledTimes(3);
  });

  it('follows artifact events for its own file only', async () => {
    apiMock.gitFileMarks.mockResolvedValue(marks());
    renderHook(() => useFileMarks(TAB, true));
    await waitFor(() => expect(apiMock.gitFileMarks).toHaveBeenCalledTimes(1));
    await act(async () => {
      emit({ type: 'artifact', data: { path: 'internal/other.go' } });
      await vi.advanceTimersByTimeAsync(200);
    });
    expect(apiMock.gitFileMarks).toHaveBeenCalledTimes(1);
    await act(async () => {
      emit({ type: 'artifact', data: { path: 'internal/a.go' } });
      await vi.advanceTimersByTimeAsync(200);
    });
    expect(apiMock.gitFileMarks).toHaveBeenCalledTimes(2);
  });

  it('drops the marks when the read fails', async () => {
    apiMock.gitFileMarks.mockResolvedValueOnce(marks());
    const { result } = renderHook(() => useFileMarks(TAB, true));
    await waitFor(() => expect(result.current).not.toBeNull());
    apiMock.gitFileMarks.mockRejectedValueOnce(new Error('git failed'));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(result.current).toBeNull();
  });

  it('clears the old marks and reads the new file on a tab switch', async () => {
    apiMock.gitFileMarks.mockResolvedValue(marks());
    const { result, rerender } = renderHook(
      ({ tab }: { tab: FileTab }) => useFileMarks(tab, true),
      { initialProps: { tab: TAB } },
    );
    await waitFor(() => expect(result.current).not.toBeNull());
    apiMock.gitFileMarks.mockResolvedValue(
      marks({ path: 'internal/b.go', mtime_ns: 11 }),
    );
    rerender({
      tab: {
        ...TAB,
        key: 'k2',
        path: '/tmp/w/internal/b.go',
        rel: 'internal/b.go',
        name: 'b.go',
      },
    });
    // The stale payload must not survive the switch for even one commit.
    expect(result.current).toBeNull();
    await waitFor(() => expect(result.current?.path).toBe('internal/b.go'));
    expect(apiMock.gitFileMarks).toHaveBeenLastCalledWith(
      '/tmp/w/internal/b.go',
    );
  });
});
