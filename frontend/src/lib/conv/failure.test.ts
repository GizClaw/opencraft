import { describe, expect, it } from 'vitest';
import { isUserStop } from './failure';

describe('isUserStop', () => {
  it('treats canceled turns as user stops unless a deadline ended them', () => {
    expect(isUserStop('canceled', 'user_cancel')).toBe(true);
    expect(isUserStop('canceled')).toBe(true);
    // The engine reports a deadline and a user stop as the same
    // `canceled` status; the error kind decides, so a deadline keeps
    // its diagnostics and never reads as something the user did.
    expect(isUserStop('canceled', undefined, 'timeout')).toBe(false);
    expect(isUserStop('canceled', '', 'timeout')).toBe(false);
  });

  it('treats user_cancel and user_input interruptions as user stops', () => {
    expect(isUserStop('interrupted', 'user_cancel')).toBe(true);
    expect(isUserStop('interrupted', 'user_input')).toBe(true);
  });

  it('keeps non-user interruptions and failures out of the user-stop bucket', () => {
    expect(isUserStop('interrupted', 'host_shutdown')).toBe(false);
    expect(isUserStop('interrupted', 'custom')).toBe(false);
    expect(isUserStop('interrupted', '')).toBe(false);
    expect(isUserStop('interrupted')).toBe(false);
    expect(isUserStop('failed', 'user_cancel')).toBe(false);
    expect(isUserStop('aborted', 'user_cancel')).toBe(false);
  });
});
