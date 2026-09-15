import { describe, expect, it } from 'vitest';
import { measurePetGeometry } from './geometry';

/** A rect helper for the stubbed DOM nodes. */
function box(left: number, top: number, width: number, height: number) {
  return {
    left,
    top,
    width,
    height,
    right: left + width,
    bottom: top + height,
    x: left,
    y: top,
    toJSON: () => ({}),
  } as DOMRect;
}

/**
 * A 256x256 drawing surface (128 CSS px at DPR 2) whose painted pixels
 * are the given device-pixel window.
 */
function makeCanvas(painted?: {
  left: number;
  top: number;
  right: number;
  bottom: number;
}): HTMLCanvasElement {
  const canvas = document.createElement('canvas');
  canvas.width = 256;
  canvas.height = 256;
  canvas.getBoundingClientRect = (() =>
    box(20, 30, 128, 128)) as HTMLCanvasElement['getBoundingClientRect'];
  const context = {
    getImageData(x: number, y: number, width: number, height: number) {
      const data = new Uint8ClampedArray(width * height * 4);
      if (painted) {
        for (let row = 0; row < height; row++) {
          for (let column = 0; column < width; column++) {
            const px = x + column;
            const py = y + row;
            if (
              px >= painted.left &&
              px < painted.right &&
              py >= painted.top &&
              py < painted.bottom
            ) {
              data[(row * width + column) * 4 + 3] = 255;
            }
          }
        }
      }
      return { data, width, height };
    },
  };
  canvas.getContext = (() =>
    context) as unknown as HTMLCanvasElement['getContext'];
  return canvas;
}

function makeSurface(): HTMLElement {
  const surface = document.createElement('main');
  surface.getBoundingClientRect = (() =>
    box(0, 0, 168, 168)) as HTMLElement['getBoundingClientRect'];
  return surface;
}

describe('measurePetGeometry', () => {
  it('maps the drawn pixels back to window-relative DIP', () => {
    // Half the canvas is painted: 128x128 device px starting at (64, 64).
    const geometry = measurePetGeometry(
      makeCanvas({ left: 64, top: 64, right: 192, bottom: 192 }),
      makeSurface(),
    );
    expect(geometry).toEqual({
      canvas: { x: 20, y: 30, width: 128, height: 128 },
      art: { x: 52, y: 62, width: 64, height: 64 },
      measured: true,
    });
  });

  it('reports nothing while the window is empty', () => {
    expect(measurePetGeometry(makeCanvas(), makeSurface())).toBeNull();
  });

  it('reports nothing when the pixels cannot be read', () => {
    const canvas = makeCanvas({ left: 0, top: 0, right: 8, bottom: 8 });
    canvas.getContext = (() =>
      null) as unknown as HTMLCanvasElement['getContext'];
    expect(measurePetGeometry(canvas, makeSurface())).toBeNull();
    expect(measurePetGeometry(null, makeSurface())).toBeNull();
    expect(measurePetGeometry(makeCanvas(), null)).toBeNull();
  });
});
