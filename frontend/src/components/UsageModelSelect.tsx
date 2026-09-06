import { useEffect, useRef, useState } from 'react';
import { Check, ChevronDown } from 'lucide-react';

interface UsageModelSelectProps {
  value: string;
  options: string[];
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
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const selected = options.indexOf(value);
    setActive(selected >= 0 ? selected : 0);
  }, [open, options, value]);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', onDown);
    return () => document.removeEventListener('mousedown', onDown);
  }, [open]);

  const pick = (model: string) => {
    onChange(model);
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
    } else if (e.key === 'Escape') {
      setOpen(false);
    } else if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      setOpen((v) => !v);
    }
  };

  return (
    <div ref={rootRef} className="relative min-w-0">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        onKeyDown={onKeyDown}
        aria-haspopup="listbox"
        aria-expanded={open}
        title={title ?? value}
        className="flex h-9 max-w-[24.2857rem] items-center gap-2 rounded-lg border border-edge/70 bg-panel/70 px-3 text-xs font-mono text-fg backdrop-blur-sm transition-colors hover:bg-panel focus:border-accent"
      >
        <span className="truncate">{value}</span>
        <ChevronDown
          size={14}
          className={`ml-auto shrink-0 text-dim transition-transform ${
            open ? 'rotate-180' : ''
          }`}
        />
      </button>
      {open && (
        <>
          <div
            className="fixed inset-0 z-30"
            onMouseDown={() => setOpen(false)}
          />
          <div
            role="listbox"
            className="absolute left-0 top-full z-40 mt-1 w-full min-w-[15rem] max-w-[24.2857rem] rounded-lg border border-edge/80 bg-panel/95 p-1 shadow-xl backdrop-blur-md"
          >
            {options.length === 0 ? (
              <div className="px-2.5 py-2 text-xs text-dim">—</div>
            ) : (
              options.map((model, i) => {
                const selected = model === value;
                return (
                  <button
                    key={model}
                    type="button"
                    role="option"
                    aria-selected={selected}
                    onMouseEnter={() => setActive(i)}
                    onClick={() => pick(model)}
                    className={`flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left font-mono text-xs transition-colors ${
                      active === i
                        ? 'bg-panel2 text-fg'
                        : 'text-dim hover:text-fg'
                    } ${selected ? 'text-accent' : ''}`}
                  >
                    <span className="truncate">{model}</span>
                    {selected && (
                      <Check size={13} className="ml-auto shrink-0" />
                    )}
                  </button>
                );
              })
            )}
          </div>
        </>
      )}
    </div>
  );
}
