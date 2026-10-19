// conv/failure.ts: how a terminal status and an interrupt cause read
// to the user.
import { describe, expect, it } from 'vitest';
import {
  friendlyFailure,
  friendlyInterruption,
  isUserStop,
  turnNeedsAttention,
} from './failure';

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

describe('turnNeedsAttention', () => {
  it('marks the endings the reader has to come back to', () => {
    expect(turnNeedsAttention('failed')).toBe(true);
    expect(turnNeedsAttention('aborted')).toBe(true);
    // A deadline and a user stop share the wire status; the error kind
    // is the only thing that tells them apart, and a cut-off turn is
    // worth opening.
    expect(turnNeedsAttention('canceled', 'timeout')).toBe(true);
    // Everything the client cannot name is a failure on the transcript
    // side too (eventRouter's turnEndStatus), so the row follows it.
    expect(turnNeedsAttention('')).toBe(true);
    expect(turnNeedsAttention(undefined)).toBe(true);
    expect(turnNeedsAttention('some_future_status')).toBe(true);
  });

  it('leaves the endings the reader already knows alone', () => {
    expect(turnNeedsAttention('completed')).toBe(false);
    // A stop is the reader's own click, and a barge-in delivers its own
    // text: neither is news by the time the turn settles.
    expect(turnNeedsAttention('canceled', 'user_cancel')).toBe(false);
    expect(turnNeedsAttention('canceled', '')).toBe(false);
    // The host was shutting down or the app restarted: the transcript
    // says so where the turn sits, and there is nothing to open.
    expect(turnNeedsAttention('interrupted', 'host_shutdown')).toBe(false);
    expect(turnNeedsAttention('interrupted', 'app_restart')).toBe(false);
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
