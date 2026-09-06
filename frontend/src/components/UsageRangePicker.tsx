import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { CalendarDays, ChevronDown } from 'lucide-react';
import { MAX_USAGE_RANGE_DAYS } from '../lib/usageWindow';

export type UsageRangePreset = 'today' | '1d' | '7d' | '14d' | '30d';

interface UsageRangePickerProps {
  active: boolean;
  startMs: number;
  endMs: number;
  liveEnd: boolean;
  onApply: (startMs: number, endMs: number, liveEnd: boolean) => void;
  onCancel?: () => void;
}

function toLocalInput(ms: number): string {
  const d = new Date(ms);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(
    d.getDate(),
  )}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function fromLocalInput(value: string): number {
  const t = new Date(value).getTime();
  return Number.isFinite(t) ? t : 0;
}

function fmtClock(ms: number): string {
  const d = new Date(ms);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(
    d.getHours(),
  )}:${pad(d.getMinutes())}`;
}

export function UsageRangePicker({
  active,
  startMs,
  endMs,
  liveEnd,
  onApply,
  onCancel,
}: UsageRangePickerProps) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [draftStart, setDraftStart] = useState(
    startMs > 0 ? startMs : Date.now() - 7 * 86_400_000,
  );
  const [draftEnd, setDraftEnd] = useState(endMs > 0 ? endMs : Date.now());
  const [draftLive, setDraftLive] = useState(liveEnd);
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    setDraftStart(startMs > 0 ? startMs : Date.now() - 7 * 86_400_000);
    setDraftEnd(endMs > 0 ? endMs : Date.now());
    setDraftLive(liveEnd);
  }, [open, startMs, endMs, liveEnd]);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', onDown);
    return () => document.removeEventListener('mousedown', onDown);
  }, [open]);

  const liveEndMs = draftLive ? Date.now() : draftEnd;
  const spanMs = liveEndMs - draftStart;
  const tooLong = draftStart > 0 && spanMs > MAX_USAGE_RANGE_DAYS * 86_400_000;
  const canApply = draftStart > 0 && liveEndMs > draftStart && !tooLong;
  const label = active
    ? `${fmtClock(draftStart)} → ${
        draftLive ? t('config.usageAtRefresh') : fmtClock(draftEnd)
      }`
    : t('config.usageCustom');

  const commit = () => {
    if (!canApply) return;
    const end = draftLive ? Date.now() : draftEnd;
    onApply(draftStart, end, draftLive);
    setOpen(false);
  };

  return (
    <div ref={rootRef} className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className={`flex h-9 max-w-[17rem] items-center gap-1.5 rounded-lg border px-3 text-xs transition-all ${
          active
            ? 'border-accent/50 bg-accent/10 text-fg'
            : 'border-edge/70 bg-panel/70 text-dim hover:bg-panel hover:text-fg'
        } backdrop-blur-sm`}
      >
        <CalendarDays size={14} className="shrink-0" />
        <span className="truncate">{label}</span>
        <ChevronDown
          size={14}
          className={`shrink-0 transition-transform ${open ? 'rotate-180' : ''}`}
        />
      </button>
      {open && (
        <div className="absolute right-0 top-11 z-30 w-[19.5rem] rounded-xl border border-edge/70 bg-panel/95 p-3 shadow-xl backdrop-blur-md">
          <label className="block">
            <span className="mb-1 block text-[11px] font-medium text-dim">
              {t('config.usageCustomStart')}
            </span>
            <input
              type="datetime-local"
              value={toLocalInput(draftStart)}
              onChange={(e) => setDraftStart(fromLocalInput(e.target.value))}
              className="w-full rounded-lg border border-edge bg-panel2 px-2.5 py-1.5 text-xs text-fg outline-none focus:border-accent"
            />
          </label>
          <label className="mt-2.5 block">
            <span className="mb-1 block text-[11px] font-medium text-dim">
              {t('config.usageCustomEnd')}
            </span>
            <input
              type="datetime-local"
              value={
                draftLive ? toLocalInput(Date.now()) : toLocalInput(draftEnd)
              }
              disabled={draftLive}
              onChange={(e) => setDraftEnd(fromLocalInput(e.target.value))}
              className="w-full rounded-lg border border-edge bg-panel2 px-2.5 py-1.5 text-xs text-fg outline-none disabled:cursor-not-allowed disabled:opacity-50 focus:border-accent"
            />
          </label>
          <label className="mt-2.5 flex cursor-pointer items-center gap-2 text-xs text-dim">
            <input
              type="checkbox"
              checked={draftLive}
              onChange={(e) => {
                setDraftLive(e.target.checked);
              }}
              className="h-3.5 w-3.5 accent-[var(--color-accent)]"
            />
            {t('config.usageLiveEnd')}
          </label>
          {draftStart > 0 && liveEndMs <= draftStart && (
            <p className="mt-2 text-[11px] text-err">
              {t('config.usageRangeInvalid')}
            </p>
          )}
          {tooLong && (
            <p className="mt-2 text-[11px] text-err">
              {t('config.usageRangeTooLong')}
            </p>
          )}
          <div className="mt-3 flex items-center justify-end gap-2">
            <button
              type="button"
              onClick={() => {
                setOpen(false);
                onCancel?.();
              }}
              className="rounded-lg px-3 py-1.5 text-xs text-dim transition-colors hover:text-fg"
            >
              {t('config.cancel')}
            </button>
            <button
              type="button"
              disabled={!canApply}
              onClick={commit}
              className="rounded-lg bg-accent px-3 py-1.5 text-xs font-medium text-white transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40"
            >
              {t('config.apply')}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
