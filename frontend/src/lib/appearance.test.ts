import { beforeEach, describe, expect, it } from 'vitest';
import { installMemoryLocalStorage } from '../test/storage';
import {
  applyCachedUISettings,
  applyUISettings,
  cacheUISettings,
  clampFontScale,
  DEFAULT_UI_SETTINGS,
  FONT_PRESET_CUSTOM,
  FONT_SCALE_STEPS,
  MAX_FONT_SCALE,
  MIN_FONT_SCALE,
  nearestFontScaleIndex,
  normalizeUISettings,
  quoteFontFamily,
  readCachedUISettings,
  resolveMonoStack,
  resolveSansStack,
  rootFontSize,
} from './appearance';

beforeEach(() => {
  installMemoryLocalStorage();
  window.localStorage.clear();
  document.documentElement.removeAttribute('style');
});

describe('font stacks', () => {
  it('renders the presets with their platform stack', () => {
    expect(resolveSansStack(DEFAULT_UI_SETTINGS)).toContain('system-ui');
    expect(resolveMonoStack(DEFAULT_UI_SETTINGS)).toContain('ui-monospace');
  });

  it('renders a named family with the system fallback', () => {
    const stack = resolveSansStack({
      ...DEFAULT_UI_SETTINGS,
      fontFamily: FONT_PRESET_CUSTOM,
      fontFamilyName: 'PingFang SC',
    });
    expect(stack.startsWith("'PingFang SC', ")).toBe(true);
    expect(stack).toContain('sans-serif');
    expect(
      resolveMonoStack({
        ...DEFAULT_UI_SETTINGS,
        codeFont: FONT_PRESET_CUSTOM,
        codeFontName: 'JetBrains Mono',
      }),
    ).toContain("'JetBrains Mono'");
  });

  it('falls back to the default stack for an empty or hand-edited value', () => {
    expect(
      resolveSansStack({
        ...DEFAULT_UI_SETTINGS,
        fontFamily: FONT_PRESET_CUSTOM,
        fontFamilyName: '   ',
      }),
    ).toBe(resolveSansStack(DEFAULT_UI_SETTINGS));
    // An unknown preset id from an older document is treated as a family
    // name, which is how the settings page renders it too; with no usable
    // family it lands on the default (bundled) faces.
    expect(
      resolveSansStack({ ...DEFAULT_UI_SETTINGS, fontFamily: 'pingfang' }),
    ).toBe(resolveSansStack(DEFAULT_UI_SETTINGS));
  });
});

describe('quoteFontFamily', () => {
  it('quotes names and escapes the terminating characters', () => {
    expect(quoteFontFamily('PingFang SC')).toBe("'PingFang SC'");
    expect(quoteFontFamily("O'Neill")).toBe("'O\\'Neill'");
    expect(quoteFontFamily(' back\\slash ')).toBe("'back\\\\slash'");
  });

  it('passes a raw stack through', () => {
    expect(quoteFontFamily("'A', sans-serif")).toBe("'A', sans-serif");
  });
});

describe('font scale', () => {
  it('scales the 14px design base', () => {
    expect(rootFontSize(1.12)).toBe('15.68px');
    expect(rootFontSize(1)).toBe('14.00px');
    expect(rootFontSize(1.4)).toBe('19.60px');
  });

  it('clamps into the range the desktop document accepts', () => {
    expect(clampFontScale(0)).toBe(1.12);
    expect(clampFontScale(Number.NaN)).toBe(1.12);
    expect(clampFontScale(9)).toBe(MAX_FONT_SCALE);
    expect(clampFontScale(0.1)).toBe(MIN_FONT_SCALE);
  });

  it('places a hand-edited scale on the nearest notch', () => {
    expect(nearestFontScaleIndex(1)).toBe(0);
    expect(nearestFontScaleIndex(1.12)).toBe(1);
    expect(nearestFontScaleIndex(1.2)).toBe(2);
    expect(nearestFontScaleIndex(4)).toBe(FONT_SCALE_STEPS.length - 1);
  });
});

describe('applying settings', () => {
  it('writes the font and scale variables onto the document', () => {
    applyUISettings({
      ...DEFAULT_UI_SETTINGS,
      fontFamily: FONT_PRESET_CUSTOM,
      fontFamilyName: 'Microsoft YaHei',
      codeFont: FONT_PRESET_CUSTOM,
      codeFontName: 'Fira Code',
      fontScale: 1.25,
    });
    const root = document.documentElement;
    expect(root.style.getPropertyValue('--oc-font-sans')).toContain(
      "'Microsoft YaHei'",
    );
    expect(root.style.getPropertyValue('--oc-font-mono')).toContain(
      "'Fira Code'",
    );
    expect(root.style.getPropertyValue('--oc-root-font-size')).toBe('17.50px');
  });

  it('round-trips the mirror and repaints from it', () => {
    const settings = {
      ...DEFAULT_UI_SETTINGS,
      fontFamily: FONT_PRESET_CUSTOM,
      fontFamilyName: 'Inter',
      fontScale: 1.4,
    };
    cacheUISettings(settings);
    expect(readCachedUISettings()).toEqual(settings);

    document.documentElement.removeAttribute('style');
    expect(applyCachedUISettings()).toEqual(settings);
    expect(
      document.documentElement.style.getPropertyValue('--oc-root-font-size'),
    ).toBe('19.60px');
  });

  it('ignores an empty or broken mirror', () => {
    expect(readCachedUISettings()).toBeNull();
    expect(applyCachedUISettings()).toBeNull();
    expect(document.documentElement.style.length).toBe(0);

    window.localStorage.setItem('opencraft.ui', '{not json');
    expect(readCachedUISettings()).toBeNull();
  });
});

describe('normalizeUISettings', () => {
  it('returns null when the binding payload is missing', () => {
    expect(normalizeUISettings(undefined)).toBeNull();
    expect(normalizeUISettings(null)).toBeNull();
    expect(normalizeUISettings('system')).toBeNull();
  });

  it('fills missing fields with defaults', () => {
    expect(normalizeUISettings({})).toEqual(DEFAULT_UI_SETTINGS);
    // A named selection without a family renders as the default stack, so it
    // is stored that way; a name left behind by a preset is dropped.
    expect(
      normalizeUISettings({ fontFamily: 'custom', fontFamilyName: '   ' }),
    ).toEqual(DEFAULT_UI_SETTINGS);
    expect(
      normalizeUISettings({
        fontFamily: 'system',
        fontFamilyName: 'PingFang SC',
      }),
    ).toEqual({ ...DEFAULT_UI_SETTINGS, fontFamily: 'system' });
    expect(
      normalizeUISettings({
        fontFamily: 'custom',
        fontFamilyName: '  PingFang SC  ',
        codeFont: 'nope',
        fontScale: 8,
      }),
    ).toEqual({
      fontFamily: 'custom',
      fontFamilyName: 'PingFang SC',
      codeFont: 'system',
      codeFontName: '',
      fontScale: MAX_FONT_SCALE,
    });
  });
});
