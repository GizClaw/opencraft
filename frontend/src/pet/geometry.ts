import { alphaBounds, readAlphaGrid } from './hit';

/** A rectangle in window-relative DIP. */
export interface PetRectBox {
  x: number;
  y: number;
  width: number;
  height: number;
}

/**
 * Where the renderer drew the character inside the pet window. The
 * window is a transparent 168px stage; Go anchors the dock position and
 * the "stand on the main window" spot on these boxes, so the numbers
 * have to describe the character, not the stage.
 */
export interface PetWindowGeometry {
  /** The Rive canvas inside the window. */
  canvas: PetRectBox;
  /** The drawn character's bounding box inside the window. */
  art: PetRectBox;
  /** Whether the art box came from real pixels. */
  measured: boolean;
}

/**
 * Measures the drawn character inside the window. Returns null while
 * nothing is painted (before the pack mounts, or after a failed mount):
 * Go keeps its own default layout until the surface has something real
 * to report.
 */
export function measurePetGeometry(
  canvas: HTMLCanvasElement | null,
  surface: HTMLElement | null,
): PetWindowGeometry | null {
  if (!canvas || !surface) return null;
  const grid = readAlphaGrid(canvas);
  if (!grid) return null;
  const bounds = alphaBounds(grid);
  if (!bounds) return null;

  const surfaceRect = surface.getBoundingClientRect();
  const canvasRect = canvas.getBoundingClientRect();
  if (canvasRect.width <= 0 || canvasRect.height <= 0) return null;
  const canvasBox: PetRectBox = {
    x: canvasRect.left - surfaceRect.left,
    y: canvasRect.top - surfaceRect.top,
    width: canvasRect.width,
    height: canvasRect.height,
  };
  // Device pixels back to CSS pixels: the artboard is drawn at the
  // canvas' backing-store resolution, which the device pixel ratio
  // scales up.
  const scaleX = canvasBox.width / grid.width;
  const scaleY = canvasBox.height / grid.height;
  return {
    canvas: roundBox(canvasBox),
    art: roundBox({
      x: canvasBox.x + bounds.left * scaleX,
      y: canvasBox.y + bounds.top * scaleY,
      width: (bounds.right - bounds.left) * scaleX,
      height: (bounds.bottom - bounds.top) * scaleY,
    }),
    measured: true,
  };
}

/**
 * The binding decodes into integer DIP fields, so a fractional value
 * would fail the call outright.
 */
function roundBox(box: PetRectBox): PetRectBox {
  return {
    x: Math.round(box.x),
    y: Math.round(box.y),
    width: Math.round(box.width),
    height: Math.round(box.height),
  };
}
