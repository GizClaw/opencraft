import { useId, type ReactNode } from 'react';

// SettingRow — one labelled setting: what it does on the left, the
// control that changes it on the right.
//
// Settings cards used to lay their fields out as bare
// label-above-input grids: a numeric knob with no unit, no range and no
// explanation then read as the same kind of thing as the card's
// description, and a row of them left the eye to guess which label
// belonged to which field. The label/hint pair on the left and the
// control on the right is the shape the app's settings cards already
// use for their menus and sliders, so this is that shape as a recipe.
export function SettingRow({
  label,
  hint,
  children,
}: {
  label: ReactNode;
  hint?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="flex items-start justify-between gap-4">
      <div className="min-w-0">
        <div className="text-xs font-medium text-dim">{label}</div>
        {hint !== undefined && hint !== '' && (
          <p className="mt-0.5 text-label text-dim">{hint}</p>
        )}
      </div>
      <div className="flex shrink-0 items-center gap-2">{children}</div>
    </div>
  );
}

// ToggleSetting — a SettingRow whose control is a checkbox. The whole
// row is the hit target (the label wraps it), which is why the box can
// sit at the far edge without becoming a pixel hunt.
//
// The box is named by the label and described by the hint rather than by
// the label element's whole text: with the hint inside the same <label>,
// the accessible name would be the two sentences glued together.
export function ToggleSetting({
  label,
  hint,
  checked,
  onChange,
}: {
  label: ReactNode;
  hint?: ReactNode;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  const id = useId();
  const labelId = `${id}-label`;
  const hintId = `${id}-hint`;
  return (
    <label className="flex cursor-pointer items-start justify-between gap-4">
      <span className="min-w-0">
        <span id={labelId} className="block text-xs font-medium text-dim">
          {label}
        </span>
        {hint !== undefined && hint !== '' && (
          <span id={hintId} className="mt-0.5 block text-label text-dim">
            {hint}
          </span>
        )}
      </span>
      <input
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        aria-labelledby={labelId}
        aria-describedby={
          hint === undefined || hint === '' ? undefined : hintId
        }
        className="mt-0.5 shrink-0 accent-accent"
      />
    </label>
  );
}

// NumberSetting — a bounded number with its unit written inside the
// field, the way a unit is written next to a measurement: "36 messages"
// is one value, not a number and a stray word 40px to its right.
//
// The field is one frame: the number sits right-aligned against the
// divider so the digits line up with the digits above and below it, the
// unit sits in a fixed-width cell so the frames themselves come out the
// same width, and the whole cluster is what a row right-aligns. The
// number is the only thing that reads as input: the unit cell is a
// label, not a place to type.
export function NumberSetting({
  label,
  value,
  onChange,
  unit,
  min,
  max,
  step,
  width = 'w-20',
  unitWidth = 'w-[4.75rem]',
}: {
  /** Accessible name; the visible one is the row's label. */
  label: string;
  value: number;
  onChange: (value: number) => void;
  unit?: string;
  min?: number;
  max?: number;
  step?: number;
  width?: string;
  unitWidth?: string;
}) {
  return (
    <span className="flex items-stretch overflow-hidden rounded-control border border-edge bg-panel transition-colors hover:border-accent/50 focus-within:border-accent">
      <input
        type="number"
        aria-label={label}
        value={value}
        min={min}
        max={max}
        step={step}
        onChange={(e) => onChange(Number(e.target.value) || 0)}
        className={`${width} shrink-0 bg-transparent py-1 pr-2.5 pl-2.5 text-right text-xs tabular-nums text-fg outline-none`}
      />
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
