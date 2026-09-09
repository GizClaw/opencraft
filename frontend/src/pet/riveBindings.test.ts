import { describe, expect, it } from 'vitest';
import type { PetPack } from './pack';
import type { PetView } from './state';
import { bindingForView } from './riveBindings';

const pack: PetPack = {
  id: 'test',
  displayName: 'test',
  version: '1',
  stateMachine: 'PetSM',
  meta: { scale: 1, walkSpeed: 110, anchor: 'bottom-center' },
  bindings: {
    idle: { type: 'input', name: 'idle' },
    'tool:file': { type: 'input', name: 'busy' },
    'tool:*': { type: 'input', name: 'busy' },
    answering: { type: 'input', name: 'talk' },
  },
};

function view(overrides: Partial<PetView>): PetView {
  return {
    phase: 'idle',
    disposition: 'roam',
    interactive: false,
    ...overrides,
  };
}

describe('bindingForView', () => {
  it('maps a phase to its direct binding', () => {
    expect(bindingForView(pack, view({ phase: 'answering' }))).toEqual({
      type: 'input',
      name: 'talk',
    });
  });

  it('prefers a concrete tool category over the wildcard', () => {
    expect(
      bindingForView(
        pack,
        view({ phase: 'tool', toolCategory: 'file', toolName: 'apply_patch' }),
      ),
    ).toEqual({ type: 'input', name: 'busy' });
  });

  it('falls back to the wildcard for unknown categories', () => {
    expect(
      bindingForView(pack, view({ phase: 'tool', toolCategory: 'other' })),
    ).toEqual({ type: 'input', name: 'busy' });
  });

  it('returns undefined when the pack lacks a mapping', () => {
    expect(bindingForView(pack, view({ phase: 'thinking' }))).toBeUndefined();
  });

  it('prefers walk while the window is roaming idle', () => {
    const p: PetPack = {
      ...pack,
      bindings: {
        ...pack.bindings,
        walk: { type: 'input', name: 'walk' },
      },
    };
    expect(bindingForView(p, view({ phase: 'idle', walking: true }))).toEqual({
      type: 'input',
      name: 'walk',
    });
  });

  it('keeps work states ahead of walk', () => {
    expect(
      bindingForView(pack, view({ phase: 'answering', walking: true })),
    ).toEqual({ type: 'input', name: 'talk' });
  });
});
