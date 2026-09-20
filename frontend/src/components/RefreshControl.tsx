import { useRef, useState } from 'react';
import { Check, ChevronDown, RefreshCw } from 'lucide-react';
import { Popover } from './ui/Popover';
import { ICON } from './ui/icon';

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
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const label = value > 0 ? `${value / 1000} s` : offLabel;
  return (
    <div className="relative">
      <div className="flex items-center overflow-hidden rounded-control border border-edge">
        <button
          onClick={() => {
            setOpen(false);
            onRefresh();
          }}
          className="grid h-7 w-7 place-items-center text-dim hover:bg-panel2 hover:text-fg"
          data-tip={refreshLabel}
          aria-label={refreshLabel}
        >
          <RefreshCw
            size={ICON.sm}
            className={spinning ? 'animate-spin' : ''}
          />
        </button>
        <button
          ref={triggerRef}
          onClick={() => setOpen((v) => !v)}
          className="flex h-7 items-center gap-1 border-l border-edge px-1.5 text-micro text-dim hover:bg-panel2 hover:text-fg"
          data-tip={intervalLabel}
          aria-label={intervalLabel}
          aria-haspopup="listbox"
          aria-expanded={open}
        >
          {label}
          <ChevronDown size={ICON.xs} />
        </button>
      </div>
      <Popover
        open={open}
        onClose={() => setOpen(false)}
        anchor={triggerRef.current}
        role="listbox"
        ariaLabel={intervalLabel}
        align="end"
        keyboard
        panelClassName="w-44 rounded-control border border-edge bg-panel p-1 shadow-popover"
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
              className={`flex w-full items-center justify-between rounded-control px-2 py-1.5 text-left text-xs ${
                active
                  ? 'bg-accent/10 text-accent'
                  : 'text-dim hover:bg-panel2 hover:text-fg'
              }`}
            >
              <span>{optionLabel}</span>
              {active && <Check size={ICON.xs} />}
            </button>
          );
        })}
      </Popover>
    </div>
  );
}
