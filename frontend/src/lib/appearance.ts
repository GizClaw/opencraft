// UI appearance (Settings > Interface): the interface font, the code/mono
// font and the whole-UI scale.
//
// The Go desktop preference document (~/.opencraft/config/desktop.json) is
// the durable copy. A localStorage mirror lets the very first paint use the
// stored choice before the binding resolves — the same split the i18next
// language cache uses — and the store reconciles the two after init.
//
// A font is a preset id plus, for "custom", a family name picked from the
// host font catalogue (internal/foundation/sysfont) or typed by hand. Turning
// that family into a CSS stack — quoting it and appending the platform
// fallback — is this module's job, so the desktop document never carries CSS.
// The preset ids are a contract with
// internal/adapters/desktop/core/ui_prefs.go: keep both sides in sync.

export interface UISettings {
  /** Preset id of the interface font (see UI_FONT_PRESETS). */
  fontFamily: string;
  /** Family name rendered while fontFamily is "custom". */
  fontFamilyName: string;
  /** Preset id of the code font (see CODE_FONT_PRESETS). */
  codeFont: string;
  /** Family name rendered while codeFont is "custom". */
  codeFontName: string;
  /** Whole-UI scale applied to the 14px design base. */
  fontScale: number;
}

export interface FontPreset {
  id: string;
  /** Translation key of the option label (config.* namespace). */
  labelKey: string;
  /** CSS font-family list the preset renders with. */
  stack: string;
}

export interface FontScaleStep {
  id: string;
  labelKey: string;
  scale: number;
}

// The platform stacks every named family falls back to. Keeping them here
// (instead of only in style.css) lets the renderer build previews without
// reading computed styles.
const SYSTEM_SANS =
  "ui-sans-serif, system-ui, -apple-system, 'PingFang SC', 'Microsoft YaHei', sans-serif";
const SYSTEM_MONO = 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace';

export const FONT_PRESET_SYSTEM = 'system';
export const FONT_PRESET_CUSTOM = 'custom';

export const UI_FONT_PRESETS: FontPreset[] = [
  {
    id: FONT_PRESET_SYSTEM,
    labelKey: 'config.uiFontSystem',
    stack: SYSTEM_SANS,
  },
];

export const CODE_FONT_PRESETS: FontPreset[] = [
  {
    id: FONT_PRESET_SYSTEM,
    labelKey: 'config.uiCodeFontSystem',
    stack: SYSTEM_MONO,
  },
];

// Mirrors the Go-side bounds (core.DefaultFontScale / minFontScale /
// maxFontScale) so a value that survives one side survives the other.
export const DEFAULT_FONT_SCALE = 1.12;
export const MIN_FONT_SCALE = 0.85;
export const MAX_FONT_SCALE = 1.6;

// The rem scale is expressed against a 14px design base (style.css).
const DESIGN_BASE_PX = 14;

export const FONT_SCALE_STEPS: FontScaleStep[] = [
  { id: 'sm', labelKey: 'config.uiFontSizeSmall', scale: 1 },
  { id: 'md', labelKey: 'config.uiFontSizeStandard', scale: 1.12 },
  { id: 'lg', labelKey: 'config.uiFontSizeLarge', scale: 1.25 },
  { id: 'xl', labelKey: 'config.uiFontSizeExtraLarge', scale: 1.4 },
];

export const DEFAULT_UI_SETTINGS: UISettings = {
  fontFamily: FONT_PRESET_SYSTEM,
  fontFamilyName: '',
  codeFont: FONT_PRESET_SYSTEM,
  codeFontName: '',
  fontScale: DEFAULT_FONT_SCALE,
};

const UI_CACHE_KEY = 'opencraft.ui';

// quoteFontFamily renders one family name as a CSS string, escaping the
// characters that would end it. A value that already looks like a list (a
// comma) is passed through: hand-edited documents may carry a raw stack, and
// quoting that would silently break the font.
export function quoteFontFamily(name: string): string {
  const trimmed = name.trim();
  if (trimmed.includes(',')) return trimmed;
  return `'${trimmed.replace(/\\/g, '\\\\').replace(/'/g, "\\'")}'`;
}

// namedStack renders a family name with the platform fallback appended, so a
// font that is not installed (or a Latin-only family) still renders.
function namedStack(name: string, fallback: string): string {
  const trimmed = name.trim();
  return trimmed === '' ? fallback : `${quoteFontFamily(trimmed)}, ${fallback}`;
}

export function resolveSansStack(settings: UISettings): string {
  const preset = UI_FONT_PRESETS.find(
    (candidate) => candidate.id === settings.fontFamily,
  );
  if (preset !== undefined) return preset.stack;
  return namedStack(settings.fontFamilyName, SYSTEM_SANS);
}

