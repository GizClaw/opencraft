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

const snapshot = (phase: string) => ({ phase, disposition: 'work' });

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
