// The Playwright stub (riveModule.js) stands in for the real
// `@rive-app/canvas` chunk, so it has to expose the same surface
// `src/pet/rive.ts` drives. Declaring it here lets the unit test in
// src/pet/riveRuntime.test.ts pin both sides against each other.

export interface StubViewModel {
  name: string;
  properties: { name: string; type: string; enumName?: string }[];
  instance(): unknown;
  defaultInstance(): unknown;
  instanceByName(name: string): unknown;
}

export declare const EventType: { Load: string; LoadError: string };

export declare class Rive {
  constructor(options: Record<string, unknown>);
  get contents(): {
    artboards?: { name: string; stateMachines: { name: string }[] }[];
  };
  on(type: string, handler: (payload: unknown) => void): void;
  off(type: string, handler: (payload: unknown) => void): void;
  viewModelByName(name: string): StubViewModel | null;
  viewModelByIndex(index: number): StubViewModel | null;
  defaultViewModel(): StubViewModel | null;
  enums(): { name: string; values: string[] }[];
  bindViewModelInstance(instance: unknown): void;
  resizeDrawingSurfaceToCanvas(): void;
  pause(): void;
  play(): void;
  cleanup(): void;
}

export declare const r: { EventType: typeof EventType; Rive: typeof Rive };
