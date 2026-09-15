import { useEffect, useState } from 'react';
import {
  Check,
  ChevronDown,
  Languages,
  Monitor,
  Moon,
  Palette,
  Sun,
  Type,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { api } from '../lib/api';
import {
  CODE_FONT_PRESETS,
  FONT_SCALE_STEPS,
  nearestFontScaleIndex,
  resolveMonoStack,
  resolveSansStack,
  UI_FONT_PRESETS,
  type UISettings,
} from '../lib/appearance';
import { useStore } from '../lib/store';
import { FontPicker } from './FontPicker';
import { PluginPanels } from '../plugins/components/PluginPanels';

// SettingsDisplay is the interface/display settings tab: language,
// light/dark theme, interface/code font and text size, plus the plugin
// contribution area for this surface.
export function SettingsDisplay() {
  const { t, i18n } = useTranslation();
  const lang = i18n.resolvedLanguage?.startsWith('zh') ? 'zh' : 'en';
  const theme = useStore((s) => s.theme);
  const setTheme = useStore((s) => s.setTheme);
  const uiSettings = useStore((s) => s.uiSettings);
  const setUISettings = useStore((s) => s.setUISettings);
  const toast = useStore((s) => s.toast);
  const [langMenuOpen, setLangMenuOpen] = useState(false);
  // null keeps the picker in its "reading the catalogue" state; an empty array
  // means the host cannot enumerate fonts here.
  const [fontFamilies, setFontFamilies] = useState<string[] | null>(null);

  useEffect(() => {
    let live = true;
    void api
      .uiFonts()
      .then((families) => {
        if (live) setFontFamilies(families);
      })
      .catch(() => {
        // The picker reports the empty catalogue and keeps the presets.
        if (live) setFontFamilies([]);
      });
    return () => {
      live = false;
    };
  }, []);

  // persist stores the choice in the desktop preference document, rolling
  // the optimistic application above back when the write fails.
  const persist = (next: UISettings) => {
    const prev = uiSettings;
    setUISettings(next);
    void api.setUISettings(next).catch(() => {
      setUISettings(prev);
      toast(t('config.saveFailed'));
    });
  };

  // pickFont applies one picker choice: a preset id without a family name, or
  // a family from the host catalogue.
  const pickFont = (
    kind: 'sans' | 'mono',
    presetId: string,
    familyName: string,
  ) => {
    const next = { ...uiSettings };
    if (kind === 'sans') {
      next.fontFamily = presetId;
      next.fontFamilyName = familyName;
    } else {
      next.codeFont = presetId;
      next.codeFontName = familyName;
    }
    persist(next);
  };

  const fontScaleIndex = nearestFontScaleIndex(uiSettings.fontScale);

  // The size control steps along FONT_SCALE_STEPS; each notch is a discrete
  // value, so a drag writes the desktop document at most a few times and
  // needs no debounce (the value applies immediately either way).
  const stepFontScale = (delta: number) => {
    const next = FONT_SCALE_STEPS[fontScaleIndex + delta];
    if (next === undefined) return;
    persist({ ...uiSettings, fontScale: next.scale });
  };

  return (
    <div className="space-y-3">
      <PluginPanels tab="display" />
      <div className="rounded-xl border border-edge bg-panel2 p-4">
        <div className="flex items-start justify-between gap-4">
          <div>
            <div className="flex items-center gap-2 text-sm font-medium">
              <Languages size="1.0714rem" className="text-accent" />
              {t('config.uiLanguage')}
            </div>
            <p className="mt-1 text-xs text-dim">
              {t('config.uiLanguageHint')}
            </p>
          </div>
          <div className="relative shrink-0">
            <button
              onClick={() => setLangMenuOpen((v) => !v)}
              className="flex items-center gap-1.5 rounded-lg border border-edge bg-panel px-2.5 py-1.5 text-sm text-fg transition-colors hover:border-accent/50"
            >
              <Languages size="0.8571rem" className="text-dim" />
              {lang === 'zh' ? '中文' : 'English'}
              <ChevronDown
                size="0.8571rem"
                className={`text-dim transition-transform ${
                  langMenuOpen ? 'rotate-180' : ''
                }`}
              />
            </button>
            {langMenuOpen && (
              <>
                <div
                  className="fixed inset-0 z-30"
                  onClick={() => setLangMenuOpen(false)}
                />
                <div className="absolute right-0 top-full z-40 mt-1.5 w-40 rounded-xl border border-edge bg-panel p-1 shadow-xl">
                  <button
                    onClick={() => {
                      setLangMenuOpen(false);
                      void i18n.changeLanguage('zh');
                    }}
                    className={`flex w-full items-center justify-between rounded-md px-2 py-1.5 text-left text-sm ${
                      lang === 'zh'
                        ? 'bg-accent/10 text-accent'
                        : 'text-dim hover:bg-panel2 hover:text-fg'
                    }`}
                  >
                    <span>中文</span>
                    {lang === 'zh' && <Check size="0.8571rem" />}
                  </button>
                  <button
                    onClick={() => {
                      setLangMenuOpen(false);
                      void i18n.changeLanguage('en');
                    }}
                    className={`flex w-full items-center justify-between rounded-md px-2 py-1.5 text-left text-sm ${
                      lang === 'en'
                        ? 'bg-accent/10 text-accent'
                        : 'text-dim hover:bg-panel2 hover:text-fg'
                    }`}
                  >
                    <span>English</span>
                    {lang === 'en' && <Check size="0.8571rem" />}
                  </button>
                </div>
              </>
            )}
          </div>
        </div>
      </div>
      <div className="rounded-xl border border-edge bg-panel2 p-4">
        <div className="flex items-start justify-between gap-4">
          <div>
            <div className="flex items-center gap-2 text-sm font-medium">
              <Palette size="1.0714rem" className="text-accent" />
              {t('config.uiTheme')}
            </div>
            <p className="mt-1 text-xs text-dim">{t('config.uiThemeHint')}</p>
          </div>
          <div className="flex shrink-0 overflow-hidden rounded-lg border border-edge text-sm">
            <button
              onClick={() => setTheme('dark')}
              className={`flex items-center gap-1.5 px-3 py-1.5 transition-colors ${
                theme === 'dark'
                  ? 'bg-accent text-white'
                  : 'text-dim hover:bg-panel hover:text-fg'
              }`}
            >
              <Moon size="0.9286rem" />
              {t('config.uiThemeDark')}
            </button>
            <button
              onClick={() => setTheme('light')}
              className={`flex items-center gap-1.5 px-3 py-1.5 transition-colors ${
                theme === 'light'
                  ? 'bg-accent text-white'
                  : 'text-dim hover:bg-panel hover:text-fg'
              }`}
            >
              <Sun size="0.9286rem" />
              {t('config.uiThemeLight')}
            </button>
            <button
              onClick={() => setTheme('auto')}
              className={`flex items-center gap-1.5 px-3 py-1.5 transition-colors ${
                theme === 'auto'
                  ? 'bg-accent text-white'
                  : 'text-dim hover:bg-panel hover:text-fg'
              }`}
            >
              <Monitor size="0.9286rem" />
              {t('config.uiThemeAuto')}
            </button>
          </div>
        </div>
      </div>
      <div className="rounded-xl border border-edge bg-panel2 p-4">
        <div className="flex items-center gap-2 text-sm font-medium">
          <Type size="1.0714rem" className="text-accent" />
          {t('config.uiFonts')}
        </div>
        <p className="mt-1 text-xs text-dim">{t('config.uiFontsHint')}</p>
        <div className="mt-3 space-y-3">
          <div className="flex items-center justify-between gap-4">
            <span className="shrink-0 text-xs text-dim">
              {t('config.uiFontInterface')}
            </span>
            <div className="w-[13rem] shrink-0">
              <FontPicker
                label={t('config.uiFontInterface')}
                presets={UI_FONT_PRESETS}
                families={fontFamilies}
                presetId={uiSettings.fontFamily}
                familyName={uiSettings.fontFamilyName}
                onChange={(presetId, family) =>
                  pickFont('sans', presetId, family)
                }
              />
            </div>
          </div>
          <div className="flex items-center justify-between gap-4">
            <span className="shrink-0 text-xs text-dim">
              {t('config.uiFontCode')}
            </span>
            <div className="w-[13rem] shrink-0">
              <FontPicker
                label={t('config.uiFontCode')}
                presets={CODE_FONT_PRESETS}
                families={fontFamilies}
                presetId={uiSettings.codeFont}
                familyName={uiSettings.codeFontName}
                onChange={(presetId, family) =>
                  pickFont('mono', presetId, family)
                }
              />
            </div>
          </div>
          <div className="flex items-center justify-between gap-4">
            <span className="shrink-0 text-xs text-dim">
              {t('config.uiFontSize')}
            </span>
            {/* The Apple-style text-size control: a small A, the notches, and
                a large A. Both A's step the size, so the ends read as the
                range and stay usable without dragging. */}
            <div className="flex shrink-0 items-center gap-1.5">
              <button
                type="button"
                aria-label={t('config.uiFontSizeDecrease')}
                disabled={fontScaleIndex === 0}
                onClick={() => stepFontScale(-1)}
                className="grid h-6 w-6 place-items-center rounded-md text-[0.75rem] leading-none text-dim transition-colors hover:bg-panel hover:text-fg disabled:opacity-30 disabled:hover:bg-transparent disabled:hover:text-dim"
              >
                A
              </button>
              <input
                type="range"
                min={0}
                max={FONT_SCALE_STEPS.length - 1}
                step={1}
                value={fontScaleIndex}
                aria-label={t('config.uiFontSize')}
                aria-valuetext={t(
                  FONT_SCALE_STEPS[fontScaleIndex]?.labelKey ?? '',
                )}
                onChange={(event) => {
                  const step = FONT_SCALE_STEPS[Number(event.target.value)];
                  if (step === undefined) return;
                  persist({ ...uiSettings, fontScale: step.scale });
                }}
                className="w-[9rem] accent-accent"
              />
              <button
                type="button"
                aria-label={t('config.uiFontSizeIncrease')}
                disabled={fontScaleIndex === FONT_SCALE_STEPS.length - 1}
                onClick={() => stepFontScale(1)}
                className="grid h-6 w-6 place-items-center rounded-md text-[1.15rem] leading-none text-fg transition-colors hover:bg-panel disabled:opacity-30 disabled:hover:bg-transparent"
              >
                A
              </button>
            </div>
          </div>
          <div className="rounded-lg border border-edge bg-panel px-3 py-2">
            <div
              className="text-sm"
              style={{ fontFamily: resolveSansStack(uiSettings) }}
            >
              {t('config.uiFontPreviewSans')}
            </div>
            <div
              className="mt-1 text-xs text-dim"
              style={{ fontFamily: resolveMonoStack(uiSettings) }}
            >
              {t('config.uiFontPreviewMono')}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
