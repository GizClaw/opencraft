// Asset contract test for the shipped desktop pet character.
//
// Three things have to agree, and nothing else checks it:
//   1. the .riv asset (artboard `Pet`, state machine `PetSM`, view model
//      `PetVM`, and the states it can reach),
//   2. the pack definition the Go side embeds
//      (internal/adapters/desktop/pet/assets/assistant-default.pack.json),
//   3. the activity vocabulary the frontend feeds it.
//
// It runs the official Rive runtime headless (wasm, no Chromium) through
// the advanced API, which is also how the out-of-tree asset generator
// verifies the same asset.
import { readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { BUILTIN_ASSISTANT_PACK_ID, petIntentSlot, type PetPack } from './pack';
import { writeToInstance, type ViewModelInstanceLike } from './rive';
import { intentWriteForView, valueWritesForView } from './riveBindings';
import type { PetView } from './state';
import {
  packRuntimeStatus,
  validatePack,
  type PetAssetFacts,
} from './validate';

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
const ASSET_DIR = join(REPO_ROOT, 'internal/adapters/desktop/pet/assets');
const RIVE_PKG_DIR = join(
  REPO_ROOT,
  'frontend/node_modules/@rive-app/canvas-advanced',
);

/** The pack definition the Go builtin pack is embedded from. */
const PACK: PetPack = JSON.parse(
  readFileSync(join(ASSET_DIR, 'assistant-default.pack.json'), 'utf8'),
);

/** Every state the shipped state machine can reach. */
const PET_STATES = [
  'idle',
  'walk',
  'nap',
  'think',
  'talk',
  'ask',
  'cheer',
  'fail',
  'exec',
  'file',
  'web',
  'generate',
  'busy',
  'wave',
  'look',
  'sulk',
  'zoomies',
].sort();

/** View model property -> data binding type. */
const PET_PROPERTIES: Record<string, string> = {
  phase: 'string',
  tool: 'string',
  walking: 'boolean',
  sleeping: 'boolean',
  wave: 'trigger',
  look: 'trigger',
  sulk: 'trigger',
  zoomies: 'trigger',
};

// Minimal structural view of the advanced runtime, declared locally so the
// test does not depend on the package's type declarations.
interface RivArtboard {
  name: string;
  animationCount(): number;
  animationByIndex(index: number): { name: string };
  stateMachineCount(): number;
  stateMachineByIndex(index: number): unknown & { name: string };
}
interface RivViewModel {
  name: string;
  defaultInstance(): ViewModelInstanceLike;
  getProperties(): { name: string; type: string; enumName?: string }[];
}
interface RivFile {
  artboardCount(): number;
  artboardByIndex(index: number): RivArtboard;
  viewModelCount(): number;
  viewModelByIndex(index: number): RivViewModel | null;
  defaultArtboardViewModel(artboard: RivArtboard): RivViewModel | null;
  enums(): { name: string; values: string[] }[];
  delete(): void;
}
interface RivStateMachineInstance {
  inputCount(): number;
  bindViewModelInstance(instance: ViewModelInstanceLike): void;
  advanceAndApply(seconds: number): boolean;
  stateChangedCount(): number;
  stateChangedNameByIndex(index: number): string;
  delete(): void;
}
interface RivRuntime {
  load(bytes: Uint8Array): Promise<RivFile>;
  StateMachineInstance: new (
    stateMachine: unknown,
    artboard: RivArtboard,
  ) => RivStateMachineInstance;
}

let runtime: RivRuntime;
let file: RivFile;
let artboard: RivArtboard;
let instance: ViewModelInstanceLike;
let machine: RivStateMachineInstance;

/** Builds a state machine instance bound to the shipped view model. */
function newMachine(): RivStateMachineInstance {
  const created = new runtime.StateMachineInstance(
    artboard.stateMachineByIndex(0),
    artboard,
  );
  created.bindViewModelInstance(instance);
  return created;
}

beforeAll(async () => {
  const module = (await import('@rive-app/canvas-advanced')) as unknown as {
    default: (options: {
      locateFile: (name: string) => string;
      wasmBinary: Uint8Array;
    }) => Promise<RivRuntime>;
  };
  runtime = await module.default({
    locateFile: (name) => join(RIVE_PKG_DIR, name),
    wasmBinary: new Uint8Array(readFileSync(join(RIVE_PKG_DIR, 'rive.wasm'))),
  });
  file = await runtime.load(
    new Uint8Array(readFileSync(join(ASSET_DIR, 'assistant-default.riv'))),
  );
  artboard = file.artboardByIndex(0);
  const viewModel =
    file.viewModelByIndex(0) ?? file.defaultArtboardViewModel(artboard);
  if (!viewModel) throw new Error('shipped pet asset has no view model');
  instance = viewModel.defaultInstance();
  machine = newMachine();
});

afterAll(() => {
  machine?.delete();
  file?.delete();
});

/**
 * The asset facts the renderer builds on mount: same shape, same meaning,
 * so the pack is validated here exactly like it is at runtime.
 */
function assetFacts(): PetAssetFacts {
  const boards = Array.from({ length: file.artboardCount() }, (_, index) =>
    file.artboardByIndex(index),
  );
  const facts: PetAssetFacts = {
    artboards: boards.map((board) => board.name),
    stateMachines: Object.fromEntries(
      boards.map((board) => [
        board.name,
        Array.from(
          { length: board.stateMachineCount() },
          (_, index) => board.stateMachineByIndex(index).name,
        ),
      ]),
    ),
    defaultViewModel: {},
    viewModels: {},
    enums: Object.fromEntries(
      file.enums().map((dataEnum) => [dataEnum.name, dataEnum.values]),
    ),
  };
  for (let index = 0; index < file.viewModelCount(); index++) {
    const viewModel = file.viewModelByIndex(index);
    if (!viewModel) break;
    facts.viewModels[viewModel.name] = viewModel
      .getProperties()
      .map((property) => ({
        name: property.name,
        type: property.type,
        enumName: property.enumName,
      }));
  }
  const fallback = file.defaultArtboardViewModel(artboard);
  if (fallback) facts.defaultViewModel[artboard.name] = fallback.name;
  return facts;
}

/**
 * Advances the state machine and returns the state it is in: the last
 * state it entered, or the one it stayed in when nothing moved.
 */
function settle(seconds = 1): string {
  for (let frame = 0; frame < Math.round(seconds * 60); frame++) {
    machine.advanceAndApply(1 / 60);
    for (let i = 0; i < machine.stateChangedCount(); i++) {
      settledState = machine.stateChangedNameByIndex(i);
    }
  }
  return settledState;
}

/** Last state the machine entered (idle is the entry state). */
let settledState = 'idle';

function view(overrides: Partial<PetView>): PetView {
  return {
    phase: 'idle',
    disposition: 'roam',
    interactive: false,
    intentSeq: 0,
    ...overrides,
  };
}

/**
 * Writes one surface state the way the renderer does (value writes from
 * the pack bindings, then the intent trigger) and reports the state the
 * character ended up in.
 */
function drive(overrides: Partial<PetView>, seconds = 1): string {
  const next = view(overrides);
  for (const write of valueWritesForView(PACK, next)) {
    writeToInstance(instance, write);
  }
  if (next.intent) {
    const intent = intentWriteForView(PACK, next);
    if (intent) writeToInstance(instance, intent);
  }
  return settle(seconds);
}

describe('shipped pet asset structure', () => {
  it('carries the artboard, state machine and view model the pack names', () => {
    expect(file.artboardCount()).toBe(1);
    expect(artboard.name).toBe(PACK.artboard);
    expect(
      Array.from(
        { length: artboard.stateMachineCount() },
        (_, index) => artboard.stateMachineByIndex(index).name,
      ),
    ).toContain(PACK.stateMachine);
    expect(
      Array.from(
        { length: file.viewModelCount() },
        (_, index) => file.viewModelByIndex(index)?.name,
      ),
    ).toContain(PACK.viewModel);
  });

  it('exposes exactly the view model properties the pack drives', () => {
    const viewModel = file.viewModelByIndex(0);
    expect(viewModel).not.toBeNull();
    const properties = Object.fromEntries(
      viewModel!
        .getProperties()
        .map((property) => [property.name, property.type]),
    );
    expect(properties).toEqual(PET_PROPERTIES);
  });

  it('has an animation for every state', () => {
    const animations = Array.from(
      { length: artboard.animationCount() },
      (_, index) => artboard.animationByIndex(index).name,
    ).sort();
    expect(animations).toEqual(PET_STATES);
  });

  it('drives nothing through state machine inputs', () => {
    // Contract v2 is value- and trigger-driven only; an input left over
    // from the input-driven asset would silently keep the old wiring.
    const probe = newMachine();
    expect(probe.inputCount()).toBe(0);
    probe.delete();
  });
});

describe('pack definition matches the asset', () => {
  it('validates against the shipped character', () => {
    expect(validatePack(PACK, assetFacts())).toEqual([]);
    expect(packRuntimeStatus(PACK, []).ok).toBe(true);
  });

  it('is the builtin assistant pack the Go side embeds', () => {
    expect(PACK.id).toBe(BUILTIN_ASSISTANT_PACK_ID);
    expect(PACK.rivAsset).toBe(`builtin://${BUILTIN_ASSISTANT_PACK_ID}`);
    expect(PACK.meta.walkSpeed).toBeGreaterThan(0);
    expect(PACK.meta.scale).toBeGreaterThan(0);
  });

  it('reports a degraded character for a pack that does not match', () => {
    const facts = assetFacts();
    // Without its artboard nothing else is knowable: the report stops there.
    const alien: PetPack = { ...PACK, artboard: 'Cat' };
    expect(validatePack(alien, facts)).toEqual(['artboard "Cat"']);

    const mismatched: PetPack = {
      ...PACK,
      bindings: {
        ...PACK.bindings,
        walking: { type: 'number', property: 'walking' },
        'intent:wave': { type: 'trigger', property: 'nope' },
      },
    };
    const missing = validatePack(mismatched, facts);
    expect(missing).toEqual([
      'binding "intent:wave": property "nope"',
      'binding "walking": property "walking" is boolean, not number',
    ]);
    const status = packRuntimeStatus(mismatched, missing);
    expect(status.ok).toBe(false);
    expect(status.missing).toEqual(missing);
    expect(status.pack_id).toBe(PACK.id);
  });
});

describe('pack covers the frontend vocabulary', () => {
  it('maps every phase the director can report', () => {
    const values = PACK.bindings.phase.values ?? {};
    for (const phase of [
      'idle',
      'thinking',
      'tool',
      'answering',
      'asking',
      'done',
      'error',
    ]) {
      expect(values[phase], `phase ${phase}`).toBeTruthy();
    }
  });

  it('maps the tool categories the asset draws and falls back for the rest', () => {
    const tool = PACK.bindings.tool;
    for (const category of ['exec', 'file', 'web', 'generate']) {
      expect(tool.values?.[category], `tool ${category}`).toBeTruthy();
    }
    // The Go vocabulary has more categories than the character has poses
    // (skill/plan/ask/delegate/other); they all land on the fallback.
    expect(tool.fallback).toBeTruthy();
  });

  it('binds walking and sleeping as values, not reactions', () => {
    expect(PACK.bindings.walking.type).toBe('boolean');
    expect(PACK.bindings.sleeping.type).toBe('boolean');
  });

  it('binds the one-shot intents as triggers and leaves the rest alone', () => {
    for (const intent of ['wave', 'look', 'sulk', 'zoomies']) {
      expect(PACK.bindings[petIntentSlot(intent)]?.type, intent).toBe(
        'trigger',
      );
    }
    // welcome greets through the bubble; nap is the sleeping value.
    expect(PACK.bindings[petIntentSlot('welcome')]).toBeUndefined();
    expect(PACK.bindings[petIntentSlot('nap')]).toBeUndefined();
  });
});

describe('state machine behaviour', () => {
  it('starts idle', () => {
    expect(settle(0.5)).toBe('idle');
  });

  it('follows the phase property', () => {
    expect(drive({ phase: 'idle' })).toBe('idle');
    expect(drive({ phase: 'thinking' })).toBe('think');
    expect(drive({ phase: 'answering' })).toBe('talk');
    expect(drive({ phase: 'asking' })).toBe('ask');
    expect(drive({ phase: 'done' })).toBe('cheer');
    expect(drive({ phase: 'error' })).toBe('fail');
  });

  it('picks the tool pose from the tool property', () => {
    for (const [category, state] of [
      ['exec', 'exec'],
      ['file', 'file'],
      ['web', 'web'],
      ['generate', 'generate'],
    ] as const) {
      expect(
        drive({ phase: 'tool', toolCategory: category, toolName: 'x' }),
      ).toBe(state);
    }
    // An unknown category lands on the fallback pose instead of freezing.
    expect(drive({ phase: 'tool', toolCategory: 'other', toolName: 'x' })).toBe(
      'busy',
    );
  });

  it('walks and naps from the walking/sleeping values', () => {
    expect(drive({ phase: 'idle', walking: true })).toBe('walk');
    expect(drive({ phase: 'idle', walking: false })).toBe('idle');
    expect(drive({ phase: 'idle', sleeping: true, disposition: 'sleep' })).toBe(
      'nap',
    );
    expect(drive({ phase: 'idle', sleeping: false })).toBe('idle');
  });

  it('keeps work phases ahead of walking', () => {
    // The rover sets walking while it moves toward the window, which can
    // overlap a turn; the work pose has to win or the character would
    // look like it is idling in the middle of a job.
    expect(drive({ phase: 'idle', walking: false })).toBe('idle');
    expect(drive({ phase: 'thinking', walking: true })).toBe('think');
    expect(drive({ phase: 'tool', toolCategory: 'exec', walking: true })).toBe(
      'exec',
    );
    expect(drive({ phase: 'answering', walking: true })).toBe('talk');
  });

  it('plays a one-shot per intent and falls back to the phase state', () => {
    // Start from the idle pose: the one-shot has to fall back to the
    // phase the surface is actually in, so pin what that phase is.
    expect(drive({ phase: 'idle', walking: false })).toBe('idle');
    for (const [intent, state] of [
      ['wave', 'wave'],
      ['look', 'look'],
      ['sulk', 'sulk'],
      ['zoomies', 'zoomies'],
    ] as const) {
      const next = view({ intent, intentSeq: 1 });
      const write = intentWriteForView(PACK, next);
      expect(write, intent).toBeDefined();
      writeToInstance(instance, write!);
      expect(settle(0.2)).toBe(state);
      // A one-shot must not stick: the phase state takes over again.
      expect(settle(2)).toBe('idle');
    }
  });

  it('returns to the current phase state after a one-shot', () => {
    expect(drive({ phase: 'thinking' })).toBe('think');
    writeToInstance(
      instance,
      intentWriteForView(PACK, view({ intent: 'wave', intentSeq: 2 }))!,
    );
    expect(settle(0.2)).toBe('wave');
    expect(settle(2)).toBe('think');
  });
});
