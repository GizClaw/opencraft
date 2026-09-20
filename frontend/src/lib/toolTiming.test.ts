import { describe, expect, it } from 'vitest';
import { toolDurationMs, workedForLabel } from './toolTiming';

describe('workedForLabel', () => {
  it('formats a span from seconds up to hours', () => {
    expect(workedForLabel(0)).toBe('<1s');
    expect(workedForLabel(999)).toBe('<1s');
    expect(workedForLabel(1000)).toBe('1s');
    expect(workedForLabel(65_000)).toBe('1m 5s');
    expect(workedForLabel(3_600_000)).toBe('1h 0m 0s');
    expect(workedForLabel(3_725_000)).toBe('1h 2m 5s');
  });

  it('renders nothing without a real span', () => {
    expect(workedForLabel(undefined)).toBe('');
    expect(workedForLabel(Number.NaN)).toBe('');
    expect(workedForLabel(-1)).toBe('');
  });
});

describe('toolDurationMs', () => {
  it('measures a running call from when this client saw it', () => {
    expect(toolDurationMs({ status: 'running', seenAt: 1000 }, 4500)).toBe(
      3500,
    );
  });

  it('freezes at the span between the call and its result', () => {
    expect(
      toolDurationMs({ status: 'done', seenAt: 1000, endedAt: 2500 }, 99_999),
    ).toBe(1500);
  });

  it('never reports a negative span', () => {
    expect(
      toolDurationMs({ status: 'done', seenAt: 2000, endedAt: 1000 }, 3000),
    ).toBe(0);
    expect(toolDurationMs({ status: 'running', seenAt: 5000 }, 4000)).toBe(0);
  });

  it('reports nothing for a call this client never saw start', () => {
    expect(toolDurationMs({ status: 'done' }, 1000)).toBeUndefined();
  });

  it('reports nothing for a finished call without a result stamp', () => {
    expect(
      toolDurationMs({ status: 'done', seenAt: 1000 }, 5000),
    ).toBeUndefined();
  });
});
