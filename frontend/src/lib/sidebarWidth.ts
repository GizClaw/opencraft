// Sidebar width: the left column of the shell, and the arithmetic the
// resize handle on the seam runs on.
//
// The handle owns the seam line, so a width is always a whole number of
// pixels — a fractional column would land the hairline between two device
// pixels — and the bounds below are the ones the handle advertises to
// assistive tech through aria-valuemin/max. The stored key keeps the name
// it has always had; its value is a bare number of pixels.
export const SIDEBAR_MIN_WIDTH = 180;
export const SIDEBAR_MAX_WIDTH = 480;
export const SIDEBAR_DEFAULT_WIDTH = 240;
// One arrow press moves the seam a comfortable notch; Shift asks for the
// single pixel that is hard to land by dragging.
export const SIDEBAR_STEP = 8;
export const SIDEBAR_FINE_STEP = 1;

const SIDEBAR_WIDTH_KEY = 'oc.sidebarW';

// clampSidebarWidth rounds to whole pixels and keeps the seam inside the
// range the handle allows. A value that is not a finite number (a
// hand-edited entry, a NaN out of a pointer delta) falls back to the
// default rather than pinning the column to an edge.
export function clampSidebarWidth(width: number): number {
  if (!Number.isFinite(width)) return SIDEBAR_DEFAULT_WIDTH;
  return Math.min(
    Math.max(Math.round(width), SIDEBAR_MIN_WIDTH),
    SIDEBAR_MAX_WIDTH,
  );
}

// readSidebarWidth returns the stored width, or the default when the entry
// is missing or unreadable (storage disabled, private browsing).
export function readSidebarWidth(): number {
  try {
    const raw = window.localStorage.getItem(SIDEBAR_WIDTH_KEY);
    if (raw === null || raw.trim() === '') return SIDEBAR_DEFAULT_WIDTH;
    return clampSidebarWidth(Number(raw));
  } catch {
    return SIDEBAR_DEFAULT_WIDTH;
  }
}

// writeSidebarWidth mirrors the width for the next launch; a failed write
// only costs the remembered width, never the current one.
export function writeSidebarWidth(width: number): void {
  try {
    window.localStorage.setItem(
      SIDEBAR_WIDTH_KEY,
      String(clampSidebarWidth(width)),
    );
  } catch {
    // Storage disabled: the in-memory width still applies.
  }
}
