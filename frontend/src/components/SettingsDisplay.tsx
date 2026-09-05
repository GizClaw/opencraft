import { useState } from 'react';
import {
  Check,
  ChevronDown,
  Languages,
  Monitor,
  Moon,
  Palette,
  Sun,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useStore } from '../lib/store';
import { PluginPanels } from '../plugins/components/PluginPanels';

// SettingsDisplay is the interface/display settings tab: language and
// light/dark theme, plus the plugin contribution area for this
// surface.
export function SettingsDisplay() {
  const { t, i18n } = useTranslation();
  const lang = i18n.resolvedLanguage?.startsWith('zh') ? 'zh' : 'en';
  const theme = useStore((s) => s.theme);
  const setTheme = useStore((s) => s.setTheme);
  const [langMenuOpen, setLangMenuOpen] = useState(false);

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
    </div>
  );
}
