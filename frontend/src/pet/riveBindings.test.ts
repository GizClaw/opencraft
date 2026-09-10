import { describe, expect, it } from 'vitest';
import type { PetPack } from './pack';
import type { PetView } from './state';
import {
  intentWriteForView,
  resolveBindingValue,
  valueWritesForView,
} from './riveBindings';

const pack: PetPack = {
  id: 'test',
  displayName: 'test',
  version: '1',
  artboard: 'Pet',
  stateMachine: 'PetSM',
  viewModel: 'PetVM',
  meta: { scale: 1, walkSpeed: 110 },
  bindings: {
    phase: {
      type: 'string',
      property: 'phase',
      values: { idle: 'Idle', tool: 'Tool', answering: 'Answering' },
    },
    tool: {
      type: 'string',
      property: 'tool',
      values: { file: 'File' },
      fallback: 'Busy',
    },
    walking: { type: 'boolean', property: 'walking' },
    facing: {
      type: 'string',
      property: 'facing',
      values: { left: 'TurnLeft', right: 'TurnRight' },
    },
    sleeping: { type: 'boolean', property: 'sleeping' },
    'intent:wave': { type: 'trigger', property: 'wave' },
  },
};

function view(overrides: Partial<PetView>): PetView {
  return {
    phase: 'idle',
    disposition: 'roam',
    interactive: false,
    intentSeq: 0,
    ...overrides,
  };
}

describe('valueWritesForView', () => {
  it('writes the phase, walking and sleeping properties in order', () => {
    expect(valueWritesForView(pack, view({ phase: 'answering' }))).toEqual([
      { property: 'phase', type: 'string', value: 'Answering' },
      { property: 'walking', type: 'boolean', value: false },
      { property: 'sleeping', type: 'boolean', value: false },
    ]);
  });

  it('writes the tool category alongside the tool phase', () => {
    expect(
      valueWritesForView(
        pack,
        view({ phase: 'tool', toolCategory: 'file', toolName: 'apply_patch' }),
      ),
    ).toEqual([
      { property: 'phase', type: 'string', value: 'Tool' },
      { property: 'tool', type: 'string', value: 'File' },
      { property: 'walking', type: 'boolean', value: false },
      { property: 'sleeping', type: 'boolean', value: false },
    ]);
  });

  it('falls back for unknown tool categories', () => {
    expect(
      valueWritesForView(pack, view({ phase: 'tool', toolCategory: 'other' })),
    ).toContainEqual({ property: 'tool', type: 'string', value: 'Busy' });
  });

  it('leaves the tool property alone outside the tool phase', () => {
    expect(
      valueWritesForView(pack, view({ phase: 'idle', toolCategory: 'file' })),
    ).toEqual([
      { property: 'phase', type: 'string', value: 'Idle' },
      { property: 'walking', type: 'boolean', value: false },
      { property: 'sleeping', type: 'boolean', value: false },
    ]);
  });

  it('carries the locomotion flags through', () => {
    const writes = valueWritesForView(pack, view({ walking: true }));
    expect(writes).toContainEqual({
      property: 'walking',
      type: 'boolean',
      value: true,
    });
    expect(
      valueWritesForView(pack, view({ sleeping: true, disposition: 'sleep' })),
    ).toContainEqual({
      property: 'sleeping',
      type: 'boolean',
      value: true,
    });
  });

  it('writes the facing value only when the rover reports one', () => {
    expect(
      valueWritesForView(pack, view({ walking: true, facing: 'left' })),
    ).toContainEqual({
      property: 'facing',
      type: 'string',
      value: 'TurnLeft',
    });
    // Facing stays empty until the pet has walked: nothing to write.
    expect(
      valueWritesForView(pack, view({ walking: true })).some(
        (write) => write.property === 'facing',
      ),
    ).toBe(false);
  });

  it('passes the raw facing through a values-less string binding', () => {
    const raw: PetPack = {
      ...pack,
      bindings: { facing: { type: 'string', property: 'turn' } },
    };
    expect(valueWritesForView(raw, view({ facing: 'right' }))).toEqual([
      { property: 'turn', type: 'string', value: 'right' },
    ]);
  });

  it('leaves the facing property alone without a binding', () => {
    const bare: PetPack = {
      ...pack,
      bindings: { walking: pack.bindings.walking },
    };
    expect(
      valueWritesForView(bare, view({ walking: true, facing: 'left' })).some(
        (write) => write.property === 'facing',
      ),
    ).toBe(false);
  });

  it('skips properties the pack does not bind', () => {
    const bare: PetPack = {
      ...pack,
      bindings: { walking: pack.bindings.walking },
    };
    expect(valueWritesForView(bare, view({ phase: 'tool' }))).toEqual([
      { property: 'walking', type: 'boolean', value: false },
    ]);
  });
});

describe('intentWriteForView', () => {
  it('fires the trigger of a bound intent', () => {
    expect(intentWriteForView(pack, view({ intent: 'wave' }))).toEqual({
      property: 'wave',
      type: 'trigger',
      value: 0,
    });
  });

  it('has nothing to fire for unbound intents', () => {
    // welcome greets through the bubble and nap is the sleeping value.
    expect(
      intentWriteForView(pack, view({ intent: 'welcome' })),
    ).toBeUndefined();
    expect(intentWriteForView(pack, view({ intent: 'nap' }))).toBeUndefined();
  });

  it('has nothing to fire without an intent', () => {
    expect(intentWriteForView(pack, view({}))).toBeUndefined();
  });
});

describe('resolveBindingValue', () => {
  it('prefers the table, then the fallback, then the raw string', () => {
    expect(
      resolveBindingValue(
        { type: 'string', property: 'p', values: { a: 'A' } },
        'a',
      ),
    ).toBe('A');
    expect(
      resolveBindingValue(
        { type: 'string', property: 'p', values: { a: 'A' }, fallback: 'F' },
        'b',
      ),
    ).toBe('F');
    expect(resolveBindingValue({ type: 'string', property: 'p' }, 'b')).toBe(
      'b',
    );
  });

  it('does not invent a value for enum bindings', () => {
    expect(
      resolveBindingValue(
        { type: 'enum', property: 'p', values: { a: 'A' } },
        'b',
      ),
    ).toBeUndefined();
  });
});
