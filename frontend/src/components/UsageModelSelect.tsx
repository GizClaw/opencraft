import { useEffect, useRef, useState } from 'react';
import { Check, ChevronDown } from 'lucide-react';
import { Popover } from './ui/Popover';
import { ICON } from './ui/icon';

// One row of the picker. The value is what the series query receives,
// so the all-models entry uses the empty string the backend treats as
// "sum every model"; the label is what the dropdown shows.
export interface UsageModelOption {
  value: string;
  label: string;
}

interface UsageModelSelectProps {
  value: string;
  options: UsageModelOption[];
  onChange: (model: string) => void;
  title?: string;
}

export function UsageModelSelect({
  value,
  options,
  onChange,
  title,
}: UsageModelSelectProps) {
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(-1);
  const anchorRef = useRef<HTMLButtonElement>(null);

  // The selected row (or the first) is the highlight the arrow keys start
  // roving from when the menu opens.
  useEffect(() => {
    if (!open) return;
    const selected = options.findIndex((option) => option.value === value);
    setActive(selected >= 0 ? selected : 0);
  }, [open, options, value]);

  const pick = (option: UsageModelOption) => {
    onChange(option.value);
    setOpen(false);
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      if (!open) {
        setOpen(true);
        return;
      }
      setActive((i) => Math.min(options.length - 1, i + 1));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      if (!open) {
        setOpen(true);
        return;
      }
      setActive((i) => Math.max(0, i - 1));
    } else if (e.key === 'Enter' && open && active >= 0) {
      e.preventDefault();
      pick(options[active]);
    } else if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      setOpen((v) => !v);
    }
  };

  const selectedLabel =
    options.find((option) => option.value === value)?.label ?? value;

  return (
    <div className="relative min-w-0">
      <button
        ref={anchorRef}
        type="button"
        onClick={() => setOpen((v) => !v)}
        onKeyDown={onKeyDown}
        aria-haspopup="listbox"
        aria-expanded={open}
        data-tip={title ?? selectedLabel}
        className="flex h-9 max-w-[24.2857rem] items-center gap-2 rounded-control border border-edge bg-panel px-3 text-xs font-mono text-fg transition-colors hover:border-accent/60 focus:border-accent"
      >
        <span className="truncate">{selectedLabel}</span>
        <ChevronDown
          size={ICON.sm}
          className={`ml-auto shrink-0 text-dim transition-transform ${
            open ? 'rotate-180' : ''
          }`}
        />
      </button>
      {open && (
        <Popover
          open
          onClose={() => setOpen(false)}
          anchor={anchorRef.current}
          keyboard
          matchWidth
          maxHeight={288}
          panelClassName="min-w-[15rem] max-w-[24.2857rem] rounded-control border border-edge bg-panel p-1 shadow-popover"
        >
          {options.length === 0 ? (
            <div className="px-2.5 py-2 text-xs text-dim">—</div>
          ) : (
            options.map((option, i) => {
              const selected = option.value === value;
              // Hover and the roving keyboard focus both drive the highlight,
              // so the row the arrow keys land on is the row that reads as
              // active.
              return (
                <button
                  key={option.value}
                  type="button"
                  role="option"
                  aria-selected={selected}
                  onMouseEnter={() => setActive(i)}
                  onFocus={() => setActive(i)}
                  onClick={() => pick(option)}
                  className={`flex w-full items-center gap-2 rounded-control px-2.5 py-1.5 text-left font-mono text-xs transition-colors ${
                    active === i
                      ? 'bg-panel2 text-fg'
                      : 'text-dim hover:text-fg'
                  } ${selected ? 'text-accent' : ''}`}
                >
                  <span className="truncate">{option.label}</span>
                  {selected && (
                    <Check size={ICON.sm} className="ml-auto shrink-0" />
                  )}
                </button>
              );
            })
          )}
        </Popover>
      )}
    </div>
  );
}
