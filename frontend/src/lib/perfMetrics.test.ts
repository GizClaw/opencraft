import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  beginMarkdownRender,
  drainInteractions,
  endMarkdownRender,
  flushCommitStats,
  markdownStats,
  measureInteraction,
  scheduleFlushCommit,
  setPerfMetricsEnabled,
} from './perfMetrics';

// frames replaces animation frames with a queue a test fires by hand:
// jsdom does not run them, and when a measurement lands is the whole point
// of the commit number.
function frames() {
  const queued: Array<() => void> = [];
  vi.stubGlobal('requestAnimationFrame', (cb: () => void) => {
    queued.push(cb);
    return queued.length;
  });
  const fire = () => {
    const next = queued.shift();
    if (!next) throw new Error('no frame was scheduled');
    next();
  };
  return { queued, fire };
}

// setVisibility stubs what the page reports and announces the change the
// way the engine does: the module counts a hide so a measurement spanning
// one can be dropped.
function setVisibility(state: 'visible' | 'hidden') {
  Object.defineProperty(document, 'visibilityState', {
    configurable: true,
    get: () => state,
  });
  document.dispatchEvent(new Event('visibilitychange'));
}

let clock = 0;
let nowSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
  clock = 1_000;
  nowSpy = vi
    .spyOn(performance, 'now')
    .mockImplementation(() => clock) as ReturnType<typeof vi.spyOn>;
});

afterEach(() => {
  setPerfMetricsEnabled(false);
  nowSpy.mockRestore();
  // The stub is an own property; removing it restores jsdom's getter.
  delete (document as unknown as { visibilityState?: string }).visibilityState;
  vi.unstubAllGlobals();
});

describe('perfMetrics', () => {
  it('records nothing until the probe starts sampling', async () => {
    const frame = frames();
    setVisibility('visible');
    scheduleFlushCommit(clock);
    expect(frame.queued).toHaveLength(0);

    // The interaction wrapper is a passthrough when the sampler is off,
    // not a deferral: the action still runs before the promise is built.
    let ran = false;
    const value = await measureInteraction('send', () => {
      ran = true;
      return 7;
    });
    expect(ran).toBe(true);
    expect(value).toBe(7);
    expect(flushCommitStats().max).toBe(0);
    expect(drainInteractions().count).toBe(0);
  });

  it('times a flush to the frame that follows it', () => {
    const frame = frames();
    setVisibility('visible');
    setPerfMetricsEnabled(true);

    scheduleFlushCommit(clock);
    expect(frame.queued).toHaveLength(1);
    clock += 18;
    frame.fire();

    expect(flushCommitStats()).toEqual({ p50: 18, p95: 18, max: 18 });
  });

  it('drops a frame that arrived past the suspension gap', () => {
    const frame = frames();
    setVisibility('visible');
    setPerfMetricsEnabled(true);

    scheduleFlushCommit(clock);
    // A frame five seconds later is not a slow frame; it is a window that
    // was occluded or asleep (see the frame sampler's dropped_gaps).
    clock += 5_000;
    frame.fire();

    expect(flushCommitStats().max).toBe(0);
  });

  it('drops an interaction that spans the window hiding', async () => {
    const frame = frames();
    setVisibility('visible');
    setPerfMetricsEnabled(true);

    const interaction = measureInteraction('send', () => {
      clock += 5;
    });
    setVisibility('hidden');
    setVisibility('visible');
    await interaction;
    clock += 5;
    frame.fire();

    expect(frame.queued).toHaveLength(0);
    expect(drainInteractions().count).toBe(0);
  });

  it('keeps the slowest interaction of the window and the count', async () => {
    const frame = frames();
    setVisibility('visible');
    setPerfMetricsEnabled(true);

    const first = measureInteraction('resume', async () => {
      clock += 30;
    });
    await first;
    const second = measureInteraction('settings-save', () => {
      clock += 5;
    });
    await second;
    clock += 10;
    frame.fire();
    clock += 10;
    frame.fire();

    expect(drainInteractions()).toEqual({
      max: 45,
      name: 'resume',
      count: 2,
    });
    // Draining clears the window: nothing is reported twice.
    expect(drainInteractions().count).toBe(0);
  });

  it('names the window by its first interaction even at zero', async () => {
    const frame = frames();
    setVisibility('visible');
    setPerfMetricsEnabled(true);

    await measureInteraction('send', () => {});
    frame.fire();

    // A frame in the same instant is a real interaction, not a missing
    // one: the label is what makes the sample readable.
    expect(drainInteractions()).toEqual({ max: 0, name: 'send', count: 1 });
  });

  it('times one markdown render to its commit', () => {
    setVisibility('visible');
    setPerfMetricsEnabled(true);

    const started = beginMarkdownRender();
    clock += 12;
    endMarkdownRender(started);
    expect(markdownStats().max).toBe(12);

    // Stopping the sampler clears the rings, and the disabled pair is a
    // zero the effect has nothing to report for.
    setPerfMetricsEnabled(false);
    endMarkdownRender(beginMarkdownRender());
    expect(markdownStats().max).toBe(0);
  });
});
