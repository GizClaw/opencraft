import { Cpu } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api } from '../lib/api';
import type { ExecPool } from '../lib/types';
import { ICON } from './ui/icon';
import { NumberField } from './ui/NumberField';
import { SaveBar } from './ui/SaveBar';

interface Draft {
  prewarm: number;
  maxIdle: number;
  maxActive: number;
  idleMinutes: number;
}

// The ranges the host normalizes to; the inputs advertise them so a
// value is never silently clamped behind the user's back.
const FIELDS: Array<{
  key: keyof Draft;
  label: string;
  min: number;
  max: number;
}> = [
  { key: 'prewarm', label: 'diagExecPoolPrewarm', min: 0, max: 8 },
  { key: 'maxIdle', label: 'diagExecPoolMaxIdle', min: 0, max: 16 },
  { key: 'maxActive', label: 'diagExecPoolMaxActive', min: 1, max: 64 },
  { key: 'idleMinutes', label: 'diagExecPoolIdleMinutes', min: 1, max: 60 },
];

function draftOf(pool: ExecPool): Draft {
  return {
    prewarm: pool.prewarm,
    maxIdle: pool.maxIdle,
    maxActive: pool.maxActive,
    idleMinutes: pool.idleMinutes,
  };
}

function sameDraft(a: Draft, b: Draft): boolean {
  return (
    a.prewarm === b.prewarm &&
    a.maxIdle === b.maxIdle &&
    a.maxActive === b.maxActive &&
    a.idleMinutes === b.idleMinutes
  );
}

// ExecPoolCard configures the exec supervisor pool: how many unbound
// children stay warm, how many idle children are kept, how many
// workspaces share a pooled child, and when idle children are recycled.
// Saving applies to future leases; running children keep serving.
export function ExecPoolCard() {
  const { t } = useTranslation();
  const [pool, setPool] = useState<ExecPool | null>(null);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    let alive = true;
    const load = async () => {
      try {
        const next = await api.execPool();
        if (alive) setPool(next);
        return next;
      } catch (err) {
        if (alive) setError(String(err));
        return null;
      }
    };
    void (async () => {
      const first = await load();
      // Seed the editable draft once; the timer below only refreshes
      // the live idle/active counters.
      if (alive && first) setDraft(draftOf(first));
    })();
    const timer = window.setInterval(() => void load(), 5000);
    return () => {
      alive = false;
      window.clearInterval(timer);
    };
  }, []);

  const save = async () => {
    if (!draft) return;
    setSaving(true);
    try {
      const next = await api.setExecPool(
        draft.prewarm,
        draft.maxIdle,
        draft.maxActive,
        draft.idleMinutes,
      );
      setPool(next);
      setDraft(draftOf(next));
      setError('');
      setSaved(true);
    } catch (err) {
      setSaved(false);
      setError(String(err));
    } finally {
      setSaving(false);
    }
  };

  if (!draft) {
    return error ? (
      <p className="text-xs text-err break-words">{error}</p>
    ) : null;
  }

  const dirty = pool !== null && !sameDraft(draft, draftOf(pool));

  return (
    <div className="rounded-card border border-edge bg-panel2">
      <div className="flex items-start justify-between gap-3 p-3 pb-0">
        <div className="min-w-0">
          <div className="flex items-center gap-2 text-sm font-medium">
            <Cpu size={ICON.sm} className="shrink-0 text-accent" />
            {t('config.diagExecPoolTitle')}
          </div>
          <p className="mt-1 text-xs text-faint">
            {t('config.diagExecPoolHint')}
          </p>
        </div>
        <span className="inline-flex shrink-0 items-center gap-1.5 rounded-full border border-edge px-2 py-0.5 text-micro text-dim">
          {t('config.diagExecPoolLive', {
            idle: pool?.idle ?? 0,
            active: pool?.active ?? 0,
          })}
        </span>
      </div>

      <div className="grid grid-cols-2 gap-2 p-3">
        {FIELDS.map((field) => (
          // A div, not a label: the steppers are buttons, and a label
          // element's control is its first labelable descendant — which
          // would make the field's own label point at the minus button.
          // The field is named by its aria-label instead.
          <div key={field.key} className="block">
            <span className="text-micro text-dim">
              {t(`config.${field.label}`)}
            </span>
            <NumberField
              label={t(`config.${field.label}`)}
              size="sm"
              surface="raised"
              steppers
              min={field.min}
              max={field.max}
              value={draft[field.key]}
              onChange={(next) => {
                setSaved(false);
                setDraft(
                  (prev) =>
                    prev && {
                      ...prev,
                      [field.key]: next === '' ? field.min : next,
                    },
                );
              }}
              className="mt-1 w-full"
            />
          </div>
        ))}
        <p className="col-span-2 text-micro text-faint">
          {t('config.diagExecPoolRanges')}
        </p>
      </div>

      <SaveBar
        saved={saved && !dirty}
        error={error}
        saving={saving}
        onSave={() => void save()}
        saveLabel={t('config.diagExecPoolSave')}
      >
        {dirty && (
          <span className="text-dim">{t('config.diagExecPoolUnsaved')}</span>
        )}
      </SaveBar>
    </div>
  );
}
