// MOTION and the animation tokens in style.css are the same fact written
// twice: the stylesheet says how long an exit animation runs, MOTION says
// when the surface is unmounted. When they drift, a surface is torn down
// mid-fade (the toast animated 140ms while its timer ran 120ms) or a
// drawer inherits a neighbour's timer. This reads the stylesheet and
// compares the two.
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { MOTION } from './overlay';

const CSS = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), '../style.css'),
  'utf8',
);

// Every exit animation the app plays, the MOTION number that times its
// unmount, and the surface that plays it.
const EXITS: Array<{ token: string; ms: number; plays: string }> = [
  { token: '--animate-scrim-out', ms: MOTION.fast, plays: 'scrim' },
  { token: '--animate-panel-out', ms: MOTION.fast, plays: 'panels' },
  { token: '--animate-drawer-out', ms: MOTION.drawerOut, plays: 'drawer' },
  { token: '--animate-toast-out', ms: MOTION.toast, plays: 'toast' },
];

/** animationMs reads the duration one token declares: 130 from
 * `--animate-drawer-out: oc-drawer-out 130ms ease-in both;`. */
function animationMs(token: string): number {
  const declaration = new RegExp(`${token}\\s*:\\s*([^;]+);`).exec(CSS);
  if (declaration === null) throw new Error(`style.css has no ${token}`);
  const duration = /(\d+(?:\.\d+)?)ms/.exec(declaration[1]);
  if (duration === null) {
    throw new Error(`${token} declares no ms duration: ${declaration[1]}`);
  }
  return Number(duration[1]);
}

describe('MOTION', () => {
  it.each(EXITS)('unmounts the $plays after $token', ({ token, ms }) => {
    expect(animationMs(token)).toBe(ms);
  });

  // The other direction: a duration in the table that no exit animation
  // declares is a rule nobody plays. `base: 160` and `drawer: 180` were
  // that — the *entrance* durations, copied in and never read.
  it('holds no duration that no exit animation plays', () => {
    const exits = new Set(EXITS.map((exit) => exit.ms));
    expect(Object.values(MOTION).filter((ms) => !exits.has(ms))).toEqual([]);
  });
});
