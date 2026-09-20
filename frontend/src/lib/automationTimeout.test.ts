import { describe, expect, it } from 'vitest';
import {
  MAX_AUTOMATION_TIMEOUT_MINUTES,
  timeoutDurationFromMinutes,
  timeoutMinutesFromDuration,
} from './automationTimeout';

describe('timeoutMinutesFromDuration', () => {
  it('reads the shapes the scheduler stores', () => {
    expect(timeoutMinutesFromDuration('15m')).toBe('15');
    expect(timeoutMinutesFromDuration('2h')).toBe('120');
    expect(timeoutMinutesFromDuration('1h30m')).toBe('90');
    expect(timeoutMinutesFromDuration('90s')).toBe('2');
  });

  it('treats unset and unusable values as the default', () => {
    expect(timeoutMinutesFromDuration('')).toBe('');
    expect(timeoutMinutesFromDuration(undefined)).toBe('');
    expect(timeoutMinutesFromDuration('15')).toBe('');
    expect(timeoutMinutesFromDuration('0s')).toBe('');
  });
});

describe('timeoutDurationFromMinutes', () => {
  it('renders whole minutes as a Go duration', () => {
    expect(timeoutDurationFromMinutes('')).toBe('');
    expect(timeoutDurationFromMinutes('15')).toBe('15m');
    expect(timeoutDurationFromMinutes('90')).toBe('90m');
    expect(timeoutDurationFromMinutes(String(MAX_AUTOMATION_TIMEOUT_MINUTES)))
      .toBe('1440m');
  });

  it('refuses values the backend would reject', () => {
    expect(timeoutDurationFromMinutes('0')).toBeUndefined();
    expect(timeoutDurationFromMinutes('-5')).toBeUndefined();
    expect(timeoutDurationFromMinutes('2.5')).toBeUndefined();
    expect(timeoutDurationFromMinutes('abc')).toBeUndefined();
    expect(
      timeoutDurationFromMinutes(String(MAX_AUTOMATION_TIMEOUT_MINUTES + 1)),
    ).toBeUndefined();
  });
});
