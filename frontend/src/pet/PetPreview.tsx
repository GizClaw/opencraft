import { useEffect, useRef, useState } from 'react';
import { api } from '../lib/api';
import type { PetPack } from './pack';
import {
  base64ToArrayBuffer,
  createPetRive,
  type PetRiveHandle,
} from './rive';
import { toPetView } from './state';

/**
 * PetPreview renders one pack with the same Rive driver the roaming pet
 * window uses, giving the settings page and plugin authors a live
 * preview of a character without opening a desktop window.
 */
export function PetPreview({ pack }: { pack: PetPack }) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    let handle: PetRiveHandle | null = null;
    void (async () => {
      try {
        if (!pack.rivAsset) return;
        const base64 = await api.petPackAsset(pack.rivAsset);
        const canvas = canvasRef.current;
        if (!canvas || cancelled) return;
        handle = await createPetRive(
          canvas,
          base64ToArrayBuffer(base64),
          pack,
        );
        if (cancelled) {
          handle.destroy();
          return;
        }
        handle.apply(toPetView(null));
      } catch (err) {
        setError(String(err));
      }
    })();
    return () => {
      cancelled = true;
      handle?.destroy();
    };
  }, [pack]);

  return (
    <div className="pet-preview">
      <canvas ref={canvasRef} className="pet-canvas" />
      {error && <span className="pet-preview-error">{error}</span>}
    </div>
  );
}
