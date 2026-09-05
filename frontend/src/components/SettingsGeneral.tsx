import { useEffect, useRef, useState } from 'react';
import {
  Check,
  ChevronDown,
  Flame,
  Minimize2,
  Power,
  Sparkles,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { api } from '../lib/api';
import { SESSION_MODES } from '../lib/sessionModes';
import { useStore } from '../lib/store';
import { PluginPanels } from '../plugins/components/PluginPanels';
import { YoloConfirmDialog } from './YoloConfirmDialog';

// The think slider persists on a short debounce so dragging across
// several levels writes the file once instead of once per step.
const THINK_SAVE_DELAY_MS = 400;

// SettingsGeneral is the first settings tab: behavior that applies
// app-wide (window close policy, new-session defaults) plus the
// plugin contribution area for this surface.
export function SettingsGeneral() {
  const { t } = useTranslation();
  const toast = useStore((s) => s.toast);
  const setSessionDefaults = useStore((s) => s.setSessionDefaults);
  const storeDefaults = useStore((s) => s.sessionDefaults);
  const yoloOnly = useStore((s) => s.yoloOnly);
  // null until the persisted value loads; the close-window row renders
  // once known so the highlighted option never flashes the default.
  const [closeToTray, setCloseToTray] = useState<boolean | null>(null);
  const [mode, setMode] = useState(storeDefaults.mode);
  const [think, setThink] = useState(storeDefaults.think);
  const [confirmYolo, setConfirmYolo] = useState(false);
  const [modeMenuOpen, setModeMenuOpen] = useState(false);
  // confirmedRef is the last value the backend accepted; it is the
  // rollback target when a save fails. pendingRef + timerRef drive the
  // debounced slider save and flush it if the tab closes early.
  const confirmedRef = useRef({
    mode: storeDefaults.mode,
    think: storeDefaults.think,
  });
  const pendingRef = useRef<{ mode: string; think: string } | null>(null);
  const timerRef = useRef<number | undefined>(undefined);

  const thinkLevels = [
    { value: 'minimal', label: t('chat.thinkMinimal') },
    { value: 'low', label: t('chat.thinkLow') },
    { value: 'medium', label: t('chat.thinkMedium') },
    { value: 'high', label: t('chat.thinkHigh') },
    { value: 'xhigh', label: t('chat.thinkXHigh') },
  ];
  const thinkIndex = Math.max(
    0,
    thinkLevels.findIndex((l) => l.value === think),
  );
  const modes = SESSION_MODES.map((m) => ({
    value: m.value,
    icon: m.icon,
    label: t(m.labelKey),
    banner: t(m.bannerKey),
  }));
  const activeMode = modes.find((m) => m.value === mode) ?? modes[1];
  const ModeIcon = activeMode.icon;

  useEffect(() => {
    let alive = true;
    void Promise.all([api.getCloseToTray(), api.sessionDefaults()])
      .then(([close, defaults]) => {
        if (!alive) return;
        setCloseToTray(close);
        setMode(defaults.mode);
        setThink(defaults.think);
        confirmedRef.current = {
          mode: defaults.mode,
          think: defaults.think,
        };
      })
      .catch(() => {
        // Best-effort: the store snapshot remains the fallback.
      });
    return () => {
      alive = false;
    };
  }, []);

  const persist = async (nextMode: string, nextThink: string) => {
    if (timerRef.current !== undefined) {
      window.clearTimeout(timerRef.current);
      timerRef.current = undefined;
    }
    pendingRef.current = null;
    try {
      await api.saveSessionDefaults({ mode: nextMode, think: nextThink });
      confirmedRef.current = { mode: nextMode, think: nextThink };
      setSessionDefaults({ mode: nextMode, think: nextThink });
    } catch {
      setMode(confirmedRef.current.mode);
      setThink(confirmedRef.current.think);
      toast(t('config.saveFailed'));
    }
  };

  const commitDefaults = async (nextMode: string, nextThink: string) => {
    setMode(nextMode);
    setThink(nextThink);
    await persist(nextMode, nextThink);
  };

  const scheduleThinkSave = (nextMode: string, nextThink: string) => {
    setMode(nextMode);
    setThink(nextThink);
    pendingRef.current = { mode: nextMode, think: nextThink };
    if (timerRef.current !== undefined) {
      window.clearTimeout(timerRef.current);
    }
    timerRef.current = window.setTimeout(() => {
      timerRef.current = undefined;
      void persist(nextMode, nextThink);
    }, THINK_SAVE_DELAY_MS);
  };

  const persistRef = useRef(persist);
  persistRef.current = persist;
  useEffect(
    () => () => {
      if (timerRef.current !== undefined) {
        window.clearTimeout(timerRef.current);
      }
      if (pendingRef.current) {
        void persistRef.current(
          pendingRef.current.mode,
          pendingRef.current.think,
        );
      }
    },
    [],
  );

  const chooseMode = (nextMode: string) => {
    if (nextMode === 'yolo' && nextMode !== mode) {
      setConfirmYolo(true);
      return;
    }
    void commitDefaults(nextMode, think);
  };

  const setClose = (next: boolean) => {
    const prev = closeToTray;
    if (prev === null) return;
    setCloseToTray(next);
    void api.setCloseToTray(next).catch(() => {
      setCloseToTray(prev);
      toast(t('config.saveFailed'));
    });
  };

  return (
    <div className="space-y-3">
      <PluginPanels tab="general" />
      <div className="rounded-xl border border-edge bg-panel2 p-4">
        <div className="flex items-start justify-between gap-4">
          <div>
            <div className="flex items-center gap-2 text-sm font-medium">
              <Sparkles size="1.0714rem" className="text-accent" />
              {t('config.generalNewSessionDefaults')}
            </div>
            <p className="mt-1 text-xs text-dim">
              {t('config.generalNewSessionDefaultsHint')}
            </p>
          </div>
        </div>
        <div className="mt-4 space-y-4">
          {yoloOnly ? (
            <div className="flex items-start justify-between gap-4">
              <div>
                <div className="text-xs font-medium text-dim">
                  {t('config.generalDefaultMode')}
                </div>
                <p className="mt-0.5 text-[0.7857rem] text-dim/80">
                  {t('config.generalDefaultModeHint')}
                </p>
              </div>
              <div className="flex shrink-0 items-center gap-1.5 rounded-lg border border-yolo/50 bg-yolo/15 px-2.5 py-1.5 text-xs text-yolo">
                <Flame size="0.9286rem" />
                {t('chat.yoloMode')}
              </div>
            </div>
          ) : (
            <div className="flex items-start justify-between gap-4">
              <div>
                <div className="text-xs font-medium text-dim">
                  {t('config.generalDefaultMode')}
                </div>
                <p className="mt-0.5 text-[0.7857rem] text-dim/80">
                  {t('config.generalDefaultModeHint')}
                </p>
              </div>
              <div className="relative shrink-0">
                <button
                  onClick={() => setModeMenuOpen((v) => !v)}
                  aria-label={t('config.generalDefaultMode')}
                  aria-haspopup="menu"
                  aria-expanded={modeMenuOpen}
                  className={`flex items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-xs transition-colors ${
                    mode === 'yolo'
                      ? 'border-yolo/50 bg-yolo/15 text-yolo hover:bg-yolo/25'
                      : mode === 'read-only'
                        ? 'border-accent/40 bg-accent/10 text-accent hover:bg-accent/20'
                        : 'border-edge text-dim hover:text-fg'
                  }`}
                >
                  <ModeIcon size="0.8571rem" />
                  <span>{activeMode.label}</span>
                  <ChevronDown
                    size="0.7857rem"
                    className={`text-dim transition-transform ${
                      modeMenuOpen ? 'rotate-180' : ''
                    }`}
                  />
                </button>
                {modeMenuOpen && (
                  <>
                    <div
                      className="fixed inset-0 z-30"
                      onClick={() => setModeMenuOpen(false)}
                    />
                    <div
                      role="menu"
                      className="absolute right-0 top-full z-40 mt-1.5 w-80 rounded-xl border border-edge bg-panel p-1 shadow-xl"
                    >
                      {modes.map((m) => {
                        const Icon = m.icon;
                        const active = m.value === mode;
                        return (
                          <button
                            key={m.value}
                            role="menuitem"
                            onClick={() => {
                              setModeMenuOpen(false);
                              chooseMode(m.value);
                            }}
                            className={`flex w-full items-start gap-2 rounded-md px-2 py-1.5 text-left text-xs ${
                              active
                                ? 'bg-accent/10 text-accent'
                                : 'text-dim hover:bg-panel2 hover:text-fg'
                            }`}
                          >
                            <Icon
                              size="0.8571rem"
                              className="mt-0.5 shrink-0"
                            />
                            <span className="min-w-0 flex-1">
                              <span className="flex items-center gap-1.5 font-medium text-fg">
                                {m.label}
                                {active && <Check size="0.7857rem" />}
                              </span>
                              <span className="mt-0.5 block leading-snug">
                                {m.banner}
                              </span>
                            </span>
                          </button>
                        );
                      })}
                    </div>
                  </>
                )}
              </div>
            </div>
          )}
          <div>
            <div className="flex items-center justify-between">
              <div>
                <div className="text-xs font-medium text-dim">
                  {t('config.generalDefaultThink')}
                </div>
                <p className="mt-0.5 text-[0.7857rem] text-dim/80">
                  {t('config.generalDefaultThinkHint')}
                </p>
              </div>
              <span className="text-sm font-medium">
                {thinkLevels[thinkIndex]?.label ?? think}
              </span>
            </div>
            <input
              type="range"
              min={0}
              max={thinkLevels.length - 1}
              step={1}
              value={thinkIndex}
              onChange={(e) => {
                const next =
                  thinkLevels[Number(e.target.value)]?.value ?? 'medium';
                scheduleThinkSave(mode, next);
              }}
              className="mt-2 w-full accent-accent"
              aria-label={t('config.generalDefaultThink')}
            />
            <div className="flex justify-between text-[0.7143rem] text-dim">
              {thinkLevels.map((l) => (
                <span key={l.value}>{l.label}</span>
              ))}
            </div>
          </div>
        </div>
      </div>
      {closeToTray !== null && (
        <div className="rounded-xl border border-edge bg-panel2 p-4">
          <div className="flex items-start justify-between gap-4">
            <div>
              <div className="flex items-center gap-2 text-sm font-medium">
                <Minimize2 size="1.0714rem" className="text-accent" />
                {t('config.uiCloseToTray')}
              </div>
              <p className="mt-1 text-xs text-dim">
                {t('config.uiCloseToTrayHint')}
              </p>
            </div>
            <div className="flex shrink-0 overflow-hidden rounded-lg border border-edge text-sm">
              <button
                onClick={() => setClose(true)}
                className={`flex items-center gap-1.5 px-3 py-1.5 transition-colors ${
                  closeToTray
                    ? 'bg-accent text-white'
                    : 'text-dim hover:bg-panel hover:text-fg'
                }`}
              >
                <Minimize2 size="0.9286rem" />
                {t('config.uiCloseToTrayHide')}
              </button>
              <button
                onClick={() => setClose(false)}
                className={`flex items-center gap-1.5 px-3 py-1.5 transition-colors ${
                  !closeToTray
                    ? 'bg-accent text-white'
                    : 'text-dim hover:bg-panel hover:text-fg'
                }`}
              >
                <Power size="0.9286rem" />
                {t('config.uiCloseToTrayQuit')}
              </button>
            </div>
          </div>
        </div>
      )}
      {confirmYolo && (
        <YoloConfirmDialog
          layer="z-[70]"
          title={t('config.generalYoloConfirmTitle')}
          intro={t('config.generalYoloConfirmIntro')}
          scope={t('config.generalYoloConfirmScope')}
          confirmLabel={t('config.generalYoloEnable')}
          onCancel={() => setConfirmYolo(false)}
          onConfirm={() => {
            setConfirmYolo(false);
            void commitDefaults('yolo', think);
          }}
        />
      )}
    </div>
  );
}
