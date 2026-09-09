// Declarative pet pack contract shared by the plugin host (registration
// side) and the pet surface renderer. A pack is pure data: the .riv
// binary travels through the Go registry's asset channel, and the
// renderer only ever sees specs, never plugin code.

export type PetPackBindingType = 'input' | 'trigger';

export interface PetPackBinding {
  type: PetPackBindingType;
  name: string;
}

export interface PetPackMeta {
  scale: number;
  walkSpeed: number;
  anchor: string;
}

export interface PetPack {
  id: string;
  displayName: string;
  version: string;
  /** Set by the host to the registering plugin's id. */
  pluginId?: string;
  stateMachine: string;
  /** builtin://<id> or plugin://<pluginId>/<path>. Empty keeps the
   *  placeholder renderer active until a .riv asset is delivered. */
  rivAsset?: string;
  meta: PetPackMeta;
  bindings: Record<string, PetPackBinding>;
}

export const BUILTIN_ASSISTANT_PACK_ID = 'assistant-default';

/**
 * Resolves the active pack for a pet surface. Preference wins, the
 * builtin assistant pack is the fallback, and the first registered
 * pack covers registry setups without the builtin (tests, mocks).
 */
export function pickPack(
  preferredId: string | undefined,
  packs: PetPack[] | null | undefined,
): PetPack | undefined {
  const list = packs ?? [];
  if (preferredId) {
    const preferred = list.find((pack) => pack.id === preferredId);
    if (preferred) return preferred;
  }
  return (
    list.find((pack) => pack.id === BUILTIN_ASSISTANT_PACK_ID) ?? list[0]
  );
}
