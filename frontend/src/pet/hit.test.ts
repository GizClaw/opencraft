import { describe, expect, it } from 'vitest';
import {
  PET_HIT_PATCH,
  devicePixelPoint,
  enclosedByDrawable,
  hitTestPet,
  insidePetRect,
  patchMaxAlpha,
  readAlphaGrid,
  type PetAlphaGrid,
} from './hit';

const RECT = { left: 20, top: 20, width: 128, height: 128 };

/** A canvas with a fixed CSS box and a backing store `dpr` times as big. */
function makeCanvas(dpr = 1): HTMLCanvasElement {
  const element = document.createElement('canvas');
  element.width = RECT.width * dpr;
  element.height = RECT.height * dpr;
  element.getBoundingClientRect = (() =>
    ({
      ...RECT,
      right: RECT.left + RECT.width,
      bottom: RECT.top + RECT.height,
      x: RECT.left,
      y: RECT.top,
      toJSON: () => ({}),
    }) as DOMRect) as HTMLCanvasElement['getBoundingClientRect'];
  return element;
}

/**
 * Installs readable pixels on a canvas: alpha comes from `alphaAt`
 * (device pixel coordinates), the colour channels stay at maximum so a
 * test can prove only the alpha channel is consulted.
 */
function withAlpha(
  canvas: HTMLCanvasElement,
  alphaAt: (x: number, y: number) => number,
): void {
  const context = {
    getImageData(x: number, y: number, width: number, height: number) {
      const data = new Uint8ClampedArray(width * height * 4);
      for (let row = 0; row < height; row++) {
        for (let column = 0; column < width; column++) {
          const offset = (row * width + column) * 4;
          data[offset] = 255;
          data[offset + 1] = 255;
          data[offset + 2] = 255;
          data[offset + 3] = alphaAt(x + column, y + row);
        }
      }
      return { data, width, height };
    },
  };
  canvas.getContext = (() =>
    context) as unknown as HTMLCanvasElement['getContext'];
}

/** Alpha grid built from a predicate, no canvas involved. */
function gridFrom(
  alphaAt: (x: number, y: number) => number,
  width = 128,
  height = 128,
): PetAlphaGrid {
  const alpha = new Uint8ClampedArray(width * height);
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) alpha[y * width + x] = alphaAt(x, y);
  }
  return { width, height, alpha };
}

/**
 * The shipped character's silhouette, measured from the real asset: a
 * ring centred at (64, 72) whose hole (48px wide) is wider than the
 * stroke band (17px) is thick.
 */
function shippedRing(x: number, y: number): number {
  const dx = Math.abs(x - 64);
  const dy = Math.abs(y - 72);
  if (Math.max(dx, dy) > 41) return 0;
  if (dx <= 24 && dy <= 30) return 0;
  return 255;
}

describe('pet hit geometry', () => {
  it('treats the rectangle as half-open', () => {
    expect(insidePetRect(RECT, 20, 20)).toBe(true);
    expect(insidePetRect(RECT, 84, 84)).toBe(true);
    expect(insidePetRect(RECT, 147.9, 147.9)).toBe(true);
    expect(insidePetRect(RECT, 148, 100)).toBe(false);
    expect(insidePetRect(RECT, 100, 148)).toBe(false);
    expect(insidePetRect(RECT, 19.9, 100)).toBe(false);
  });

  it('maps client points onto the device-pixel grid', () => {
    expect(devicePixelPoint(RECT, { width: 128, height: 128 }, 20, 20)).toEqual(
      {
        x: 0,
        y: 0,
      },
    );
    // Retina: the backing store is twice the CSS box.
    expect(devicePixelPoint(RECT, { width: 256, height: 256 }, 84, 84)).toEqual(
      {
        x: 128,
        y: 128,
      },
    );
  });

  it('reports no point for a degenerate surface', () => {
    expect(
      devicePixelPoint(RECT, { width: 0, height: 128 }, 84, 84),
    ).toBeNull();
    expect(
      devicePixelPoint(
        { ...RECT, width: 0 },
        { width: 128, height: 128 },
        84,
        84,
      ),
    ).toBeNull();
  });
});

describe('readAlphaGrid', () => {
  it('keeps the alpha channel and drops the colour channels', () => {
    const canvas = makeCanvas();
    withAlpha(canvas, (x, y) => (x === 3 && y === 4 ? 200 : 0));
    const grid = readAlphaGrid(canvas);
    expect(grid).not.toBeNull();
    expect(grid?.width).toBe(128);
    expect(grid?.height).toBe(128);
    expect(grid?.alpha[4 * 128 + 3]).toBe(200);
    expect(grid?.alpha[0]).toBe(0);
  });

  it('reports null when the pixels cannot be read', () => {
    const plain = makeCanvas();
    plain.getContext = (() =>
      null) as unknown as HTMLCanvasElement['getContext'];
    expect(readAlphaGrid(plain)).toBeNull();

    const broken = makeCanvas();
    broken.getContext = (() =>
      ({
        getImageData: () => {
          throw new Error('tainted');
        },
      }) as unknown as CanvasRenderingContext2D) as unknown as HTMLCanvasElement['getContext'];
    expect(readAlphaGrid(broken)).toBeNull();

    const unsized = makeCanvas();
    unsized.width = 0;
    withAlpha(unsized, () => 255);
    expect(readAlphaGrid(unsized)).toBeNull();
  });
});

