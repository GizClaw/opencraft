// Pins the two places where the renderer and its doubles could drift
// apart without any other suite noticing:
//
//   1. factsFromRuntime is the only production code that reads the real
//      runtime's view model API. A renamed accessor would make every
//      mount "degraded" (or wrongly OK) while every other test stayed
//      green, so the required accessors are asserted against the
//      installed @rive-app/canvas below.
//   2. e2e/mock/riveModule.js stands in for that chunk in the browser
//      spec, so it has to expose the same surface the renderer drives.
import { readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { Rive as StubRive } from '../../e2e/mock/riveModule.js';
import { factsFromRuntime } from './rive';
import { validatePack } from './validate';
import type { PetPack } from './pack';

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');

/** Accessors `createPetRive` drives on the loaded Rive instance. */
const RIVE_ACCESSORS = [
  'contents',
  'on',
  'off',
  'viewModelByName',
  'viewModelByIndex',
  'defaultViewModel',
  'enums',
  'bindViewModelInstance',
  'resizeDrawingSurfaceToCanvas',
  'pause',
  'play',
  'cleanup',
];

/** Accessors it uses to resolve and write the pack's view model. */
const VIEW_MODEL_ACCESSORS = [
  'properties',
  'defaultInstance',
  'instanceByName',
  'instance',
];
const INSTANCE_ACCESSORS = [
  'string',
  'enum',
  'number',
  'boolean',
  'color',
  'trigger',
];

/** The pack the desktop ships, read from the file the binary embeds. */
const PACK: PetPack = JSON.parse(
  readFileSync(
    join(
      REPO_ROOT,
      'internal/adapters/desktop/pet/assets/assistant-default.pack.json',
    ),
    'utf8',
  ),
);

/**
 * Reports whether a class exposes one accessor, walking the prototype
 * chain so both methods and getters count.
 */
function exposes(prototype: object, name: string): boolean {
  for (
    let current: object | null = prototype;
    current;
    current = Object.getPrototypeOf(current) as object | null
  ) {
    const descriptor = Object.getOwnPropertyDescriptor(current, name);
    if (!descriptor) continue;
    if (typeof descriptor.value === 'function') return true;
    if (typeof descriptor.get === 'function') return true;
  }
  return false;
}

/**
 * Resolves the runtime namespace. `@rive-app/canvas` ships as CJS, so
 * depending on the loader the classes hang off the namespace itself or
 * off its `default` export.
 */
async function runtimeNamespace(): Promise<Record<string, unknown>> {
  const mod = (await import('@rive-app/canvas')) as unknown as Record<
    string,
    unknown
  >;
  const asDefault = mod.default as Record<string, unknown> | undefined;
  return asDefault && asDefault.Rive ? asDefault : mod;
}

function prototypeOf(namespace: Record<string, unknown>, name: string) {
  const exported = namespace[name] as { prototype?: object } | undefined;
  expect(exported, `${name} is not exported by @rive-app/canvas`).toBeDefined();
  return exported?.prototype ?? {};
}

describe('installed Rive runtime', () => {
  it('exposes every accessor the renderer drives', async () => {
    const namespace = await runtimeNamespace();
    const rive = prototypeOf(namespace, 'Rive');
    for (const name of RIVE_ACCESSORS) {
      expect(exposes(rive, name), `Rive.${name}`).toBe(true);
    }

    const viewModel = prototypeOf(namespace, 'ViewModel');
    for (const name of VIEW_MODEL_ACCESSORS) {
      expect(exposes(viewModel, name), `ViewModel.${name}`).toBe(true);
    }

    const instance = prototypeOf(namespace, 'ViewModelInstance');
    for (const name of INSTANCE_ACCESSORS) {
      expect(exposes(instance, name), `ViewModelInstance.${name}`).toBe(true);
    }
  });
});

describe('playwright Rive stub', () => {
  it('stands in for the same surface as the real runtime', async () => {
    const namespace = await runtimeNamespace();
    const realRive = prototypeOf(namespace, 'Rive');
    for (const name of RIVE_ACCESSORS) {
      if (!exposes(realRive, name)) continue;
      expect(exposes(StubRive.prototype, name), `stub Rive.${name}`).toBe(true);
    }

    // The stub resolves the view model and its instance the same way,
    // so the spec exercises the production resolution order.
    const stub = new StubRive({ buffer: new ArrayBuffer(4) });
    const viewModel = stub.defaultViewModel();
    expect(viewModel?.name).toBe(PACK.viewModel);
    for (const name of ['defaultInstance', 'instanceByName', 'instance']) {
      expect(
        typeof (viewModel as unknown as Record<string, unknown>)[name],
        `stub ViewModel.${name}`,
      ).toBe('function');
    }
  });
});

describe('factsFromRuntime', () => {
  it('builds facts the shipped pack validates against', () => {
    const stub = new StubRive({ buffer: new ArrayBuffer(4) });
    const facts = factsFromRuntime(
      {
        contents: stub.contents,
        viewModelByIndex: (index) => stub.viewModelByIndex(index),
        defaultViewModel: () => stub.defaultViewModel(),
        enums: () => stub.enums(),
      },
      PACK.artboard,
    );

    expect(facts.artboards).toEqual([PACK.artboard]);
    expect(facts.stateMachines[PACK.artboard]).toEqual([PACK.stateMachine]);
    expect(facts.defaultViewModel[PACK.artboard]).toBe(PACK.viewModel);
    const properties = (facts.viewModels[PACK.viewModel ?? ''] ?? [])
      .map((property) => property.name)
      .sort();
    expect(properties).toEqual(
      Object.values(PACK.bindings)
        .map((binding) => binding.property)
        .sort(),
    );
    expect(validatePack(PACK, facts)).toEqual([]);
  });
});
