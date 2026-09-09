import { useEffect, useRef, useState } from 'react';
import { Check, ChevronDown, RefreshCw } from 'lucide-react';

const REFRESH_INTERVALS_MS = [0, 5000, 10_000, 30_000, 60_000];

// RefreshControl is the split refresh control used by live panels: the icon
// triggers an immediate refresh and the adjacent picker sets the auto-refresh
// interval (Off / 5s / 10s / 30s / 60s). The Git panel and the Diagnostics
// metric charts share this control so both keep the same look and behavior.
export function RefreshControl({
  spinning,
  value,
  onChange,
  onRefresh,
  refreshLabel,
  intervalLabel,
  offLabel,
}: {
  spinning: boolean;
  value: number;
  onChange: (ms: number) => void;
  onRefresh: () => void;
  refreshLabel: string;
  intervalLabel: string;
  offLabel: string;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onDown = (e: PointerEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener('pointerdown', onDown);
    return () => document.removeEventListener('pointerdown', onDown);
  }, [open]);
  const label = value > 0 ? `${value / 1000} s` : offLabel;
  return (
    <div className="relative" ref={ref}>
      <div className="flex items-center overflow-hidden rounded-lg border border-edge">
        <button
          onClick={() => {
            setOpen(false);
            onRefresh();
          }}
          className="grid h-7 w-7 place-items-center text-dim hover:bg-panel2 hover:text-fg"
          title={refreshLabel}
          aria-label={refreshLabel}
        >
          <RefreshCw
            size="0.9286rem"
            className={spinning ? 'animate-spin' : ''}
          />
        </button>
        <button
          onClick={() => setOpen((v) => !v)}
          className="flex h-7 items-center gap-1 border-l border-edge px-1.5 text-[0.7143rem] text-dim hover:bg-panel2 hover:text-fg"
          title={intervalLabel}
          aria-label={intervalLabel}
          aria-haspopup="listbox"
          aria-expanded={open}
        >
          {label}
          <ChevronDown size="0.7857rem" />
        </button>
      </div>
      {open && (
        <div
          role="listbox"
          className="absolute right-0 top-full z-40 mt-1.5 w-44 rounded-lg border border-edge bg-panel p-1 shadow-xl"
        >
          {REFRESH_INTERVALS_MS.map((ms) => {
            const optionLabel = ms > 0 ? `${ms / 1000} s` : offLabel;
            const active = value === ms;
            return (
              <button
                key={ms}
                role="option"
                aria-selected={active}
                onClick={() => {
                  setOpen(false);
                  onChange(ms);
                }}
                className={`flex w-full items-center justify-between rounded-md px-2 py-1.5 text-left text-xs ${
                  active
                    ? 'bg-accent/10 text-accent'
                    : 'text-dim hover:bg-panel2 hover:text-fg'
                }`}
              >
                <span>{optionLabel}</span>
                {active && <Check size="0.8571rem" />}
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}
