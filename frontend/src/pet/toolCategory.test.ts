// Pins the three copies of the tool-category vocabulary that the pet
// surface needs and that no other suite compares:
//
//   1. the Go feed's PetToolCategory constants (the source of truth),
//   2. the category -> glyph map in toolCategory.ts,
//   3. the [data-category='...'] tints in pet.css.
//
// A category added to one of them (or a typo in any of them) would
// otherwise ship as an uncoloured, unmarked pill that no test notices.
import { readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import {
  PET_TOOL_CATEGORIES,
  petToolCategory,
  petToolIcon,
} from './toolCategory';

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');

function goCategories(): string[] {
  const source = readFileSync(
    join(REPO_ROOT, 'internal/adapters/desktop/pet/pet.go'),
    'utf8',
  );
  return [
    ...source.matchAll(
      /PetToolCategory\w*\s+PetToolCategory\s*=\s*"([a-z]+)"/g,
    ),
  ].map((match) => match[1]);
}

function cssCategories(): string[] {
  const source = readFileSync(
    join(REPO_ROOT, 'frontend/src/pet/pet.css'),
    'utf8',
  );
  return [...source.matchAll(/\.pet-tool\[data-category='([a-z]+)'\]/g)].map(
    (match) => match[1],
  );
}

describe('pet tool categories', () => {
  it('matches the Go vocabulary', () => {
    expect(goCategories().length).toBeGreaterThan(0);
    expect([...PET_TOOL_CATEGORIES]).toEqual(goCategories());
  });

  it('tints every category in the stylesheet', () => {
    expect(cssCategories().sort()).toEqual([...PET_TOOL_CATEGORIES].sort());
  });

  it('gives every category its own glyph', () => {
    const icons = PET_TOOL_CATEGORIES.map((category) => petToolIcon(category));
    for (const icon of icons) expect(icon).toBeDefined();
    expect(new Set(icons).size).toBe(PET_TOOL_CATEGORIES.length);
  });

  it('degrades unknown and missing categories to "other"', () => {
    expect(petToolCategory('browse')).toBe('other');
    expect(petToolCategory(undefined)).toBe('other');
    expect(petToolIcon('browse')).toBe(petToolIcon('other'));
    expect(petToolCategory('exec')).toBe('exec');
  });
});
