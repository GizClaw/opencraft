import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api } from '../lib/api';
import type { PetMindDebug } from '../pet/state';

function moodLabel(
  mood: string,
  t: (key: string) => string,
): string {
  switch (mood) {
    case 'sleepy':
      return t('config.petMoodSleepy');
    case 'needy':
      return t('config.petMoodNeedy');
    case 'attentive':
      return t('config.petMoodAttentive');
    case 'playful':
      return t('config.petMoodPlayful');
    default:
      return t('config.petMoodContent');
  }
}

function dispositionLabel(
  disposition: string,
  t: (key: string) => string,
): string {
  switch (disposition) {
    case 'work':
      return t('config.petDispWork');
    case 'ask':
      return t('config.petDispAsk');
    case 'sleep':
      return t('config.petDispSleep');
    default:
      return t('config.petDispRoam');
  }
}

/**
 * PetBehaviorPanel shows the live pet drives/mood/interaction stats in
 * the Settings > Diagnostics tab so personality tuning is observable.
 */
export function PetBehaviorPanel() {
  const { t } = useTranslation();
  const [debug, setDebug] = useState<PetMindDebug | null>(null);

  useEffect(() => {
    let alive = true;
    const refresh = async () => {
      try {
        const next = await api.petDiagnostics();
        if (alive) setDebug(next);
      } catch {
        // The pet may be disabled or the window not running; the panel
        // simply keeps its last snapshot.
      }
    };
    void refresh();
    const timer = window.setInterval(() => void refresh(), 2000);
    return () => {
      alive = false;
      window.clearInterval(timer);
    };
  }, []);

  if (!debug) {
    return (
      <div className="rounded-xl border border-edge bg-panel2 p-4 text-xs text-dim">
        {t('config.petDiagEmpty')}
      </div>
    );
  }

  const drives = [
    {
      key: t('config.petDiagAttention'),
      value: debug.drives.attention,
      bar: 'bg-sky-400',
    },
    {
      key: t('config.petDiagEnergy'),
      value: debug.drives.energy,
      bar: 'bg-emerald-400',
    },
    {
      key: t('config.petDiagComfort'),
      value: debug.drives.comfort,
      bar: 'bg-violet-400',
    },
  ];

  return (
    <div className="rounded-xl border border-edge bg-panel2 p-4">
      <div className="flex items-center justify-between">
        <span className="text-xs font-medium uppercase tracking-wide text-dim">
          {t('config.petDiagTitle')}
        </span>
        <span className="rounded-full border border-accent/40 bg-accent/10 px-2 py-0.5 text-[0.7rem] text-accent">
          {moodLabel(debug.mood, t)}
        </span>
      </div>
      <div className="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div className="space-y-2">
          {drives.map((drive) => (
            <div key={drive.key}>
              <div className="flex justify-between text-xs text-dim">
                <span>{drive.key}</span>
                <span className="font-mono">
                  {Math.round(drive.value)}/100
                </span>
              </div>
              <div className="mt-1 h-1.5 overflow-hidden rounded bg-panel">
                <div
                  className={`h-full rounded ${drive.bar}`}
                  style={{ width: `${Math.max(0, Math.min(100, drive.value))}%` }}
                />
              </div>
            </div>
          ))}
        </div>
        <div className="space-y-1.5 text-xs">
          <div className="flex justify-between text-dim">
            <span>{t('config.petDiagDisposition')}</span>
            <span className="font-medium text-fg">
              {dispositionLabel(debug.disposition, t)}
            </span>
          </div>
          <div className="flex justify-between text-dim">
            <span>{t('config.petDiagPhase')}</span>
            <span className="font-mono text-fg">{debug.phase}</span>
          </div>
          <div className="flex justify-between text-dim">
            <span>{t('config.petDiagWalking')}</span>
            <span className={debug.walking ? 'text-accent' : 'text-dim'}>
              {debug.walking ? '✓' : '—'}
            </span>
          </div>
          <div className="flex justify-between text-dim">
            <span>{t('config.petDiagPokes')}</span>
            <span className="font-mono text-fg">{debug.stats.poke_count}</span>
          </div>
        </div>
      </div>
    </div>
  );
}
