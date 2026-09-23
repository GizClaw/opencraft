import { useState, type ComponentProps } from 'react';
import { useTranslation } from 'react-i18next';
import { Minus, Plus } from 'lucide-react';
import { ICON } from './icon';

// NumberField — the one number-field recipe.
//
// The call sites were eleven hand-written `<input type="number">` strings
// with three commit semantics between them: every keystroke wrote a
// number, clearing the field wrote `Number('') || 0` — a zero nobody
// typed — and the engine's spinner, styled by no one, sat on top of the
// digits. This keeps the native number input (min/max/step, the arrow
// keys and the value hygiene stay the browser's job) and takes over only
// what was never the browser's: the frame, the unit cell, the steppers
// and what an empty field means.
//
// What an empty field means: typing is never clamped — "15" on the way
// to "150" is not the app's to correct — and an empty draft commits
// nothing unless the call site has an empty state (`allowEmpty`, an
// optional limit that falls back to the provider's default). Leaving the
// field either clamps what was typed into [min, max] or restores the last
// committed value: a cleared field is a cancelled edit, not a zero.
//
// The frame is ui/Input's recipe at the same two sizes (sm for compact
// rows, md for forms) and the same surface rungs, so a number field and a
// text field in one form share a border, a fill and a focus behavior.
// `className` carries the layout the field cannot know (width, margins);
// the frame lights up on focus-within, because the digits are the only
// thing that reads as input.
export type NumberFieldProps = Omit<
  ComponentProps<'input'>,
  'type' | 'size' | 'value' | 'onChange' | 'min' | 'max' | 'step' | 'className'
> & {
  /** Committed value; `''` is the unset state of an optional limit. */
  value: number | '';
  onChange: (value: number | '') => void;
  /** The field has an empty state, and `''` is a value it can commit. */
  allowEmpty?: boolean;
  min?: number;
  max?: number;
  step?: number;
  size?: 'sm' | 'md';
  surface?: 'sunken' | 'raised';
  invalid?: boolean;
  /** Right-aligned digits line up with the digits above and below them. */
  align?: 'left' | 'right';
  /** The unit is a label, not a place to type: it gets its own cell and
   * a divider, so the digits stay the only thing that looks editable. */
  unit?: string;
  /** Fixed width of the unit cell, so stacked rows come out even. */
  unitWidth?: string;
  /** Accessible name: it labels the input and names the stepper buttons
   * ("Increase Raw window"), which is why a stepper needs one. */
  label?: string;
  /** Layout of the frame (width, margins). */
  className?: string;
  /** Layout of the input cell itself — its width when a unit follows. */
  inputClassName?: string;
  /** −/+ cells either side of the digits, for callers that want the
   * affordance the engine's spinner used to provide. */
  steppers?: boolean;
};

export function NumberField({
  value,
  onChange,
  allowEmpty = false,
  min,
  max,
  step,
  size = 'md',
  surface = 'sunken',
  invalid = false,
  align = 'left',
  unit,
  unitWidth = 'w-[4.75rem]',
  label,
  className = '',
  inputClassName = 'w-full',
  steppers = false,
  disabled,
  onBlur,
  ...rest
}: NumberFieldProps) {
  const { t } = useTranslation();
  // What the user is mid-way through typing; null means the field shows
  // the committed value. Without a draft, clearing the field would snap
  // back to the last committed number on the first keystroke.
  const [draft, setDraft] = useState<string | null>(null);

  const clamp = (next: number) =>
    Math.min(max ?? Infinity, Math.max(min ?? -Infinity, next));

  const typed = draft ?? (value === '' ? '' : String(value));

  const commit = (next: number | '') => {
    setDraft(null);
    onChange(next);
  };

  const edit = (text: string) => {
    setDraft(text);
    if (text === '') {
      if (allowEmpty) onChange('');
      return;
    }
    // In range or not, a finite number is a value while it is being
    // typed; the bounds are the blur's business.
    const parsed = Number(text);
    if (Number.isFinite(parsed)) onChange(parsed);
  };

  const settle = () => {
    if (draft === null) return;
    const text = draft;
    setDraft(null);
    if (text === '') {
      if (allowEmpty) onChange('');
      return;
    }
    const parsed = Number(text);
    if (Number.isFinite(parsed)) onChange(clamp(parsed));
  };

  const typedNumber = draft === null || draft === '' ? value : Number(draft);
  const base =
    typedNumber !== '' && Number.isFinite(typedNumber)
      ? typedNumber
      : (min ?? 0);
  const nudge = (direction: 1 | -1) =>
    commit(clamp(base + direction * (step ?? 1)));
  // A label-less stepper still announces what it does; the label names it
  // when the call site has one.
  const stepName = (key: 'ui.stepDown' | 'ui.stepUp') =>
    t(key, { label: label ?? '' }).trim();

  const pad = size === 'sm' ? 'px-2.5 py-1 text-xs' : 'px-3 py-1.5 text-sm';
  const cell = `grid w-5 shrink-0 cursor-pointer place-items-center border-edge text-dim transition-colors hover:text-fg disabled:cursor-default disabled:opacity-30 disabled:hover:bg-transparent ${
    surface === 'raised' ? 'hover:bg-panel3' : 'hover:bg-panel2'
  }`;

  return (
    <span
      className={`flex items-stretch overflow-hidden rounded-control border transition-colors ${
        surface === 'raised' ? 'bg-panel2' : 'bg-panel'
      } ${
        invalid
          ? 'border-err focus-within:border-err'
          : 'border-edge hover:border-accent/50 focus-within:border-accent'
      } ${className}`}
    >
      {steppers && (
        <button
          type="button"
          aria-label={stepName('ui.stepDown')}
          disabled={disabled === true || (min !== undefined && base <= min)}
          onClick={() => nudge(-1)}
          className={`${cell} border-r`}
        >
          <Minus size={ICON.xs} />
        </button>
      )}
      <input
        {...rest}
        type="number"
        disabled={disabled}
        aria-label={label}
        value={typed}
        min={min}
        max={max}
        step={step}
        onChange={(event) => edit(event.target.value)}
        onBlur={(event) => {
          settle();
          onBlur?.(event);
        }}
        className={`min-w-0 bg-transparent text-fg outline-none disabled:opacity-40 ${pad} ${
          align === 'right' ? 'text-right tabular-nums' : 'text-left'
        } ${inputClassName}`}
      />
      {steppers && (
        <button
          type="button"
          aria-label={stepName('ui.stepUp')}
          disabled={disabled === true || (max !== undefined && base >= max)}
          onClick={() => nudge(1)}
          className={`${cell} border-l`}
        >
          <Plus size={ICON.xs} />
        </button>
      )}
      {unit !== undefined && (
        <span
          className={`${unitWidth} flex shrink-0 items-center border-l border-edge px-2 text-micro text-faint`}
        >
          {unit}
        </span>
      )}
    </span>
  );
}
