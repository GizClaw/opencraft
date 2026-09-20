import type { ReactNode } from 'react';
import { ICON } from './icon';

// Segmented — an exclusive choice rendered as one bordered control
// (theme dark/light/auto, pets on/off, log level). Every call site used
// to spell out the wrapper border plus the active button's accent fill,
// which is why the same control shipped at three paddings and two text
// sizes. Options carry an optional icon, label and trailing content
// (e.g. a log-level count).
//
// Sizes: sm for dense rows (log viewer), md for settings cards.
export type SegmentedOption<T extends string> = {
  value: T;
  label?: ReactNode;
  icon?: React.ComponentType<{ size?: string | number; className?: string }>;
  // Trailing content rendered after the label (counts, badges).
  content?: ReactNode;
  // Accessible name when the label alone is ambiguous; also the title.
  title?: string;
  disabled?: boolean;
};

export function Segmented<T extends string>({
  value,
  options,
  onChange,
  size = 'md',
  className = '',
}: {
  value: T;
  options: SegmentedOption<T>[];
  onChange: (value: T) => void;
  size?: 'sm' | 'md';
  className?: string;
}) {
  const pad = size === 'sm' ? 'px-2 py-1 text-xs' : 'px-3 py-1.5 text-sm';
  const iconSize = size === 'sm' ? ICON.xs : ICON.sm;
  return (
    <div
      className={`flex shrink-0 overflow-hidden rounded-control border border-edge ${
        size === 'sm' ? 'text-xs' : 'text-sm'
      } ${className}`}
      role="group"
    >
      {options.map((option) => {
        const active = option.value === value;
        const Icon = option.icon;
        return (
          <button
            key={option.value}
            type="button"
            data-tip={option.title}
            aria-pressed={active}
            disabled={option.disabled}
            onClick={() => onChange(option.value)}
            className={`flex items-center gap-1.5 transition-colors disabled:opacity-40 ${pad} ${
              active
                ? 'bg-accent text-white'
                : 'text-dim hover:bg-panel hover:text-fg'
            }`}
          >
            {Icon && <Icon size={iconSize} />}
            {option.label}
            {option.content}
          </button>
        );
      })}
    </div>
  );
}
