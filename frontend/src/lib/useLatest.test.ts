// The guard's whole job is to answer "is this still mine" — first answer
// wins for the first ticket, a later ticket retires it, and unmount retires
// everything. These cases pin those three, because the failure mode they
// prevent (a slow response overwriting a newer selection) is invisible in
// a test that only ever fires one request.
import { act, renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { useLatestRequest } from './useLatest';

describe('useLatestRequest', () => {
  it('keeps the newest ticket and retires the ones before it', () => {
    const { result } = renderHook(() => useLatestRequest());
    const first = result.current.begin();
    expect(result.current.isCurrent(first)).toBe(true);

    const second = result.current.begin();
    expect(result.current.current()).toBe(second);
    expect(result.current.isCurrent(first)).toBe(false);
    expect(result.current.isCurrent(second)).toBe(true);
  });

  it('retires every ticket once the component unmounts', () => {
    const { result, unmount } = renderHook(() => useLatestRequest());
    const ticket = result.current.begin();
    expect(result.current.isCurrent(ticket)).toBe(true);

    unmount();

    // A response that lands after the component is gone must not write,
    // schedule a timer or setState behind its back.
    expect(result.current.isCurrent(ticket)).toBe(false);
  });

  it('treats a ticket the guard never issued as stale', () => {
    const { result } = renderHook(() => useLatestRequest());
    act(() => {
      result.current.begin();
    });
    expect(result.current.isCurrent(0)).toBe(false);
    expect(result.current.isCurrent(99)).toBe(false);
  });
});
