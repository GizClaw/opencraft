// Design-scale contract test.
//
// style.css promises that the ladders are "the only vocabulary": type
// and icon sizes come from tokens, surfaces pick a radius rung and an
// elevation rung, and colors come from the semantic palette. Nothing
// else enforces that, so a hand-written rem literal or a stray
// `rounded-xl` would silently reintroduce the drift the ladders were
// introduced to remove.
//
// The scan covers src/**/*.ts(x) minus test files (selectors inside
// tests are not styling) and minus this file.
import { readdirSync, readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const SRC_DIR = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');

// Raw type sizes: text-[13px], text-[0.7143rem], …
const RAW_TEXT_SIZE = /\btext-\[\d[^\]]*?(?:rem|px)\]/g;
// Raw icon sizes: size="0.8571rem" (the ICON ladder replaced these).
const RAW_ICON_SIZE = /\bsize="[\d.]+rem"/g;
// Tailwind's own radius rungs (the ladder: tight/control/card/full).
const FOREIGN_RADIUS =
  /\brounded(?:-(?:t|r|b|l|tl|tr|br|bl|ss|se|ee|es))?-(?:xs|sm|md|lg|xl|2xl|3xl|4xl)\b/g;
// Tailwind's own elevation rungs (the ladder: raised/popover/modal).
const FOREIGN_SHADOW = /\bshadow-(?:2xs|xs|sm|md|lg|xl|2xl)\b/g;
// The default palette: semantic colors live in style.css. `white` and
// `black` are deliberately absent here — they carry no shade number
// and are the two absolutes the tokens cannot express (text on an
// accent fill, modal scrims).
const FOREIGN_PALETTE =
  /\b(?:bg|text|border|ring|from|to|via|fill|stroke|divide|outline|decoration|placeholder|caret|accent)-(?:red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose|slate|gray|grey|zinc|neutral|stone)-\d{2,3}\b/g;

function sourceFiles(): string[] {
  const files: string[] = [];
  const walk = (dir: string) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const path = join(dir, entry.name);
      if (entry.isDirectory()) {
        walk(path);
        continue;
      }
      if (!/\.tsx?$/.test(entry.name)) continue;
      if (/\.test\.tsx?$/.test(entry.name)) continue;
      files.push(path);
    }
  };
  walk(SRC_DIR);
  return files;
}

/** offenders lists file:line for every match of one pattern. */
function offenders(pattern: RegExp): string[] {
  const found: string[] = [];
  for (const file of sourceFiles()) {
    const lines = readFileSync(file, 'utf8').split('\n');
    lines.forEach((line, i) => {
      pattern.lastIndex = 0;
      if (pattern.test(line)) {
        const rel = file.slice(SRC_DIR.length + 1);
        found.push(`${rel}:${i + 1}`);
      }
    });
  }
  return found;
}

describe('design scale', () => {
  it('sizes text and icons from the ladders, not raw rem literals', () => {
    expect({
      text: offenders(RAW_TEXT_SIZE),
      icons: offenders(RAW_ICON_SIZE),
    }).toEqual({ text: [], icons: [] });
  });

  it('picks radius and elevation rungs instead of Tailwind defaults', () => {
    expect({
      radius: offenders(FOREIGN_RADIUS),
      shadow: offenders(FOREIGN_SHADOW),
    }).toEqual({ radius: [], shadow: [] });
  });

  it('colors only from the semantic palette', () => {
    expect(offenders(FOREIGN_PALETTE)).toEqual([]);
  });

  it('defines the ladder the comments promise', () => {
    const css = readFileSync(join(SRC_DIR, 'style.css'), 'utf8');
    for (const token of [
      '--text-micro',
      '--text-label',
      '--radius-tight',
      '--radius-control',
      '--radius-card',
      '--shadow-raised',
      '--shadow-popover',
      '--shadow-modal',
    ]) {
      expect(css).toContain(token);
    }
    const icon = readFileSync(
      join(SRC_DIR, 'components', 'ui', 'icon.ts'),
      'utf8',
    );
    for (const rung of ['xs', 'sm', 'md', 'lg', 'xl', 'hero']) {
      expect(icon).toContain(`${rung}:`);
    }
  });
});
