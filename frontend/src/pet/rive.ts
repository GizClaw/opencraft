import type { PetPack } from './pack';
import type { PetView } from './state';
import { bindingForView } from './riveBindings';

/**
 * Thin driver over @rive-app/canvas. The module is dynamically
 * imported so the WASM runtime only loads for surfaces that actually
 * received a .riv pack.
 */
export interface PetRiveHandle {
  apply(view: PetView): void;
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

interface InputLike {
  name: string;
  value: number | boolean;
  fire(): void;
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
    stateMachines: pack.stateMachine,
    autoplay: true,
  });

  await new Promise<void>((resolve, reject) => {
    let settled = false;
    const onLoad = () => {
      if (settled) return;
      settled = true;
      window.clearTimeout(timer);
      resolve();
    };
    const timer = window.setTimeout(() => {
      if (settled) return;
      settled = true;
      rive.off(mod.EventType.Load, onLoad);
      reject(new Error(`pet: rive load timed out for ${pack.id}`));
    }, 10_000);
    rive.on(mod.EventType.Load, onLoad);
  });

  rive.resizeDrawingSurfaceToCanvas();
  const inputs = new Map<string, InputLike>();
  for (const input of rive.stateMachineInputs(pack.stateMachine) ?? []) {
    inputs.set(input.name, input);
  }
  const activeBools = new Set<string>();

  const setBool = (name: string, value: boolean) => {
    const input = inputs.get(name);
    if (!input || typeof input.value !== 'boolean') return;
    input.value = value;
  };

  const apply = (view: PetView) => {
    const binding = bindingForView(pack, view);
    if (!binding) {
      for (const name of activeBools) setBool(name, false);
      activeBools.clear();
      return;
    }
    if (binding.type === 'trigger') {
      // Triggers fire from an exclusive base: release any held boolean
      // first, otherwise a stale "idle" input bounces the state machine
      // straight back out of the one-shot state it just entered.
      for (const name of activeBools) setBool(name, false);
      activeBools.clear();
      inputs.get(binding.name)?.fire();
      return;
    }
    const wanted = new Set([binding.name]);
    for (const name of activeBools) {
      if (!wanted.has(name)) setBool(name, false);
    }
    activeBools.clear();
    wanted.forEach((name) => {
      activeBools.add(name);
      setBool(name, true);
    });
  };

  return {
    apply,
    resize: () => rive.resizeDrawingSurfaceToCanvas(),
    destroy: () => rive.cleanup(),
  };
}