export function resolveMonoStack(settings: UISettings): string {
  const preset = CODE_FONT_PRESETS.find(
    (candidate) => candidate.id === settings.codeFont,
  );
  if (preset !== undefined) return preset.stack;
  return namedStack(settings.codeFontName, SYSTEM_MONO);
}

export function clampFontScale(scale: number): number {
  if (!Number.isFinite(scale) || scale <= 0) return DEFAULT_FONT_SCALE;
  return Math.min(Math.max(scale, MIN_FONT_SCALE), MAX_FONT_SCALE);
}

// rootFontSize is the value style.css feeds into html/body/#root: every
// rem-based size in the app scales with it (text, spacing and icons alike).
export function rootFontSize(scale: number): string {
  return `${(DESIGN_BASE_PX * clampFontScale(scale)).toFixed(2)}px`;
}

// nearestFontScaleIndex maps an arbitrary stored scale onto the notch the
// size control shows, so a hand-edited value still lands on a step.
export function nearestFontScaleIndex(scale: number): number {
  const target = clampFontScale(scale);
  let closest = 0;
  FONT_SCALE_STEPS.forEach((step, index) => {
    const best = FONT_SCALE_STEPS[closest];
    if (best === undefined) return;
    if (Math.abs(step.scale - target) < Math.abs(best.scale - target)) {
      closest = index;
    }
  });
  return closest;
}

export function applyUISettings(settings: UISettings): void {
  const root = document.documentElement.style;
  root.setProperty('--oc-font-sans', resolveSansStack(settings));
  root.setProperty('--oc-font-mono', resolveMonoStack(settings));
  root.setProperty('--oc-root-font-size', rootFontSize(settings.fontScale));
}

// normalizeUISettings coerces a binding payload (or a cached document) into
// complete settings. It returns null when the payload is missing entirely —
// a binding double or an older desktop build — so callers keep the cached
// copy instead of resetting the user's appearance.
export function normalizeUISettings(raw: unknown): UISettings | null {
  if (raw === null || typeof raw !== 'object') return null;
  const source = raw as Partial<UISettings>;
  const family = (value: unknown): string =>
    typeof value === 'string' ? value.trim() : '';
  // A named selection without a family renders as the system stack, so it is
  // stored that way: the picker then shows a selection the document actually
  // has (the desktop document normalizes it identically). The family is
  // dropped for presets as well, so a stale name cannot shadow a later pick.
  const selection = (
    presets: FontPreset[],
    id: unknown,
    name: unknown,
  ): { id: string; name: string } => {
    const trimmed = family(name);
    if (typeof id === 'string' && id === FONT_PRESET_CUSTOM && trimmed !== '') {
      return { id: FONT_PRESET_CUSTOM, name: trimmed };
    }
    const preset =
      typeof id === 'string'
        ? presets.find((candidate) => candidate.id === id)
        : undefined;
    return {
      id: preset?.id ?? FONT_PRESET_SYSTEM,
      name: '',
    };
  };
  const sans = selection(
    UI_FONT_PRESETS,
    source.fontFamily,
    source.fontFamilyName,
  );
  const mono = selection(
    CODE_FONT_PRESETS,
    source.codeFont,
    source.codeFontName,
  );
  return {
    fontFamily: sans.id,
    fontFamilyName: sans.name,
    codeFont: mono.id,
    codeFontName: mono.name,
    fontScale: clampFontScale(
      typeof source.fontScale === 'number'
        ? source.fontScale
        : DEFAULT_FONT_SCALE,
    ),
  };
}

// readCachedUISettings returns the mirrored preferences, or null when the
// cache is empty or unreadable (storage disabled, private browsing).
export function readCachedUISettings(): UISettings | null {
  try {
    const raw = window.localStorage.getItem(UI_CACHE_KEY);
    if (raw === null) return null;
    return normalizeUISettings(JSON.parse(raw));
  } catch {
    return null;
  }
}

// applyCachedUISettings paints the mirrored preferences before React mounts,
// so the first frame already uses the persisted font and scale.
export function applyCachedUISettings(): UISettings | null {
  const cached = readCachedUISettings();
  if (cached !== null) applyUISettings(cached);
  return cached;
}

export function cacheUISettings(settings: UISettings): void {
  try {
    window.localStorage.setItem(UI_CACHE_KEY, JSON.stringify(settings));
  } catch {
    // The Go document stays authoritative; a failed mirror only costs the
    // flash-free first paint.
  }
}
