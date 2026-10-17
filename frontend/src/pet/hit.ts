/**
 * Pixel hit test for the pet window.
 *
 * The OS window is a transparent rectangle that keeps capturing the
 * mouse (Wails only exposes whole-window mouse ignoring), so the
 * surface itself has to decide whether a press landed on the character.
 * The Rive runtime paints the character into a 2D canvas, which makes
 * the drawn pixels readable: a press counts only when there are opaque
 * pixels under the cursor. The direct probe samples a small square
 * rather than a single pixel so an anti-aliased edge, a thin stroke or
 * a sub-pixel measurement error does not turn a grab into a miss.
 *
 * A transparent pixel is not automatically a miss: the shipped
 * character is a ring, and its middle is exactly where a user grabs it.
 * A transparent pixel whose transparent neighbourhood never reaches the
 * edge of the drawing surface is a hole inside the silhouette, so it
 * counts as the character too. The flood fill works for any shape and
 * any pose, which a fixed-radius probe cannot: the ring's hole is wider
 * than its stroke is thick.
 *
 * When the pixels cannot be read at all — a canvas a non-2D renderer
 * took over, or one nothing has painted yet — the test falls back to
 * the canvas rectangle. That is still narrower than the old
 * whole-window behaviour and keeps a mounted pet grabbable instead of
 * leaving a character nobody can drag.
 */

/** Alpha (0-255) a sampled pixel needs before it counts as the character. */
export const PET_HIT_ALPHA = 24;

/** Sample radius in device pixels; 5 reads an 11x11 patch. */
export const PET_HIT_PATCH = 5;

/** The part of a DOMRect the hit test needs. */
export interface PetHitRect {
  left: number;
  top: number;
  width: number;
  height: number;
}

/** The drawing surface size in device pixels (canvas.width/height). */
export interface PetSurfaceSize {
  width: number;
  height: number;
}

export interface PetDevicePoint {
  x: number;
  y: number;
}

/** One read of the drawing surface: alpha for every device pixel. */
export interface PetAlphaGrid {
  width: number;
  height: number;
  /** Alpha channel only, `width * height` entries. */
  alpha: Uint8ClampedArray;
}

/**
 * Reusable working buffers for one read-and-analyse pass. Reading the
 * surface and then walking it used to allocate ~340KB per pointer press
 * (the alpha copy, the flood fill's visited set and its stack); a
 * scratch set hands the same buffers to the next pass. The buffers
 * belong to whoever created the scratch, and a grid built from one
 * aliases them, so a grid must be consumed before the next read.
 */
export interface PetGridScratch {
  alpha: Uint8ClampedArray | null;
  seen: Uint8Array | null;
  pending: Int32Array | null;
}

/** Creates an empty (lazily sized) scratch set. */
export function createPetGridScratch(): PetGridScratch {
  return { alpha: null, seen: null, pending: null };
}

// alphaBuffer/seenBuffer/pendingBuffer return a buffer of `size` from the
// scratch set, growing (and remembering) it when the surface changed size.
function alphaBuffer(
  scratch: PetGridScratch | null,
  size: number,
): Uint8ClampedArray {
  const reused = scratch?.alpha;
  if (reused && reused.length === size) return reused;
  const buffer = new Uint8ClampedArray(size);
  if (scratch) scratch.alpha = buffer;
  return buffer;
}

function seenBuffer(scratch: PetGridScratch | null, size: number): Uint8Array {
  const reused = scratch?.seen;
  if (reused && reused.length === size) return reused;
  const buffer = new Uint8Array(size);
  if (scratch) scratch.seen = buffer;
  return buffer;
}

function pendingBuffer(
  scratch: PetGridScratch | null,
  size: number,
): Int32Array {
  const reused = scratch?.pending;
  if (reused && reused.length === size) return reused;
  const buffer = new Int32Array(size);
  if (scratch) scratch.pending = buffer;
  return buffer;
}

/** Bounding box of the drawn pixels, in device pixels; right/bottom are
 *  exclusive. */
export interface PetAlphaBounds {
  left: number;
  top: number;
  right: number;
  bottom: number;
}

/** True when a client point falls inside a rectangle. */
export function insidePetRect(
  rect: PetHitRect,
  clientX: number,
  clientY: number,
): boolean {
  return (
    clientX >= rect.left &&
    clientX < rect.left + rect.width &&
    clientY >= rect.top &&
    clientY < rect.top + rect.height
  );
}

/**
 * Maps a client point onto the drawing surface's device-pixel grid.
 * The Rive runtime sizes the backing store to CSS pixels times the
 * device pixel ratio, so a pointer position only addresses real pixels
 * after the same transformation. Returns null for a degenerate surface.
 */
export function devicePixelPoint(
  rect: PetHitRect,
  surface: PetSurfaceSize,
  clientX: number,
  clientY: number,
): PetDevicePoint | null {
  if (
    rect.width <= 0 ||
    rect.height <= 0 ||
    surface.width <= 0 ||
    surface.height <= 0
  ) {
    return null;
  }
  return {
    x: ((clientX - rect.left) / rect.width) * surface.width,
    y: ((clientY - rect.top) / rect.height) * surface.height,
  };
}

/**
 * Reads the whole drawing surface. Null means the pixels cannot be
 * read: a canvas a non-2D renderer owns, or one that is not sized yet.
 * Passing a scratch set reuses its alpha buffer for the copy (the
 * ImageData the browser hands back is allocated per call either way).
 */
