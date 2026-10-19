// conv/failure.ts: how a terminal status and an interrupt cause read
// to the user.
import { describe, expect, it } from 'vitest';
import { friendlyFailure, friendlyInterruption, isUserStop } from './failure';

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

describe('friendly failure text', () => {
  it('friendlyFailure maps the error kind the backend classified', () => {
    for (const kind of [
      'provider_failure',
      'invalid_provider_response',
      'unknown_model',
      'invalid_request',
      'timeout',
    ]) {
      const friendly = friendlyFailure(kind);
      expect(friendly).toBeTruthy();
      // The copy never leaks the enum or the graph wiring.
      expect(friendly ?? '').not.toContain(kind);
      expect(friendly ?? '').not.toContain('graph "');
    }
    // A failure the engine did not classify keeps the raw error, which
    // is what the notice falls back to.
    expect(friendlyFailure('')).toBeNull();
    expect(friendlyFailure(undefined)).toBeNull();
    expect(friendlyFailure('some_future_kind')).toBeTruthy();
  });

  it('friendlyInterruption maps the interrupt cause, not the text', () => {
    expect(friendlyInterruption('user_cancel')).toBeTruthy();
    expect(friendlyInterruption('host_shutdown')).toBeTruthy();
    expect(friendlyInterruption('user_input')).toBeTruthy();
    // An unknown cause still reads as an interruption; no cause at all
    // means the turn did not end as one.
    expect(friendlyInterruption('custom')).toBeTruthy();
    expect(friendlyInterruption('')).toBeNull();
    expect(friendlyInterruption(undefined)).toBeNull();
  });
});
