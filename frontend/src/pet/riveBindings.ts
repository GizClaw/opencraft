import type { PetPack, PetPackBinding } from './pack';
import type { PetView } from './state';

/**
 * Resolves the Rive binding for one surface state. Tool states first
 * try the concrete category (tool:file), then the wildcard (tool:*),
 * then the bare phase key. Every other phase maps by its phase name.
 */
export function bindingForView(
  pack: PetPack,
  view: PetView,
): PetPackBinding | undefined {
  if (view.intent) {
    const intentBinding = pack.bindings[`intent:${view.intent}`];
    if (intentBinding) return intentBinding;
  }
  // While the window physically walks, prefer the walk input over the
  // idle pose. Active work states still win so the pet does not walk
  // while typing or running tools.
  if (view.phase === 'idle' && view.walking) {
    return pack.bindings['walk'] ?? pack.bindings['idle'];
  }
  const direct = pack.bindings[view.phase];
  if (view.phase !== 'tool' || !view.toolCategory) {
    return direct;
  }
  return (
    pack.bindings[`tool:${view.toolCategory}`] ??
    pack.bindings['tool:*'] ??
    direct
  );
}
