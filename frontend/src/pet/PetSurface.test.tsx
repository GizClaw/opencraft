import { act, render, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import PetSurface from './PetSurface';

// React and jsdom both log through the console in this test (caught
// render noise, an unimplemented 2d context); the assertions below are
// what the test reads.
vi.hoisted(() => {
  console.error = () => {};
  console.warn = () => {};
});

const apiMock = vi.hoisted(() => ({
  petSettings: vi.fn(async () => ({ assistantCharacter: 'assistant-default' })),
  petListPacks: vi.fn(async () => [
    {
      id: 'assistant-default',
      displayName: 'Assistant',
      version: '1.0.0',
      artboard: 'Pet',
      rivAsset: 'builtin://assistant-default',
      meta: { scale: 1, walkSpeed: 110 },
      bindings: {},
    },
  ]),
  petPackAsset: vi.fn(async () => 'AA=='),
  petReportRuntimeStatus: vi.fn(async () => undefined),
  petReportGeometry: vi.fn(async () => undefined),
  petBeginDrag: vi.fn(async () => undefined),
  petGetPosition: vi.fn(async () => ({ ready: false, x: 0, y: 0 })),
}));

const eventsMock = vi.hoisted(() => ({
  pet: null as null | ((event: { data: unknown }) => void),
}));

const riveMock = vi.hoisted(() => ({
  apply: vi.fn(),
  status: vi.fn(() => ({ pack_id: 'assistant-default', ok: true })),
  pause: vi.fn(),
  play: vi.fn(),
  resize: vi.fn(),
  destroy: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));
vi.mock('@wailsio/runtime', () => ({
  Events: {
    On: (channel: string, callback: (event: { data: unknown }) => void) => {
      // PetStateChannel, see lib/events.ts.
      if (channel === 'pet:state') eventsMock.pet = callback;
      return () => {};
    },
  },
}));
vi.mock('./rive', () => ({
  base64ToArrayBuffer: () => new ArrayBuffer(0),
  createPetRive: async () => riveMock,
}));

// The readback is the cost this file's sampler tests are about, so
// geometry is counted rather than computed: every call is one
// GPU→CPU read of the whole canvas.
const geometryMock = vi.hoisted(() => ({
  measurePetGeometry: vi.fn(() => ({
    canvas: { x: 0, y: 0, width: 168, height: 168 },
    art: { x: 10, y: 40, width: 60, height: 90 },
    measured: true,
  })),
}));
vi.mock('./geometry', () => geometryMock);

const snapshot = (phase: string) => ({ phase, disposition: 'work' });

// flush settles the mount chain (settings, packs, asset bytes, runtime
// create) under fake timers, where waitFor cannot be left to advance
// them: the sample it would advance is the one being measured.
async function flush() {
  for (let turn = 0; turn < 8; turn++) await act(async () => {});
}

async function mountPet() {
  // Clear before the render: a previous mount in this file leaves calls
  // behind, and they would satisfy the wait below before this surface has
  // a runtime at all — which is what made the assertions after it measure
  // a runtime that was not there yet.
  riveMock.apply.mockClear();
  riveMock.play.mockClear();
  riveMock.pause.mockClear();
  render(<PetSurface />);
  await waitFor(() => expect(riveMock.apply).toHaveBeenCalled());
  // The mount path applies the current view once; the counts below are
  // measured from there.
  riveMock.apply.mockClear();
  riveMock.play.mockClear();
  riveMock.pause.mockClear();
}

describe('PetSurface playback', () => {
  // F-31: rive.play() tears the pointer listeners down and re-creates
  // them and restarts the render loop, so it must only run on a runtime
  // this surface actually paused — never on every pet:state snapshot.
  it('does not restart playback for every state snapshot', async () => {
    await mountPet();
    expect(eventsMock.pet).toBeTypeOf('function');

    for (const phase of ['thinking', 'tool', 'answering']) {
      act(() => eventsMock.pet!({ data: snapshot(phase) }));
    }

    expect(riveMock.apply).toHaveBeenCalledTimes(3);
    expect(riveMock.play).not.toHaveBeenCalled();
  });

  it('restarts playback only after it paused for a hidden window', async () => {
    await mountPet();
    const setHidden = (hidden: boolean) => {
      Object.defineProperty(document, 'hidden', {
        configurable: true,
        get: () => hidden,
      });
      act(() => {
        document.dispatchEvent(new Event('visibilitychange'));
      });
    };

    // A payload first: it wakes a runtime the idle park may have put to
    // sleep, so the pair below always starts from "running" (and pins that
    // the payload reached a live runtime at all).
    act(() => eventsMock.pet!({ data: snapshot('thinking') }));
    expect(riveMock.apply).toHaveBeenCalled();
    riveMock.play.mockClear();
    riveMock.pause.mockClear();

    setHidden(true);
    expect(riveMock.pause).toHaveBeenCalledTimes(1);

    setHidden(false);
    expect(riveMock.play).toHaveBeenCalledTimes(1);

    // A payload that arrives while the runtime is running must not
    // rebuild its listeners again.
    act(() => eventsMock.pet!({ data: snapshot('thinking') }));
    act(() => eventsMock.pet!({ data: snapshot('done') }));
    expect(riveMock.play).toHaveBeenCalledTimes(1);
  });
});

// F-34: the measurement is a GPU→CPU readback of the whole drawing
// surface. It used to run every 500ms for as long as the runtime was
// drawing, whether or not anything had moved; it now follows the
// picture — a pose change or a resize reads once its frame is drawn,
// and a 2s heartbeat covers an animation running inside one state.
describe('PetSurface geometry sampling', () => {
  // Advances past the few frames a pending read waits for, so the next
  // assertion starts from a sampler with nothing in flight.
  async function settleSampler() {
    await act(async () => {
      await vi.advanceTimersByTimeAsync(64);
    });
    geometryMock.measurePetGeometry.mockClear();
  }

  it('reads the canvas on a slow heartbeat, not twice a second', async () => {
    vi.useFakeTimers();
    try {
      render(<PetSurface />);
      await flush();
      await settleSampler();

      // Nothing announced a change: the old sampler still spent two
      // readbacks in this window.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1_000);
      });
      expect(geometryMock.measurePetGeometry).not.toHaveBeenCalled();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(1_200);
      });
      expect(geometryMock.measurePetGeometry).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it('reads the canvas as soon as a pose arrives', async () => {
    vi.useFakeTimers();
    try {
      render(<PetSurface />);
      await flush();
      await settleSampler();

      act(() => eventsMock.pet!({ data: snapshot('tool') }));
      // The settle frames, not the heartbeat: far short of 2s.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(64);
      });
      expect(geometryMock.measurePetGeometry).toHaveBeenCalledTimes(1);

      // A pose that does not move the box is read (the readback is how
      // the surface knows) but must not be reported again.
      apiMock.petReportGeometry.mockClear();
      act(() => eventsMock.pet!({ data: snapshot('answering') }));
      await act(async () => {
        await vi.advanceTimersByTimeAsync(64);
      });
      expect(geometryMock.measurePetGeometry).toHaveBeenCalledTimes(2);
      expect(apiMock.petReportGeometry).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it('reads the canvas after a resize', async () => {
    vi.useFakeTimers();
    try {
      render(<PetSurface />);
      await flush();
      await settleSampler();

      act(() => {
        window.dispatchEvent(new Event('resize'));
      });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(64);
      });
      expect(geometryMock.measurePetGeometry).toHaveBeenCalledTimes(1);
      expect(riveMock.resize).toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it('skips the heartbeat while the window is hidden', async () => {
    vi.useFakeTimers();
    try {
      render(<PetSurface />);
      await flush();
      await settleSampler();

      Object.defineProperty(document, 'hidden', {
        configurable: true,
        get: () => true,
      });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5_000);
      });
      // A frozen (or off-screen) frame cannot measure differently, so
      // the readback is skipped rather than run against a stale canvas.
      expect(geometryMock.measurePetGeometry).not.toHaveBeenCalled();
    } finally {
      delete (document as { hidden?: boolean }).hidden;
      vi.useRealTimers();
    }
  });

  it('reads the canvas when the pet is pressed', async () => {
    vi.useFakeTimers();
    try {
      render(<PetSurface />);
      await flush();
      await settleSampler();

      const canvas = document.querySelector('canvas');
      expect(canvas).not.toBeNull();
      // jsdom lays every element out at zero, and the hit test starts
      // from the canvas rectangle.
      canvas!.getBoundingClientRect = () =>
        ({
          x: 0,
          y: 0,
          top: 0,
          left: 0,
          right: 168,
          bottom: 168,
          width: 168,
          height: 168,
          toJSON: () => ({}),
        }) as DOMRect;
      // jsdom has no pointer capture; the press path takes it.
      Object.defineProperty(HTMLElement.prototype, 'setPointerCapture', {
        configurable: true,
        writable: true,
        value: () => {},
      });
      // jsdom hands out no 2d context, so the pixel probe falls back to
      // "inside the canvas" and the press proceeds; the canvas is the
      // only element on the stage.
      act(() => {
        canvas!.dispatchEvent(
          new PointerEvent('pointerdown', {
            bubbles: true,
            clientX: 10,
            clientY: 10,
          }),
        );
      });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(64);
      });
      // The drag and the dock spot that follows it are anchored on this
      // read, so it cannot wait for the heartbeat.
      expect(geometryMock.measurePetGeometry).toHaveBeenCalled();
    } finally {
      delete (HTMLElement.prototype as { setPointerCapture?: unknown })
        .setPointerCapture;
      vi.useRealTimers();
    }
  });
});
