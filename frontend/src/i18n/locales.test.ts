// Locale contract test.
//
// The locale files are hand-edited and nothing else checks them, so a
// key added to one language only (or a typo in a `t('…')` call) ships
// silently: the user sees the raw key or the wrong language. These
// checks are the cheap half of that problem — key sets, placeholders,
// and the keys the source actually asks for.
//
// Deliberately not checked: whether every key in the files is still
// used. Many keys are resolved dynamically (`t(`git.check_${status}`)`,
// `labelKey` fields on option lists), so an unused-key check would be
// mostly false positives.
import { readFileSync, readdirSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import en from './locales/en.json';
import zh from './locales/zh.json';

const SRC_DIR = resolve(dirname(fileURLToPath(import.meta.url)), '..');

/** flatten turns the nested locale documents into dotted keys. */
function flatten(
  node: Record<string, unknown>,
  prefix = '',
): Map<string, string> {
  const out = new Map<string, string>();
  for (const [key, value] of Object.entries(node)) {
    const path = prefix ? `${prefix}.${key}` : key;
    if (value && typeof value === 'object') {
      for (const [k, v] of flatten(value as Record<string, unknown>, path)) {
        out.set(k, v);
      }
      continue;
    }
    out.set(path, String(value));
  }
  return out;
}

/** placeholders returns the {{name}} placeholders one string uses. */
function placeholders(text: string): string[] {
  return [...text.matchAll(/\{\{\s*([\w.]+)\s*\}\}/g)].map((m) => m[1]).sort();
}

/** sourceFiles lists the frontend sources, minus tests and the locales. */
function sourceFiles(): string[] {
  const files: string[] = [];
  const walk = (dir: string) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const path = join(dir, entry.name);
      if (entry.isDirectory()) {
        if (entry.name === 'i18n') continue;
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

const EN = flatten(en as Record<string, unknown>);
const ZH = flatten(zh as Record<string, unknown>);

/** hasKey accepts the key or its i18next plural forms. */
function hasKey(locale: Map<string, string>, key: string): boolean {
  return (
    locale.has(key) || locale.has(`${key}_one`) || locale.has(`${key}_other`)
  );
}

describe('locale files', () => {
  it('define the same keys in both languages', () => {
    const onlyEn = [...EN.keys()].filter((k) => !ZH.has(k)).sort();
    const onlyZh = [...ZH.keys()].filter((k) => !EN.has(k)).sort();
    expect({ onlyEn, onlyZh }).toEqual({ onlyEn: [], onlyZh: [] });
  });

  it('interpolate the same placeholders', () => {
    const mismatched: string[] = [];
    for (const [key, value] of EN) {
      const other = ZH.get(key);
      if (other === undefined) continue;
      if (placeholders(value).join() !== placeholders(other).join()) {
        mismatched.push(key);
      }
    }
    expect(mismatched).toEqual([]);
  });

  it('carry no empty translations', () => {
    const empty = [...EN, ...ZH]
      .filter(([, value]) => value.trim() === '')
      .map(([key]) => key);
    expect(empty).toEqual([]);
  });

  it('define every key the source asks for', () => {
    const missing = new Set<string>();
    for (const file of sourceFiles()) {
      const source = readFileSync(file, 'utf8');
      for (const match of source.matchAll(/\bt\(\s*(['"])([\w.]+)\1/g)) {
        const key = match[2];
        if (!hasKey(EN, key)) missing.add(key);
      }
    }
    expect([...missing].sort()).toEqual([]);
  });
});
