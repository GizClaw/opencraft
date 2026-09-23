import { useRef, useState } from 'react';
import { Check, ChevronDown } from 'lucide-react';
import { Popover } from './ui/Popover';
import { ICON } from './ui/icon';

// MenuSelect is the dropdown the settings page uses everywhere else: a
// panel-styled trigger with a chevron that opens a floating menu of
// options, instead of the platform's native select control (whose
// arrow, height, hit area and option list differ per browser and per OS
// theme). The floating menu is the shared <Popover>: portaled past the
// settings scroller, animated, closed by outside click / Escape / scroll
// / resize, and reachable with the arrow keys.
//
// Two shapes, one behaviour:
//
//   field   the bordered panel-height select that stands alone in a
//           settings column, taking the width it is given.
//   inline  the borderless variant for a row that already draws the
//           frame — the fact composer's scope cell, where a second
//           border around the value would read as a control nested in a
//           control. It sizes to its label and highlights on hover the
//           way the split controls do.
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
  testId,
  variant = 'field',
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
  testId?: string;
  /** Field = standalone bordered select; inline = fit into a row's frame. */
  variant?: 'field' | 'inline';
}) {
  const [open, setOpen] = useState(false);
  const [anchor, setAnchor] = useState<HTMLElement | null>(null);
  const triggerRef = useRef<HTMLButtonElement | null>(null);

  const toggle = () => {
    if (disabled) return;
    setAnchor(triggerRef.current);
    setOpen((wasOpen) => !wasOpen);
  };

  const current = options.find((option) => option.value === value);
  const inline = variant === 'inline';
  return (
    <>
      <button
        ref={triggerRef}
        type="button"
        aria-label={label}
        aria-haspopup="listbox"
        aria-expanded={open}
        data-testid={testId}
        disabled={disabled}
        onClick={toggle}
        className={
          inline
            ? `flex h-7 shrink-0 items-center gap-1 rounded-control px-1.5 text-micro whitespace-nowrap transition-colors outline-none hover:bg-panel2 hover:text-fg disabled:opacity-40 ${
                open ? 'bg-panel2 text-fg' : 'text-dim'
              }`
            : `inline-flex h-[1.875rem] w-full items-center gap-1.5 rounded-control border bg-panel px-2 text-xs transition-colors outline-none hover:border-accent/60 focus:border-accent disabled:opacity-40 ${
                open ? 'border-accent' : 'border-edge'
              }`
        }
      >
        <span
          className={
            inline
              ? mono === true
                ? 'font-mono'
                : ''
              : `min-w-0 flex-1 truncate text-left ${
                  mono === true ? 'font-mono' : ''
                } ${current === undefined ? 'text-faint' : 'text-fg'}`
          }
        >
          {current?.label ?? placeholder ?? ''}
        </span>
        <ChevronDown
          size={ICON.xs}
          className={`shrink-0 text-dim transition-transform ${
            open ? 'rotate-180' : ''
          }`}
        />
      </button>
      <Popover
        open={open}
        onClose={() => setOpen(false)}
        anchor={anchor}
        ariaLabel={label}
        align={inline ? 'end' : 'start'}
        matchWidth={!inline}
        keyboard
        maxHeight={288}
        panelClassName={
          inline
            ? 'min-w-[9.5rem] rounded-control border border-edge bg-panel py-1 shadow-popover'
            : 'rounded-card border border-edge bg-panel py-1 shadow-popover'
        }
      >
        {options.map((option) => {
          const selected = option.value === value;
          return (
            <button
              key={option.value}
              type="button"
              role="option"
              aria-selected={selected}
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => {
                onChange(option.value);
                setOpen(false);
              }}
              className={`flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-xs hover:bg-panel2 ${
                selected ? 'text-fg' : 'text-dim'
              }`}
            >
              <Check
                size={ICON.xs}
                className={`shrink-0 ${selected ? 'text-accent' : 'invisible'}`}
              />
              <span
                className={`min-w-0 flex-1 truncate ${
                  mono === true ? 'font-mono' : ''
                }`}
              >
                {option.label}
              </span>
            </button>
          );
        })}
      </Popover>
    </>
  );
}
