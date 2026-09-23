import type { ComponentProps } from 'react';

// Textarea — ui/Input's recipe for the multi-line fields.
//
// Six call sites spelled out the same border / fill / padding string with
// two type sizes and three paddings between them, and the two that edit
// paths and JSON each had to remember `font-mono` on their own. Same
// props as `Input` (size, surface, invalid, className for layout), plus
// the two variants those sites actually have: a fixed-width face (`mono`)
// and no frame at all (`seamless`, for a field that already sits inside a
// bordered box).
//
// Resizing is vertical: a form field that can be dragged wider than the
// column it sits in is a layout the user has to undo.
export type TextareaProps = ComponentProps<'textarea'> & {
  size?: 'sm' | 'md';
  surface?: 'sunken' | 'raised';
  invalid?: boolean;
  /** Fixed-width face, for paths, JSON and config leaves. */
  mono?: boolean;
  /** Drop the frame, for a field inside a bordered box of its own. */
  seamless?: boolean;
};

export function Textarea({
  size = 'md',
  surface = 'sunken',
  invalid = false,
  mono = false,
  seamless = false,
  className = '',
  ...rest
}: TextareaProps) {
  const pad = size === 'sm' ? 'px-2 py-1 text-xs' : 'px-3 py-1.5 text-sm';
  const fill = surface === 'raised' ? 'bg-panel2' : 'bg-panel';
  const border = invalid
    ? 'border-err focus:border-err'
    : 'border-edge focus:border-accent';
  const frame = seamless
    ? 'bg-transparent'
    : `rounded-control border ${fill} ${border}`;
  return (
    <textarea
      {...rest}
      className={`w-full resize-y text-fg outline-none disabled:opacity-40 ${pad} ${
        mono ? 'font-mono' : ''
      } ${frame} ${className}`}
    />
  );
}
