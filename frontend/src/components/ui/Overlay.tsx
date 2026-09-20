import { useRef, type ReactNode } from 'react';
import { MOTION, useOverlayLayer, usePresence } from '../../lib/overlay';

// Overlay is the shell every window-level surface shares: the scrim, the
// panel placement, the enter/exit animation and the behaviour that was
// previously copy-pasted (Escape ownership, focus trap, scroll lock,
// focus restore, click-outside-to-close).
//
// Variants:
//   center        the classic dialog (settings-sized cards, confirms)
//   top           a command surface pinned near the top of the window
//   drawer-right  a full-height side drawer
//   bare          no scrim and no placement: the caller positions the
//                 panel (used for surfaces that only want the behaviour)
//
// The caller owns the panel's own chrome (width, padding, header) via
// panelClassName; the overlay owns everything around it.
export type OverlayVariant = 'center' | 'top' | 'drawer-right' | 'bare';

const SCRIM: Record<Exclude<OverlayVariant, 'bare'>, string> = {
  center: 'bg-scrim',
  top: 'bg-scrim',
  'drawer-right': 'bg-scrim-soft',
};

const PLACEMENT: Record<OverlayVariant, string> = {
  center: 'grid place-items-center p-6',
  top: 'flex items-start justify-center px-6 pt-[12vh]',
  'drawer-right': 'flex justify-end',
  bare: '',
};

const PANEL_ANIM: Record<OverlayVariant, [string, string]> = {
  center: ['animate-panel-in', 'animate-panel-out'],
  top: ['animate-panel-in', 'animate-panel-out'],
  'drawer-right': ['animate-drawer-in', 'animate-drawer-out'],
  bare: ['animate-panel-in', 'animate-panel-out'],
};

export function Overlay({
  open,
  onClose,
  variant = 'center',
  /**
   * Selector of the element to focus on open. Defaults to the panel
   * itself, or whatever already holds focus inside it (autoFocus).
   */
  initialFocus,
  trap = true,
  lock = true,
  restoreFocus = true,
  exitMs = MOTION.fast,
  role = 'dialog',
  ariaLabel,
  ariaLabelledBy,
  panelClassName = '',
  className = '',
  children,
}: {
  open: boolean;
  onClose?: () => void;
  variant?: OverlayVariant;
  initialFocus?: string | false;
  trap?: boolean;
  lock?: boolean;
  restoreFocus?: boolean;
  exitMs?: number;
  role?: 'dialog' | 'alertdialog' | 'none';
  ariaLabel?: string;
  ariaLabelledBy?: string;
  panelClassName?: string;
  className?: string;
  children: ReactNode;
}) {
  const panelRef = useRef<HTMLDivElement | null>(null);
  const pressRef = useRef(false);
  const { mounted, closing } = usePresence(open, exitMs);
  useOverlayLayer({
    active: open,
    containerRef: panelRef,
    onDismiss: onClose,
    initialFocus,
    trap,
    lock,
    restoreFocus,
  });

  if (!mounted) return null;
  const [enter, leave] = PANEL_ANIM[variant];
  const scrim = variant === 'bare' ? null : SCRIM[variant];
  // A press that starts inside the panel and ends on the scrim (a drag
  // out of a text selection) must not dismiss: the browser reports that
  // click on the scrim.
  const pressedInside = pressRef.current;

  return (
    <div
      className={`fixed inset-0 z-[var(--oc-z-overlay)] ${PLACEMENT[variant]} ${
        scrim
          ? `${scrim} ${closing ? 'animate-scrim-out' : 'animate-scrim-in'}`
          : ''
      } ${className}`}
      onMouseDown={(event) => {
        pressRef.current = event.target !== event.currentTarget;
      }}
      onClick={(event) => {
        if (variant === 'bare' || onClose === undefined) return;
        if (pressedInside) return;
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div
        ref={panelRef}
        role={role === 'none' ? undefined : role}
        aria-modal={role === 'none' ? undefined : true}
        aria-label={ariaLabel}
        aria-labelledby={ariaLabelledBy}
        className={`${closing ? leave : enter} ${panelClassName}`}
      >
        {children}
      </div>
    </div>
  );
}
