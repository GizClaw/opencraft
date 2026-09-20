import type { ComponentType, ReactNode } from 'react';
import { ICON } from './icon';

// EmptyState — the shape of "there is nothing here yet". Panels used to
// print one grey sentence ("No MCP servers configured yet.") where the
// chat surfaces showed a glyph, a headline and the action that fills the
// list; this is that second behaviour, available to every panel.
//
//   <EmptyState icon={Server} title={t('…')} hint={t('…')}>
//     <Button onClick={add}>{t('…')}</Button>
//   </EmptyState>
export function EmptyState({
  icon: Icon,
  title,
  hint,
  size = 'md',
  className = '',
  children,
}: {
  icon?: ComponentType<{ size?: string | number; className?: string }>;
  title: ReactNode;
  hint?: ReactNode;
  size?: 'sm' | 'md';
  className?: string;
  children?: ReactNode;
}) {
  const pad = size === 'sm' ? 'px-4 py-5' : 'px-6 py-8';
  return (
    <div
      className={`flex flex-col items-center justify-center gap-2 text-center ${pad} ${className}`}
    >
      {Icon && (
        <span className="grid h-9 w-9 place-items-center rounded-card border border-edge bg-panel2 text-dim">
          <Icon size={ICON.md} />
        </span>
      )}
      <div className="text-sm font-medium text-fg">{title}</div>
      {hint !== undefined && hint !== '' && (
        <div className="max-w-sm text-xs leading-relaxed text-faint">
          {hint}
        </div>
      )}
      {children !== undefined && (
        <div className="mt-1 flex items-center gap-2">{children}</div>
      )}
    </div>
  );
}
