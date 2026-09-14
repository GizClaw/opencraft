import { Check, ChevronDown } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';

// MenuSelect is the dropdown the settings page uses everywhere else: a
// panel-styled trigger with a chevron that opens a floating menu of
// options, instead of the platform's native select control (whose
// arrow, height and hit area differ per browser). The menu is portaled
// to the document body so it is not clipped by the settings modal's
// scroll container, and it closes on outside click, Escape, scroll or
// resize.
export interface MenuOption {
  value: string;
  label: string;
}

export function MenuSelect({
  label,
  value,
  options,
  onChange,
  placeholder,
  disabled,
  mono,
}: {
  /** Accessible name of the control, also used by tests. */
  label: string;
  value: string;
  options: MenuOption[];
  onChange: (value: string) => void;
  /** Shown when no option is selected (an unset knob). */
  placeholder?: string;
  disabled?: boolean;
  /** Render the value and options in the mono face (wire tokens). */
  mono?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [rect, setRect] = useState<{
    top: number;
    left: number;
    width: number;
  } | null>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  const toggle = () => {
    if (disabled) return;
    const trigger = triggerRef.current;
    if (trigger === null) return;
    const box = trigger.getBoundingClientRect();
    setRect({ top: box.bottom + 4, left: box.left, width: box.width });
    setOpen((wasOpen) => !wasOpen);
  };

  useEffect(() => {
    if (!open) return;
    const close = () => setOpen(false);
    const onPointerDown = (event: MouseEvent) => {
      const target = event.target as Node;
      if (menuRef.current?.contains(target)) return;
      if (triggerRef.current?.contains(target)) return;
      close();
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') close();
    };
    document.addEventListener('mousedown', onPointerDown);
    document.addEventListener('keydown', onKeyDown);
    // A scroll or resize invalidates the measured anchor.
    window.addEventListener('scroll', close, true);
    window.addEventListener('resize', close);
    return () => {
      document.removeEventListener('mousedown', onPointerDown);
      document.removeEventListener('keydown', onKeyDown);
      window.removeEventListener('scroll', close, true);
      window.removeEventListener('resize', close);
    };
  }, [open]);

  const current = options.find((option) => option.value === value);
  return (
    <>
      <button
        ref={triggerRef}
        type="button"
        aria-label={label}
        aria-haspopup="listbox"
        aria-expanded={open}
        disabled={disabled}
        onClick={toggle}
        className={`inline-flex h-[1.875rem] w-full items-center gap-1.5 rounded-lg border bg-panel px-2 text-xs transition-colors outline-none hover:border-accent/60 focus:border-accent disabled:opacity-40 ${
          open ? 'border-accent' : 'border-edge'
        }`}
      >
        <span
          className={`min-w-0 flex-1 truncate text-left ${
            mono === true ? 'font-mono' : ''
          } ${current === undefined ? 'text-dim' : 'text-fg'}`}
        >
          {current?.label ?? placeholder ?? ''}
        </span>
        <ChevronDown
          size="0.875rem"
          className={`shrink-0 text-dim transition-transform ${
            open ? 'rotate-180' : ''
          }`}
        />
      </button>
      {open &&
        rect !== null &&
        createPortal(
          <div
            ref={menuRef}
            role="listbox"
            aria-label={label}
            style={{
              top: rect.top,
              left: rect.left,
              width: Math.max(rect.width, 160),
            }}
            className="fixed z-[100] overflow-y-auto rounded-xl border border-edge bg-panel py-1 shadow-xl"
          >
            {options.map((option) => (
              <button
                key={option.value}
                type="button"
                role="option"
                aria-selected={option.value === value}
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => {
                  onChange(option.value);
                  setOpen(false);
                }}
                className={`flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-xs hover:bg-panel2 ${
                  option.value === value ? 'text-fg' : 'text-dim'
                }`}
              >
                <Check
                  size="0.8rem"
                  className={`shrink-0 ${
                    option.value === value ? 'text-accent' : 'invisible'
                  }`}
                />
                <span
                  className={`min-w-0 flex-1 truncate ${
                    mono === true ? 'font-mono' : ''
                  }`}
                >
                  {option.label}
                </span>
              </button>
            ))}
          </div>,
          document.body,
        )}
    </>
  );
}
