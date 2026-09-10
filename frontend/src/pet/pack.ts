// Declarative pet pack contract shared by the plugin host (registration
// side) and the pet surface renderer. A pack is pure data: the .riv
// binary travels through the Go registry's asset channel, and the
// renderer only ever sees specs, never plugin code.
//
// Contract v2 drives characters through Rive's view model (data
// binding) instead of state machine inputs: every binding names a view
// model property, and state is expressed by writing values.

/** Property types a binding may target (Rive data binding types). */
export type PetPackBindingType =
  'enum' | 'string' | 'boolean' | 'number' | 'color' | 'trigger';

export interface PetPackBinding {
  type: PetPackBindingType;
  /** View model property name. */
  property: string;
  /**
   * Maps activity values (phase names, tool categories) to property
   * values. Used by enum and string bindings.
   */
  values?: Record<string, string>;
  /**
   * Property value written for activity values the table does not
   * cover (an unknown tool category, say). Empty means "leave the
   * property as it is".
   */
  fallback?: string;
}

export interface PetPackMeta {
  /** Render scale relative to the 240px pet canvas. */
  scale: number;
  /** Horizontal window speed in DIP/s while walking. */
  walkSpeed: number;
}

export interface PetPack {
  id: string;
  displayName: string;
  version: string;
  /** Set by the host to the registering plugin's id. */
  pluginId?: string;
  /** Artboard the character lives on. */
  artboard: string;
  /** Optional state machine; without one the pack only data-binds. */
  stateMachine?: string;
  /** Optional view model; empty uses the artboard's default instance. */
  viewModel?: string;
  /** builtin://<id> or plugin://<pluginId>/<path>. Empty keeps the
   *  placeholder renderer active until a .riv asset is delivered. */
  rivAsset?: string;
  meta: PetPackMeta;
  bindings: Record<string, PetPackBinding>;
}

/** Binding slots the renderer understands. */
export const PET_PHASE_SLOT = 'phase';
export const PET_TOOL_SLOT = 'tool';
export const PET_WALKING_SLOT = 'walking';
export const PET_SLEEPING_SLOT = 'sleeping';
/** Horizontal walk direction; the value is "left" or "right". */
export const PET_FACING_SLOT = 'facing';

/** Intent slots are "intent:<name>", one per one-shot reaction. */
export function petIntentSlot(intent: string): string {
  return `intent:${intent}`;
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
  return list.find((pack) => pack.id === BUILTIN_ASSISTANT_PACK_ID) ?? list[0];
}
