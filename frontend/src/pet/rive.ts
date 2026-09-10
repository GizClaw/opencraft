import type { PetPack } from './pack';
import type { PetView } from './state';
import {
  intentWriteForView,
  valueWritesForView,
  type PetWrite,
} from './riveBindings';
import {
  packRuntimeStatus,
  validatePack,
  type PetAssetFacts,
  type PetRuntimeStatus,
} from './validate';

/**
 * Thin driver over @rive-app/canvas. The module is dynamically
 * imported so the WASM runtime only loads for surfaces that actually
 * received a .riv pack.
 *
 * The character is driven through Rive's view model (data binding): the
 * pack names properties, the state machine reads them, and one-shot
 * intents are triggers. Nothing here touches state machine inputs —
 * contract v2 has none.
 */
export interface PetRiveHandle {
  /** Writes one surface state, firing an intent trigger only when the
   *  payload's intent sequence moved. */
  apply(view: PetView): void;
  /** Mount report for diagnostics (asset matches the pack?). */
  status(): PetRuntimeStatus;
  pause(): void;
  play(): void;
  resize(): void;
  destroy(): void;
}

export function base64ToArrayBuffer(base64: string): ArrayBuffer {
  const binary = atob(base64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes.buffer;
}

/**
 * Minimal shape of the runtime this module drives, so the value/trigger
 * writes can be tested against a fake without loading WASM.
 */
export interface ViewModelInstanceLike {
  string(path: string): { value: string } | null;
  enum(path: string): { value: string; values: string[] } | null;
  number(path: string): { value: number } | null;
  boolean(path: string): { value: boolean } | null;
  color(path: string): { value: number } | null;
  trigger(path: string): { trigger(): void } | null;
}

/**
 * Writes one resolved binding value to the instance of a loaded asset.
 * Value properties are only written when they actually change, which
 * keeps the state machine from re-entering transitions on every
 * broadcast.
 */
export function writeToInstance(
  instance: ViewModelInstanceLike,
  write: PetWrite,
): void {
  switch (write.type) {
    case 'trigger':
      instance.trigger(write.property)?.trigger();
      return;
    case 'boolean': {
      const property = instance.boolean(write.property);
      const value = Boolean(write.value);
      if (property && property.value !== value) property.value = value;
      return;
    }
    case 'number': {
      const property = instance.number(write.property);
      const value = Number(write.value);
      if (property && property.value !== value) property.value = value;
      return;
    }
    case 'color': {
      const property = instance.color(write.property);
      const value = Number(write.value);
      if (property && property.value !== value) property.value = value;
      return;
    }
    case 'enum': {
      const property = instance.enum(write.property);
      const value = String(write.value);
      if (property && property.value !== value) property.value = value;
      return;
    }
    default: {
      const property = instance.string(write.property);
      const value = String(write.value);
      if (property && property.value !== value) property.value = value;
    }
  }
}

/**
 * The slice of a loaded Rive instance the facts come from. Structural
 * so tests can feed it a fake and Node-side checks can feed the raw
 * runtime wrappers.
 */
export interface PetAssetRuntime {
  contents?: {
    artboards?: { name: string; stateMachines: { name: string }[] }[];
  };
  viewModelByIndex(index: number): {
    name: string;
    properties: { name: string; type: string; enumName?: string }[];
  } | null;
  defaultViewModel(): { name: string } | null;
  enums(): { name: string; values: string[] }[];
}

/**
 * Builds the asset facts the pack is validated against from a loaded
 * Rive instance. Only the loaded artboard's default view model can be
 * resolved by name from the runtime, so that is what the map carries;
 * an explicit pack.viewModel is looked up in the file's view models.
 */
export function factsFromRuntime(
  rive: PetAssetRuntime,
  artboardName: string,
): PetAssetFacts {
  const artboards = rive.contents?.artboards ?? [];
  const facts: PetAssetFacts = {
    artboards: artboards.map((artboard) => artboard.name),
    stateMachines: Object.fromEntries(
      artboards.map((artboard) => [
        artboard.name,
        (artboard.stateMachines ?? []).map((sm) => sm.name),
      ]),
    ),
    defaultViewModel: {},
    viewModels: {},
    enums: Object.fromEntries(
      rive.enums().map((dataEnum) => [dataEnum.name, dataEnum.values]),
    ),
  };
  for (let index = 0; ; index++) {
    const viewModel = rive.viewModelByIndex(index);
    if (!viewModel) break;
    facts.viewModels[viewModel.name] = viewModel.properties.map((property) => ({
      name: property.name,
      type: property.type,
      enumName: property.enumName,
    }));
  }
  const fallback = rive.defaultViewModel();
  if (fallback) facts.defaultViewModel[artboardName] = fallback.name;
  return facts;
}

export async function createPetRive(
  canvas: HTMLCanvasElement,
  bytes: ArrayBuffer,
  pack: PetPack,
): Promise<PetRiveHandle> {
  const mod = await import('@rive-app/canvas');
  const rive = new mod.Rive({
    canvas,
    buffer: bytes,
    artboard: pack.artboard,
    stateMachine: pack.stateMachine,
    autoplay: true,
    // The pack's view model instance is bound explicitly below; letting
    // the runtime auto-bind would bind the artboard's default instance
    // and ignore the pack's choice.
    autoBind: false,
  });

  await new Promise<void>((resolve, reject) => {
    let settled = false;
    const finish = (error?: Error) => {
      if (settled) return;
      settled = true;
      window.clearTimeout(timer);
      rive.off(mod.EventType.Load, onLoad);
      rive.off(mod.EventType.LoadError, onError);
      if (error) reject(error);
      else resolve();
    };
    const onLoad = () => finish();
    const onError = () =>
      finish(new Error(`pet: rive load failed for ${pack.id}`));
    const timer = window.setTimeout(
      () => finish(new Error(`pet: rive load timed out for ${pack.id}`)),
      10_000,
    );
    rive.on(mod.EventType.Load, onLoad);
    rive.on(mod.EventType.LoadError, onError);
  }).catch((err: unknown) => {
    // A failed or timed-out load still holds the canvas; release the
    // runtime so retrying the same canvas (pack switch, plugin reload)
    // does not stack a second instance on it.
    rive.cleanup();
    throw err;
  });

  rive.resizeDrawingSurfaceToCanvas();

  const facts = factsFromRuntime(
    {
      contents: rive.contents,
      viewModelByIndex: (index) => rive.viewModelByIndex(index),
      defaultViewModel: () => rive.defaultViewModel(),
      enums: () => rive.enums(),
    },
    pack.artboard,
  );

  // The artboard's default instance is what the pack contract means;
  // the named lookup only covers assets that expose one explicitly.
  const viewModelName =
    pack.viewModel || facts.defaultViewModel[pack.artboard] || '';
  const viewModel = viewModelName
    ? rive.viewModelByName(viewModelName)
    : rive.defaultViewModel();
  const instance =
    viewModel?.defaultInstance() ??
    viewModel?.instanceByName('Instance') ??
    viewModel?.instance() ??
    null;

  const missing = validatePack(pack, facts);
  if (!instance) {
    // Without an instance every write is dropped, so the character
    // would sit frozen while diagnostics claimed it was mounted.
    missing.push(`view model instance for "${viewModelName || '(default)'}"`);
  }
  const status = packRuntimeStatus(pack, missing);
  if (!status.ok) {
    // Report once, with the full list; the surface stays up and shows a
    // degraded marker instead of freezing on the last frame.
    console.error(
      `pet: pack ${pack.id} does not match the asset`,
      status.missing,
    );
  }

  if (instance) {
    rive.bindViewModelInstance(instance);
  }
  const properties = instance as ViewModelInstanceLike | null;

  let lastIntentSeq = 0;
  const apply = (view: PetView) => {
    if (!properties) return;
    for (const write of valueWritesForView(pack, view)) {
      writeToInstance(properties, write);
    }
    if (view.intentSeq === lastIntentSeq) return;
    lastIntentSeq = view.intentSeq;
    const intent = intentWriteForView(pack, view);
    if (intent) writeToInstance(properties, intent);
  };

  return {
    apply,
    status: () => status,
    pause: () => rive.pause(),
    play: () => rive.play(),
    resize: () => rive.resizeDrawingSurfaceToCanvas(),
    destroy: () => rive.cleanup(),
  };
}