describe('patchMaxAlpha', () => {
  it('reports the largest alpha inside the patch', () => {
    const grid = gridFrom((x, y) => (x === 66 && y === 64 ? 200 : 0));
    expect(patchMaxAlpha(grid, 64, 64)).toBe(200);
    expect(patchMaxAlpha(grid, 64 + PET_HIT_PATCH * 2, 64)).toBe(0);
  });

  it('clamps the patch to the grid instead of reading past the edge', () => {
    const corner = gridFrom((x, y) => (x === 5 && y === 5 ? 180 : 0));
    expect(patchMaxAlpha(corner, 0, 0)).toBe(180);
    // Outside the window the corner probe is clamped to.
    const away = gridFrom((x, y) => (x === 20 && y === 20 ? 180 : 0));
    expect(patchMaxAlpha(away, 0, 0)).toBe(0);
  });

  it('reads a grid smaller than the patch', () => {
    const tiny = gridFrom((x, y) => (x === 1 && y === 1 ? 90 : 0), 3, 3);
    expect(patchMaxAlpha(tiny, 1, 1)).toBe(90);
  });
});

describe('enclosedByDrawable', () => {
  const grid = gridFrom(shippedRing);

  it('treats the hole of a ring as part of the character', () => {
    expect(enclosedByDrawable(grid, 64, 64)).toBe(true);
    // The hole is 48px wide, so a fixed-radius probe cannot fill it;
    // every point of it still has to count.
    expect(enclosedByDrawable(grid, 41, 64)).toBe(true);
    expect(enclosedByDrawable(grid, 87, 70)).toBe(true);
  });

  it('rejects the empty margin around the character', () => {
    expect(enclosedByDrawable(grid, 6, 6)).toBe(false);
    expect(enclosedByDrawable(grid, 64, 6)).toBe(false);
    expect(enclosedByDrawable(grid, 120, 64)).toBe(false);
  });

  it('rejects painted pixels and points outside the surface', () => {
    expect(enclosedByDrawable(grid, 64, 40)).toBe(false);
    expect(enclosedByDrawable(grid, -1, 64)).toBe(false);
    expect(enclosedByDrawable(grid, 128, 64)).toBe(false);
  });

  it('rejects a hole that is open to the outside', () => {
    // A ring cracked by a channel: the fill escapes to the margin.
    const cracked = gridFrom((x, y) =>
      x === 64 && y <= 60 ? 0 : shippedRing(x, y),
    );
    expect(enclosedByDrawable(cracked, 64, 64)).toBe(false);
  });
});

describe('hitTestPet', () => {
  it('never hits outside the drawing surface', () => {
    const canvas = makeCanvas();
    let reads = 0;
    withAlpha(canvas, () => {
      reads += 1;
      return 255;
    });
    expect(hitTestPet(canvas, 10, 10)).toBe(false);
    expect(reads).toBe(0);
  });

  it('hits the drawn ring, its hole, and nothing else', () => {
    const canvas = makeCanvas();
    withAlpha(canvas, shippedRing);
    // Canvas coordinates are the client point minus the rect origin.
    expect(hitTestPet(canvas, 20 + 64, 20 + 40)).toBe(true);
    expect(hitTestPet(canvas, 20 + 64, 20 + 64)).toBe(true);
    expect(hitTestPet(canvas, 20 + 6, 20 + 6)).toBe(false);
  });

  it('follows the device-pixel ratio when probing', () => {
    const canvas = makeCanvas(2);
    withAlpha(canvas, (x, y) => shippedRing(x / 2, y / 2));
    expect(hitTestPet(canvas, 20 + 64, 20 + 64)).toBe(true);
    expect(hitTestPet(canvas, 20 + 6, 20 + 6)).toBe(false);
  });

  it('falls back to the canvas rectangle when pixels are unreadable', () => {
    const canvas = makeCanvas();
    canvas.getContext = (() =>
      null) as unknown as HTMLCanvasElement['getContext'];
    expect(hitTestPet(canvas, 84, 84)).toBe(true);
    expect(hitTestPet(canvas, 10, 10)).toBe(false);
  });

  it('misses when there is no canvas at all', () => {
    expect(hitTestPet(null, 84, 84)).toBe(false);
  });
});
