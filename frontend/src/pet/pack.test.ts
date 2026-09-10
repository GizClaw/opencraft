import { describe, expect, it } from 'vitest';
import type { PetPack } from './pack';
import { BUILTIN_ASSISTANT_PACK_ID, pickPack } from './pack';

function pack(id: string): PetPack {
  return {
    id,
    displayName: id,
    version: '1',
    artboard: 'Pet',
    stateMachine: 'PetSM',
    viewModel: 'PetVM',
    meta: { scale: 1, walkSpeed: 110 },
    bindings: {},
  };
}

describe('pickPack', () => {
  it('prefers the configured character', () => {
    const packs = [pack(BUILTIN_ASSISTANT_PACK_ID), pack('neko')];
    expect(pickPack('neko', packs)?.id).toBe('neko');
  });

  it('falls back to the builtin when the preference is missing', () => {
    const packs = [pack(BUILTIN_ASSISTANT_PACK_ID), pack('neko')];
    expect(pickPack('ghost', packs)?.id).toBe(BUILTIN_ASSISTANT_PACK_ID);
  });

  it('returns undefined when no pack is registered', () => {
    expect(pickPack('neko', [])).toBeUndefined();
  });
});
