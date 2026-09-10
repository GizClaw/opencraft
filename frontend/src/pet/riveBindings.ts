import type { PetPack, PetPackBinding, PetPackBindingType } from './pack';
import {
  PET_FACING_SLOT,
  PET_PHASE_SLOT,
  PET_SLEEPING_SLOT,
  PET_TOOL_SLOT,
  PET_WALKING_SLOT,
  petIntentSlot,
} from './pack';
import type { PetView } from './state';

/**
 * One view model write: a property, its declared type and the value the
 * renderer should store. Mutual exclusion between states comes from the
 * value itself, so writing a new value is all that is needed to move a
 * character from one pose to another.
 */
export interface PetWrite {
  property: string;
  type: PetPackBindingType;
  value: string | number | boolean;
}

/**
 * Resolves an activity value through a binding's value table.
 *
 * Order: explicit table entry, then the binding's fallback, then — for
 * plain string properties, which have no enum vocabulary to check
 * against — the raw activity value. Enum bindings without a matching
 * entry and without a fallback resolve to nothing, so the property
 * keeps its last value instead of being written with a value the asset
 * does not know.
 */
export function resolveBindingValue(
  binding: PetPackBinding,
  key: string,
): string | undefined {
  const mapped = binding.values?.[key];
  if (mapped !== undefined) return mapped;
  if (binding.fallback) return binding.fallback;
  if (binding.type === 'string') return key;
  return undefined;
}

/**
 * Value writes for one surface state: the phase/tool/walking/facing/
 * sleeping properties, in a stable order. Triggers are not included —
 * the caller fires those only when the intent actually changed (see
 * {@link intentWriteForView}).
 */
export function valueWritesForView(pack: PetPack, view: PetView): PetWrite[] {
  const writes: PetWrite[] = [];

  const phase = pack.bindings[PET_PHASE_SLOT];
  if (phase) {
    const value = resolveBindingValue(phase, view.phase);
    if (value !== undefined) {
      writes.push({ property: phase.property, type: phase.type, value });
    }
  }

  // The tool property is only meaningful while the character is in the
  // tool phase; elsewhere the phase property already picked the pose.
  const tool = pack.bindings[PET_TOOL_SLOT];
  if (tool && view.phase === 'tool') {
    const value = resolveBindingValue(tool, view.toolCategory ?? '');
    if (value !== undefined) {
      writes.push({ property: tool.property, type: tool.type, value });
    }
  }

  const walking = pack.bindings[PET_WALKING_SLOT];
  if (walking) {
    writes.push({
      property: walking.property,
      type: walking.type,
      value: Boolean(view.walking),
    });
  }

  // Facing is derived from the walk direction: the rover holds the last
  // value while the pet stands still, and reports nothing before the
  // first step, so an unbound pack or a value-less view writes nothing
  // and the property keeps its current state.
  const facing = pack.bindings[PET_FACING_SLOT];
  if (facing && view.facing) {
    const value = resolveBindingValue(facing, view.facing);
    if (value !== undefined) {
      writes.push({ property: facing.property, type: facing.type, value });
    }
  }

  const sleeping = pack.bindings[PET_SLEEPING_SLOT];
  if (sleeping) {
    writes.push({
      property: sleeping.property,
      type: sleeping.type,
      value: Boolean(view.sleeping),
    });
  }

  return writes;
}

/**
 * The trigger write for one surface state's intent, if the pack binds
 * it. Intents without a binding (welcome greets through the bubble, nap
 * is expressed by the sleeping flag) simply do not fire anything.
 */
export function intentWriteForView(
  pack: PetPack,
  view: PetView,
): PetWrite | undefined {
  if (!view.intent) return undefined;
  const binding = pack.bindings[petIntentSlot(view.intent)];
  if (!binding || binding.type !== 'trigger') return undefined;
  return { property: binding.property, type: binding.type, value: 0 };
}
