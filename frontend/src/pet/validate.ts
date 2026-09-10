import type { PetPack, PetPackBindingType } from './pack';

/**
 * Facts about a loaded .riv the pack is checked against. The renderer
 * builds them from the runtime (it is the only place with the file
 * loaded); tests build them from the shipped asset, so a pack can be
 * validated without a browser.
 */
export interface PetAssetFacts {
  artboards: string[];
  /** Artboard name -> state machine names. */
  stateMachines: Record<string, string[]>;
  /** Artboard name -> default view model name (empty when none). */
  defaultViewModel: Record<string, string>;
  /** View model name -> properties. */
  viewModels: Record<string, PetAssetProperty[]>;
  /** Enum name -> declared values. */
  enums: Record<string, string[]>;
}

export interface PetAssetProperty {
  name: string;
  /** Rive data binding type: string/boolean/number/color/enumType/trigger. */
  type: string;
  enumName?: string;
}

/** Runtime type each binding type must resolve to in the asset. */
const RUNTIME_TYPES: Record<PetPackBindingType, string[]> = {
  enum: ['enumType'],
  string: ['string'],
  boolean: ['boolean'],
  number: ['number', 'integer'],
  color: ['color'],
  trigger: ['trigger'],
};

/**
 * Checks one pack against one loaded asset and returns every mismatch
 * it found, in a stable order, as human-readable lines. An empty list
 * means the pack can drive the character.
 */
export function validatePack(pack: PetPack, facts: PetAssetFacts): string[] {
  const missing: string[] = [];
  if (!facts.artboards.includes(pack.artboard)) {
    // Nothing below can be checked without the artboard.
    return [`artboard "${pack.artboard}"`];
  }
  if (
    pack.stateMachine &&
    !(facts.stateMachines[pack.artboard] ?? []).includes(pack.stateMachine)
  ) {
    missing.push(`state machine "${pack.stateMachine}"`);
  }

  const viewModelName =
    pack.viewModel || facts.defaultViewModel[pack.artboard] || '';
  const properties = viewModelName
    ? facts.viewModels[viewModelName]
    : undefined;
  if (!properties) {
    missing.push(`view model "${viewModelName || '(default)'}"`);
    return missing;
  }

  // Sorted so the report reads the same way across runs and packs.
  for (const slot of Object.keys(pack.bindings).sort()) {
    const binding = pack.bindings[slot];
    const property = properties.find((p) => p.name === binding.property);
    if (!property) {
      missing.push(`binding "${slot}": property "${binding.property}"`);
      continue;
    }
    if (!RUNTIME_TYPES[binding.type]?.includes(property.type)) {
      missing.push(
        `binding "${slot}": property "${binding.property}" is ` +
          `${property.type}, not ${binding.type}`,
      );
      continue;
    }
    if (binding.type !== 'enum') continue;

    const enumName = property.enumName ?? '';
    const values = enumName ? facts.enums[enumName] : undefined;
    if (!values) {
      missing.push(`binding "${slot}": enum "${enumName}"`);
      continue;
    }
    for (const [key, value] of Object.entries(binding.values ?? {})) {
      if (!values.includes(value)) {
        missing.push(
          `binding "${slot}": value "${value}" (for "${key}") is not in ` +
            `enum "${enumName}"`,
        );
      }
    }
    if (binding.fallback && !values.includes(binding.fallback)) {
      missing.push(
        `binding "${slot}": fallback "${binding.fallback}" is not in ` +
          `enum "${enumName}"`,
      );
    }
  }
  return missing;
}

/** The pet window's report about the pack it mounted (wire DTO). */
export interface PetRuntimeStatus {
  pack_id: string;
  artboard: string;
  state_machine?: string;
  view_model?: string;
  ok: boolean;
  missing?: string[];
  error?: string;
  reported_at?: string;
}

/** Builds the wire status for one pack and one validation result. */
export function packRuntimeStatus(
  pack: PetPack,
  missing: string[],
): PetRuntimeStatus {
  return {
    pack_id: pack.id,
    artboard: pack.artboard,
    state_machine: pack.stateMachine,
    view_model: pack.viewModel,
    ok: missing.length === 0,
    missing: missing.length > 0 ? missing : undefined,
  };
}