export function readAlphaGrid(
  canvas: HTMLCanvasElement,
  scratch: PetGridScratch | null = null,
): PetAlphaGrid | null {
  let context: CanvasRenderingContext2D | null = null;
  try {
    context = canvas.getContext('2d');
  } catch {
    return null;
  }
  if (!context) return null;
  const { width, height } = canvas;
  if (width <= 0 || height <= 0) return null;

  try {
    const { data } = context.getImageData(0, 0, width, height);
    // A reused buffer is written end to end below, so it never carries
    // pixels over from the previous read.
    const alpha = alphaBuffer(scratch, width * height);
    for (let index = 0, offset = 3; index < alpha.length; index++) {
      alpha[index] = data[offset];
      offset += 4;
    }
    return { width, height, alpha };
  } catch {
    // A tainted or non-readable surface is not an error worth breaking
    // a click over; the caller falls back to the canvas rectangle.
    return null;
  }
}

/**
 * Largest alpha in the square patch around a device pixel, clamped to
 * the grid so a press near the edge still reads real pixels.
 */
export function patchMaxAlpha(
  grid: PetAlphaGrid,
  x: number,
  y: number,
  radius: number = PET_HIT_PATCH,
): number {
  const size = radius * 2 + 1;
  const width = Math.min(size, grid.width);
  const height = Math.min(size, grid.height);
  const left = clampInt(Math.round(x) - radius, 0, grid.width - width);
  const top = clampInt(Math.round(y) - radius, 0, grid.height - height);
  let max = 0;
  for (let row = 0; row < height; row++) {
    const offset = (top + row) * grid.width + left;
    for (let column = 0; column < width; column++) {
      const value = grid.alpha[offset + column];
      if (value > max) max = value;
    }
  }
  return max;
}

/**
 * Smallest box that contains every pixel above the hit threshold, or
 * null when the surface is empty. The pet window reports this box to Go
 * so placement anchors on the character instead of on the transparent
 * stage.
 */
export function alphaBounds(
  grid: PetAlphaGrid,
  threshold: number = PET_HIT_ALPHA,
): PetAlphaBounds | null {
  const { width, height, alpha } = grid;
  let left = width;
  let top = height;
  let right = -1;
  let bottom = -1;
  for (let y = 0; y < height; y++) {
    const row = y * width;
    for (let x = 0; x < width; x++) {
      if (alpha[row + x] <= threshold) continue;
      if (x < left) left = x;
      if (x > right) right = x;
      if (y < top) top = y;
      if (y > bottom) bottom = y;
    }
  }
  if (right < 0) return null;
  return { left, top, right: right + 1, bottom: bottom + 1 };
}

/**
 * True when a transparent pixel is a hole inside the drawn silhouette:
 * flooding its transparent neighbours never reaches the edge of the
 * drawing surface. A pixel that is painted, or one whose neighbours
 * reach the edge (the empty margin around the character), is not a hole.
 * The flood fill reuses the scratch buffers when one is passed, and
 * expands its four neighbours through explicit indices instead of an
 * array per visited pixel.
 */
export function enclosedByDrawable(
  grid: PetAlphaGrid,
  x: number,
  y: number,
  threshold: number = PET_HIT_ALPHA,
  scratch: PetGridScratch | null = null,
): boolean {
  const { width, height, alpha } = grid;
  if (x < 0 || y < 0 || x >= width || y >= height) return false;
  const start = y * width + x;
  if (alpha[start] > threshold) return false;

  const seen = seenBuffer(scratch, width * height);
  // The stack is index-addressed and re-zeroed by `count`, but the
  // visited set has to start empty.
  seen.fill(0);
  const pending = pendingBuffer(scratch, width * height);
  let count = 0;
  seen[start] = 1;
  pending[count++] = start;
  while (count > 0) {
    const index = pending[--count];
    const column = index % width;
    const row = (index - column) / width;
    // Reaching the edge means the fill is connected to the empty space
    // around the character, so the press missed it.
    if (column === 0 || row === 0 || column === width - 1 || row === height - 1)
      return false;
    // The edge check above keeps every neighbour in range.
    const left = index - 1;
    if (!seen[left] && alpha[left] <= threshold) {
      seen[left] = 1;
      pending[count++] = left;
    }
    const right = index + 1;
    if (!seen[right] && alpha[right] <= threshold) {
      seen[right] = 1;
      pending[count++] = right;
    }
    const up = index - width;
    if (!seen[up] && alpha[up] <= threshold) {
      seen[up] = 1;
      pending[count++] = up;
    }
    const down = index + width;
    if (!seen[down] && alpha[down] <= threshold) {
      seen[down] = 1;
      pending[count++] = down;
    }
  }
  return true;
}

// One scratch set is kept for the pointer path: a press reads the
// surface and walks it inside this one call, so the buffers never
// outlive the grid they describe.
const hitScratch = createPetGridScratch();

/**
 * Decides whether a pointer press on the pet window landed on the
 * drawn character — painted pixels and the holes between them. Points
 * outside the drawing surface never hit; a surface whose pixels cannot
 * be read hits anywhere inside it.
 */
export function hitTestPet(
  canvas: HTMLCanvasElement | null,
  clientX: number,
  clientY: number,
): boolean {
  if (!canvas) return false;
  const rect = canvas.getBoundingClientRect();
  if (!insidePetRect(rect, clientX, clientY)) return false;
  const point = devicePixelPoint(rect, canvas, clientX, clientY);
  if (!point) return true;
  const grid = readAlphaGrid(canvas, hitScratch);
  if (!grid) return true;
  const x = clampInt(Math.round(point.x), 0, grid.width - 1);
  const y = clampInt(Math.round(point.y), 0, grid.height - 1);
  if (patchMaxAlpha(grid, x, y) > PET_HIT_ALPHA) return true;
  return enclosedByDrawable(grid, x, y, PET_HIT_ALPHA, hitScratch);
}

function clampInt(value: number, min: number, max: number): number {
  if (max < min) return min;
  if (value < min) return min;
  if (value > max) return max;
  return value;
}
