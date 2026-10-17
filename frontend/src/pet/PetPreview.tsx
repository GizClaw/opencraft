import { useEffect, useRef, useState } from 'react';
import { api } from '../lib/api';
import type { PetPack } from './pack';
import { base64ToArrayBuffer, createPetRive, type PetRiveHandle } from './rive';
import { toPetView } from './state';

/**
 * PetPreview renders one pack with the same Rive driver the pet window
 * uses, giving the settings page and plugin authors a live
 * preview of a character without opening a desktop window.
 */
export function PetPreview({ pack }: { pack: PetPack }) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  // The resize listener needs the live handle, and the handle is
  // created inside the async mount effect.
  const handleRef = useRef<PetRiveHandle | null>(null);
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
        handle = await createPetRive(canvas, base64ToArrayBuffer(base64), pack);
        if (cancelled) {
          handle.destroy();
          return;
        }
        handleRef.current = handle;
        handle.apply(toPetView(null));
      } catch (err) {
        setError(String(err));
      }
    })();
    return () => {
      cancelled = true;
      handleRef.current = null;
      handle?.destroy();
    };
  }, [pack]);

  // Keep the drawing surface sized with its box. The preview is a
  // settings-page card, so a window resize or a move to another display
  // changes its backing store; without this it keeps the device-pixel
  // size captured at mount and the character is drawn at the wrong
  // scale/offset. The pet window listens the same way.
  useEffect(() => {
    const onResize = () => handleRef.current?.resize();
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, []);

  return (
    <div className="pet-preview">
      <canvas ref={canvasRef} className="pet-canvas" />
      {error && <span className="pet-preview-error">{error}</span>}
    </div>
  );
}
