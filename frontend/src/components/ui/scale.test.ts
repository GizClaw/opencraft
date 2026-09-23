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
// tests are not styling) and minus this file. src/styles/*.css -- the
// native-control skin -- is scanned by the skin rules below: that file
// is the one surface a future skin overrides, so it has to stay
// repaintable from tokens alone.
import { readdirSync, readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const SRC_DIR = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');
const STYLES_DIR = join(SRC_DIR, 'styles');

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
// Layer ladder: a floating surface names the rung it belongs to
// (`z-[var(--oc-z-popover)]`), never a literal — a number is how a menu
// ends up underneath the dialog it was opened from.
const RAW_Z = /\bz-\[?\d/g;
// Text ladder: quieter text picks the `dim` / `faint` rung instead of an
// alpha on a louder one. Deliberately a list of rungs, so Tailwind's
// `text-sm/6` size/leading pairs are not mistaken for alphas.
const ALPHA_TEXT =
  /\btext-(?:fg|dim|faint|accent|ok|warn|err|yolo|subagent)\/\d+/g;
// Filled absolutes: scrims are tokens (both rungs flip with the theme),
// so nothing may paint a surface `bg-black` / `bg-white`. As *text* on
// an accent fill they are still the one thing the palette cannot name.
const FILLED_ABSOLUTE = /\bbg-(?:black|white)\b/g;
// Color literals in components: the palette lives in style.css, and a
// hex tuned for one theme is invisible on the other.
const RAW_COLOR = /#[0-9a-fA-F]{3,8}\b|\brgba?\(|\bhsla?\(/g;
// Translucent surface fills. The surface ladder says panels and cards
// pick a rung, because an alpha reads differently on every parent; the
// one exception is a *floating* surface, which frosts on purpose so the
// content it covers stays legible. That is the whole rule: an alpha fill
// must come with the blur that makes it intentional.
const TRANSLUCENT_FILL = /\bbg-(?:panel|panel2|panel3|bg)\/\d+/g;

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

/** brokenLines lists file:line of every match whose line fails `ok`. */
function brokenLines(pattern: RegExp, ok: (line: string) => boolean): string[] {
  const found: string[] = [];
  for (const file of sourceFiles()) {
    const lines = readFileSync(file, 'utf8').split('\n');
    lines.forEach((line, i) => {
      pattern.lastIndex = 0;
      if (pattern.test(line) && !ok(line)) {
        const rel = file.slice(SRC_DIR.length + 1);
        found.push(`${rel}:${i + 1}`);
      }
    });
  }
  return found;
}

/** styleFiles lists src/styles/*.css, the skin-owned stylesheets. */
function styleFiles(): string[] {
  return readdirSync(STYLES_DIR)
    .filter((name) => name.endsWith('.css'))
    .map((name) => join(STYLES_DIR, name));
}

/** cssBrokenLines lists src/styles file:line of matches failing `ok`. */
function cssBrokenLines(
  pattern: RegExp,
  ok: (line: string) => boolean,
): string[] {
  const found: string[] = [];
  for (const file of styleFiles()) {
    const lines = readFileSync(file, 'utf8').split('\n');
    lines.forEach((line, i) => {
      pattern.lastIndex = 0;
      if (pattern.test(line) && !ok(line)) {
        const rel = file.slice(SRC_DIR.length + 1);
        found.push(`${rel}:${i + 1}`);
      }
    });
  }
  return found;
}

/**
 * domTitleAttributes lists file:line of `title=` attributes set on a
 * DOM element. `title` on a component is a prop (a dialog heading, an
 * empty state); on a DOM element the browser draws its own tooltip,
 * which ignores the theme, waits a second, and cannot be styled — the
 * app hints through `data-tip` and src/components/ui/Tooltip.tsx.
 */
function domTitleAttributes(): string[] {
  const found: string[] = [];
  for (const file of sourceFiles()) {
    const text = readFileSync(file, 'utf8');
    const pattern = /(?<![\w.$])title=/g;
    for (const match of text.matchAll(pattern)) {
      const at = match.index;
      let tag = at;
      while (
        tag >= 0 &&
        !(text[tag] === '<' && /[A-Za-z]/.test(text[tag + 1] ?? ''))
      ) {
        tag -= 1;
      }
      const name =
        tag < 0
          ? ''
          : (text.slice(tag + 1).match(/^[A-Za-z][\w.]*/)?.[0] ?? '');
      if (/^[a-z]/.test(name)) {
        const line = text.slice(0, at).split('\n').length;
        found.push(`${file.slice(SRC_DIR.length + 1)}:${line} <${name}>`);
      }
    }
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

  it('lifts surfaces through the layer ladder, not a literal z-index', () => {
    expect(offenders(RAW_Z)).toEqual([]);
    const css = readFileSync(join(SRC_DIR, 'style.css'), 'utf8');
    for (const rung of [
      'raised',
      'popover',
      'dialog',
      'overlay',
      'menu',
      'toast',
      'tooltip',
    ]) {
      expect(css).toContain(`--oc-z-${rung}:`);
    }
  });

  it('quiets text with a rung instead of an alpha', () => {
    expect(offenders(ALPHA_TEXT)).toEqual([]);
  });

  it('fills a surface with a rung; only a frosted float carries an alpha', () => {
    expect(
      brokenLines(TRANSLUCENT_FILL, (line) => line.includes('backdrop-blur')),
    ).toEqual([]);
    expect(offenders(FILLED_ABSOLUTE)).toEqual([]);
  });

  it('takes colors from the palette, not from literals', () => {
    expect(offenders(RAW_COLOR)).toEqual([]);
  });

  it('paints the control skin from tokens, not literals', () => {
    expect(styleFiles().length).toBeGreaterThan(0);
    // url() payloads are stripped before the color rule: the check
    // mark's data URI is the one shaped exception (white on an accent
    // fill), and everything else has to read from a token.
    expect(
      cssBrokenLines(RAW_COLOR, (line) => {
        const withoutUrls = line.replace(/url\([^)]*\)/g, '');
        RAW_COLOR.lastIndex = 0;
        return !RAW_COLOR.test(withoutUrls);
      }),
    ).toEqual([]);
  });

  it('keeps !important out of the control skin', () => {
    expect(cssBrokenLines(/!important/, () => false)).toEqual([]);
  });

  it('imports the control skin before any rule block', () => {
    const css = readFileSync(join(SRC_DIR, 'style.css'), 'utf8');
    const at = css.indexOf('./styles/controls.css');
    expect(at).toBeGreaterThan(-1);
    // A CSS @import after a declaration is ignored, and the skin would
    // silently fall off the page.
    expect(css.slice(0, at)).not.toContain('{');
  });

  it('hints through data-tip, not the native title attribute', () => {
    expect(domTitleAttributes()).toEqual([]);
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
