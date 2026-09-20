// The chart palette, resolved through CSS variables rather than JS theme
// branching.
//
// Four surfaces used to carry their own hardcoded hex list (usage hero,
// usage chart, metrics charts, graph nodes); they disagreed with each
// other, and all of them were tuned for the dark theme, so the light theme
// drew pale-on-white lines. style.css owns the palette now (--color-series-*
// plus the grid/axis tokens) and `.theme-light` overrides it, which means a
// theme flip repaints every chart without a single component knowing about
// it: SVG presentation attributes accept var(), and the browser re-resolves
// them when the class on <html> changes.
export const SERIES = [
  'var(--color-series-1)',
  'var(--color-series-2)',
  'var(--color-series-3)',
  'var(--color-series-4)',
  'var(--color-series-5)',
] as const;

/** Grid lines and axis ticks, for recharts' stroke/fill props. */
export const CHART_GRID = 'var(--color-chart-grid)';
export const CHART_AXIS = 'var(--color-chart-axis)';

/** Upload/input volume. */
export const SERIES_INPUT = SERIES[0];
/** Generated output. */
export const SERIES_OUTPUT = SERIES[1];
/** Cache writes (billed like input, but a separate fact). */
export const SERIES_CACHE_WRITE = SERIES[2];
/** Cache reads — the cheap half of the input column. */
export const SERIES_CACHE_READ = SERIES[3];
/** Reasoning / thinking tokens. */
export const SERIES_REASONING = SERIES[4];

/** Series color by index, wrapping like the old per-chart palettes did. */
export function seriesColor(index: number): string {
  return SERIES[index % SERIES.length];
}
