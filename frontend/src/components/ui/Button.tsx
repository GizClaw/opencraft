import type { ComponentProps, ReactNode } from 'react';
import { Loader2 } from 'lucide-react';
import { ICON } from './icon';

// Button — the single button recipe. Before this existed every call site
// spelled out its own padding / radius / hover / disabled combination,
// so the same "primary action" rendered three different ways.
//
// Variants:
//   primary     the accent action (submit, save, create)
//   secondary   a filled quiet button next to a primary one
//   quiet       a bordered dim button (cancel, back, refresh)
//   ghost       no border, dim until hovered (toolbars, menu rows)
//   danger      a destructive primary (delete, remove)
//   dangerGhost a bordered destructive secondary (clear, revoke)
//
// Sizes: sm (compact rows) · md (default) · lg (hero CTAs).
export type ButtonVariant =
  'primary' | 'secondary' | 'quiet' | 'ghost' | 'danger' | 'dangerGhost';
export type ButtonSize = 'sm' | 'md' | 'lg';

const VARIANTS: Record<ButtonVariant, string> = {
  primary: 'bg-accent text-white hover:opacity-90',
  secondary: 'border border-edge bg-panel2 text-fg hover:border-accent/50',
  quiet: 'border border-edge text-dim hover:border-accent/50 hover:text-fg',
  ghost: 'text-dim hover:bg-panel2 hover:text-fg',
  danger: 'bg-err text-white hover:opacity-90',
  dangerGhost: 'border border-err/40 text-err hover:bg-err/10',
};

const SIZES: Record<ButtonSize, string> = {
  sm: 'px-2.5 py-1 text-xs',
  md: 'px-3.5 py-1.5 text-sm',
  lg: 'px-4 py-2 text-sm',
};

export interface ButtonProps extends Omit<
  ComponentProps<'button'>,
  'children'
> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  loading?: boolean;
  children?: ReactNode;
}

export function Button({
  variant = 'secondary',
  size = 'md',
  loading = false,
  className = '',
  disabled,
  type = 'button',
  children,
  ...rest
}: ButtonProps) {
  return (
    <button
      type={type}
      disabled={disabled || loading}
      className={`inline-flex shrink-0 items-center justify-center gap-1.5 rounded-control font-medium transition-colors disabled:opacity-40 ${VARIANTS[variant]} ${SIZES[size]} ${className}`}
      {...rest}
    >
      {loading && (
        <Loader2
          size={size === 'sm' ? ICON.xs : ICON.sm}
          className="shrink-0 animate-spin"
        />
      )}
      {children}
    </button>
  );
}

// IconButton — square, icon-only. `label` becomes the accessible name
// (and the tooltip), so icon buttons can never ship unlabelled.
//
// Tones: quiet (default, dims until hovered), primary (accent fill —
// the composer's send button) and danger (bordered, for stop/cancel).
export type IconButtonTone = 'quiet' | 'primary' | 'danger';

const ICON_TONES: Record<IconButtonTone, string> = {
  quiet: 'text-dim hover:bg-panel2 hover:text-fg',
  primary: 'bg-accent text-white hover:opacity-90',
  danger: 'border border-edge text-err hover:bg-panel2',
};

export function IconButton({
  label,
  size = 'md',
  tone = 'quiet',
  className = '',
  children,
  type = 'button',
  disabled,
  ...rest
}: Omit<ComponentProps<'button'>, 'children' | 'title' | 'aria-label'> & {
  label: string;
  size?: 'sm' | 'md' | 'lg';
  tone?: IconButtonTone;
  children: ReactNode;
}) {
  const box = size === 'sm' ? 'h-6 w-6' : size === 'lg' ? 'h-8 w-8' : 'h-7 w-7';
  const button = (
    <button
      type={type}
      data-tip={label}
      aria-label={label}
      disabled={disabled}
      className={`grid ${box} shrink-0 place-items-center rounded-control transition-colors disabled:opacity-40 ${ICON_TONES[tone]} ${className}`}
      {...rest}
    >
      {children}
    </button>
  );
  // A disabled control emits no pointer events, so the hint has to live on
  // a wrapper; without it the "why can't I click this" hint would never
  // appear exactly when it is needed.
  return disabled === true ? (
    <span data-tip={label} className="inline-flex shrink-0">
      {button}
    </span>
  ) : (
    button
  );
}
