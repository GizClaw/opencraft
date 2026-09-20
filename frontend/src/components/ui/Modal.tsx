import type { ComponentType, ReactNode } from 'react';
import { X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { IconButton } from './Button';
import { Overlay } from './Overlay';
import { ICON } from './icon';

// Modal — the centered dialog shell: scrim, panel, header with a close
// affordance, scrolling body and an optional footer (normally a
// <SaveBar> or an action row). Callers pass content, not chrome.
//
// The shell owns the overlay behaviour (Escape, focus trap, scroll lock,
// focus restore, enter/exit animation) through <Overlay>; a dialog that
// rolled its own scrim used to get none of it. Focus lands on the panel,
// not on the header's close button: mark the field the user should type
// into with `data-autofocus`, or pass initialFocus as a selector.
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
  ariaLabelledBy,
  panelClassName = '',
  bodyClassName = 'overflow-y-auto px-4 py-3 space-y-3',
  initialFocus = false,
  children,
}: {
  open: boolean;
  onClose: () => void;
  title?: string;
  icon?: ComponentType<{ size?: string | number; className?: string }>;
  width?: string;
  footer?: ReactNode;
  ariaLabel?: string;
  ariaLabelledBy?: string;
  panelClassName?: string;
  bodyClassName?: string;
  initialFocus?: string | false;
  children: ReactNode;
}) {
  const { t } = useTranslation();
  return (
    <Overlay
      open={open}
      onClose={onClose}
      initialFocus={initialFocus}
      ariaLabel={ariaLabel ?? (ariaLabelledBy ? undefined : title)}
      ariaLabelledBy={ariaLabelledBy}
      panelClassName={`flex max-h-[calc(100vh-2rem)] max-w-full flex-col overflow-hidden rounded-card border border-edge bg-panel shadow-modal ${panelClassName}`}
    >
      <div style={{ width }} className="flex min-h-0 flex-col">
        {title !== undefined && (
          <div className="flex shrink-0 items-center justify-between gap-3 border-b border-edge px-4 py-3">
            <div className="flex min-w-0 items-center gap-2">
              {Icon && <Icon size={ICON.md} className="shrink-0 text-accent" />}
              <h3 className="min-w-0 truncate text-title font-semibold">
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
    </Overlay>
  );
}
