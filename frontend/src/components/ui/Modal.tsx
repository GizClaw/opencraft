import type { ComponentType, ReactNode } from 'react';
import { X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { IconButton } from './Button';
import { ICON } from './icon';

// Modal — the centered dialog shell: overlay, panel, header with a
// close affordance, scrolling body and an optional footer (normally a
// <SaveBar> or an action row). Callers pass content, not chrome.
// bodyClassName replaces the body layout wholesale, so a full-bleed
// pane can opt out of the padding and the body scroller.
export function Modal({
  open,
  onClose,
  title,
  icon: Icon,
  width = '38rem',
  footer,
  ariaLabel,
  panelClassName = '',
  bodyClassName = 'overflow-y-auto px-4 py-3 space-y-3',
  children,
}: {
  open: boolean;
  onClose: () => void;
  title?: string;
  icon?: ComponentType<{ size?: string | number; className?: string }>;
  width?: string;
  footer?: ReactNode;
  ariaLabel?: string;
  panelClassName?: string;
  bodyClassName?: string;
  children: ReactNode;
}) {
  const { t } = useTranslation();
  if (!open) return null;
  return (
    <div
      className="fixed inset-0 z-[60] grid place-items-center bg-black/60 p-6"
      onClick={onClose}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label={ariaLabel ?? title}
        style={{ width }}
        onClick={(e) => e.stopPropagation()}
        className={`flex max-h-[calc(100vh-2rem)] max-w-full flex-col overflow-hidden rounded-card border border-edge bg-panel shadow-modal ${panelClassName}`}
      >
        {title !== undefined && (
          <div className="flex shrink-0 items-center justify-between gap-3 border-b border-edge px-4 py-3">
            <div className="flex min-w-0 items-center gap-2">
              {Icon && <Icon size={ICON.md} className="shrink-0 text-accent" />}
              <h3 className="min-w-0 truncate text-sm font-semibold">
                {title}
              </h3>
            </div>
            <IconButton label={t('tools.close')} onClick={onClose}>
              <X size={ICON.sm} />
            </IconButton>
          </div>
        )}
        <div className={`min-h-0 flex-1 ${bodyClassName}`}>{children}</div>
        {footer}
      </div>
    </div>
  );
}
