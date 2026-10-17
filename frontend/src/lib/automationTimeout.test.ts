import { describe, expect, it } from 'vitest';
import {
  MAX_AUTOMATION_TIMEOUT_MINUTES,
  timeoutDurationFromMinutes,
  timeoutFromForm,
  timeoutMinutesFromDuration,
} from './automationTimeout';

describe('timeoutMinutesFromDuration', () => {
  it('reads the shapes the scheduler stores', () => {
    expect(timeoutMinutesFromDuration('15m')).toBe('15');
    expect(timeoutMinutesFromDuration('2h')).toBe('120');
    expect(timeoutMinutesFromDuration('1h30m')).toBe('90');
    // Go's fully folded spelling reads like any other.
    expect(timeoutMinutesFromDuration('1h0m0s')).toBe('60');
    expect(timeoutMinutesFromDuration('90s')).toBe('2');
  });

  it('treats unset and unusable values as the default', () => {
    expect(timeoutMinutesFromDuration('')).toBe('');
    expect(timeoutMinutesFromDuration(undefined)).toBe('');
    expect(timeoutMinutesFromDuration('15')).toBe('');
    expect(timeoutMinutesFromDuration('0s')).toBe('');
    // A duration the minutes field cannot state reads as '' as well; the
    // form shows the stored text in that case instead of an empty field.
    expect(timeoutMinutesFromDuration('1.5h')).toBe('');
    expect(timeoutMinutesFromDuration('40ms')).toBe('');
  });
});

describe('timeoutDurationFromMinutes', () => {
  it('renders whole minutes as a Go duration', () => {
    expect(timeoutDurationFromMinutes('')).toBe('');
    expect(timeoutDurationFromMinutes('15')).toBe('15m');
    expect(timeoutDurationFromMinutes('90')).toBe('90m');
    expect(
      timeoutDurationFromMinutes(String(MAX_AUTOMATION_TIMEOUT_MINUTES)),
    ).toBe('1440m');
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

describe('timeoutFromForm', () => {
  it('carries a stored duration through a form nobody edited', () => {
    // The whole point: a bound the minutes field cannot state survives a
    // rename. Reading it as the empty default turned a 90-minute run
    // limit into 15 minutes on the next save.
    expect(
      timeoutFromForm({
        timeoutDuration: '1.5h',
        timeoutMinutes: '',
        timeoutEdited: false,
      }),
    ).toBe('1.5h');
    expect(
      timeoutFromForm({
        timeoutDuration: '40ms',
        timeoutMinutes: '',
        timeoutEdited: false,
      }),
    ).toBe('40ms');
    // A bound the field can state is still written back as it was stored.
    expect(
      timeoutFromForm({
        timeoutDuration: '2h',
        timeoutMinutes: '120',
        timeoutEdited: false,
      }),
    ).toBe('2h');
  });

  it('keeps an unset bound unset', () => {
    expect(
      timeoutFromForm({
        timeoutDuration: '',
        timeoutMinutes: '',
        timeoutEdited: false,
      }),
    ).toBe('');
  });

  it('writes the field back once it has been edited', () => {
    expect(
      timeoutFromForm({
        timeoutDuration: '1.5h',
        timeoutMinutes: '30',
        timeoutEdited: true,
      }),
    ).toBe('30m');
    // A cleared field means the default, which is what the hint promises.
    expect(
      timeoutFromForm({
        timeoutDuration: '2h',
        timeoutMinutes: '',
        timeoutEdited: true,
      }),
    ).toBe('');
  });

  it('refuses edited minutes the backend would reject', () => {
    expect(
      timeoutFromForm({
        timeoutDuration: '2h',
        timeoutMinutes: '0',
        timeoutEdited: true,
      }),
    ).toBeUndefined();
    expect(
      timeoutFromForm({
        timeoutDuration: '2h',
        timeoutMinutes: '2.5',
        timeoutEdited: true,
      }),
    ).toBeUndefined();
  });
});
