import type { ComponentProps } from 'react';

// Input — the one text-field recipe. Call sites used to spell out the
// border, fill, padding and focus ring, which is how ~60 field variants
// ended up with two paddings and two text sizes for the same role.
//
// Sizes: sm for compact rows (search, arg/env text), md for forms.
// Surface: sunken (bg-panel, the default on card/panel bodies) or
// raised (bg-panel2, a field on a panel2 card).
export type InputProps = Omit<ComponentProps<'input'>, 'size'> & {
  size?: 'sm' | 'md';
  surface?: 'sunken' | 'raised';
  invalid?: boolean;
};

export function Input({
  size = 'md',
  surface = 'sunken',
  invalid = false,
  className = '',
  ...rest
}: InputProps) {
  const pad = size === 'sm' ? 'px-2.5 py-1 text-xs' : 'px-3 py-1.5 text-sm';
  const fill = surface === 'raised' ? 'bg-panel2' : 'bg-panel';
  const border = invalid
    ? 'border-err focus:border-err'
    : 'border-edge focus:border-accent';
  return (
    <input
      {...rest}
      className={`w-full rounded-control border ${fill} text-fg outline-none transition-colors disabled:opacity-40 ${border} ${pad} ${className}`}
    />
  );
}
