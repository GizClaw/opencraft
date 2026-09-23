import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { createPortal } from 'react-dom';
import { useOverlayLayer, usePresence } from '../../lib/overlay';

// Popover — the anchored floating surface: menus, listboxes, suggestion
// cards. It owns the parts every hand-rolled menu re-implemented:
// measuring the anchor, flipping when there is no room below, clamping to
// the viewport, portaling out of the scroll container, closing on outside
// click / scroll / resize, owning Escape without closing the dialog
// underneath (through the shared layer stack), and the enter/exit
// animation.
//
// `keyboard` adds roving focus over the items ([role=option] /
// [role=menuitem]) so a menu is usable without a mouse; the item marked
// aria-selected or data-popover-active gets focus on open.
const GAP = 4; // px between the anchor and the panel
const PAD = 8; // px kept between the panel and the window edge

export function Popover({
  open,
  onClose,
  anchor,
  role = 'listbox',
  ariaLabel,
  align = 'start',
  matchWidth = false,
  keyboard = false,
  maxHeight,
  exitMs = 100,
  panelClassName = '',
  children,
}: {
  open: boolean;
  onClose: () => void;
  /** The trigger the panel is placed against. */
  anchor: HTMLElement | null;
  role?: 'listbox' | 'menu' | 'dialog' | 'none';
  ariaLabel?: string;
  align?: 'start' | 'end';
  /** Let the panel take the anchor's width (a select-style menu). */
  matchWidth?: boolean;
  keyboard?: boolean;
  /** Cap the panel height (px); the list scrolls inside. */
  maxHeight?: number;
  exitMs?: number;
  panelClassName?: string;
  children: ReactNode;
}) {
  const panelRef = useRef<HTMLDivElement | null>(null);
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null);
  const { mounted, closing } = usePresence(open, exitMs);
  useOverlayLayer({
    active: open,
    containerRef: panelRef,
    onDismiss: onClose,
    trap: false,
    lock: false,
  });

  const measure = useCallback(() => {
    const panel = panelRef.current;
    if (anchor === null || panel === null) return;
    const rect = anchor.getBoundingClientRect();
    const panelRect = panel.getBoundingClientRect();
    const vw = window.innerWidth;
    const vh = window.innerHeight;
    let top = rect.bottom + GAP;
    if (top + panelRect.height > vh - PAD) {
      const above = rect.top - GAP - panelRect.height;
      top = above >= PAD ? above : Math.max(PAD, vh - PAD - panelRect.height);
    }
    let left = align === 'end' ? rect.right - panelRect.width : rect.left;
    left = Math.max(PAD, Math.min(left, vw - PAD - panelRect.width));
    setPos({ top, left });
  }, [anchor, align]);

  useLayoutEffect(() => {
    if (!open) return;
    setPos(null);
    measure();
  }, [open, measure, mounted]);

  useEffect(() => {
    if (!open) return;
    const onPointerDown = (event: MouseEvent) => {
      const target = event.target as Node;
      if (panelRef.current?.contains(target)) return;
      if (anchor?.contains(target)) return;
      onClose();
    };
    // A scroll or resize invalidates the measured anchor, but only a
    // scroll that can move it counts. The panel's own list scrolls
    // independently — resting a trackpad on it must not close the menu
    // it is scrolling — and a scroller the anchor is not inside moved
    // nothing this menu is placed against: the chat transcript, the
    // activity card's tails and every other pane that scrolls on its own
    // used to dismiss any open menu anywhere in the app.
    const onScroll = (event: Event) => {
      const target = event.target;
      if (target instanceof Node) {
        if (panelRef.current?.contains(target)) return;
        const pageScroll =
          target === document ||
          target === document.documentElement ||
          target === document.body;
        if (!pageScroll && !target.contains(anchor)) return;
      }
      onClose();
    };
    document.addEventListener('mousedown', onPointerDown);
    window.addEventListener('scroll', onScroll, true);
    window.addEventListener('resize', onClose);
    return () => {
      document.removeEventListener('mousedown', onPointerDown);
      window.removeEventListener('scroll', onScroll, true);
      window.removeEventListener('resize', onClose);
    };
  }, [open, onClose, anchor]);

  useEffect(() => {
    if (!open || !keyboard || pos === null) return;
    const panel = panelRef.current;
    if (panel === null) return;
    const items = menuItems(panel);
    const preferred =
      panel.querySelector<HTMLElement>(
        '[data-popover-active], [aria-selected="true"]',
      ) ?? items[0];
    preferred?.focus({ preventScroll: true });
  }, [open, keyboard, pos]);

  useEffect(() => {
    if (!open || !keyboard) return;
    const onKeyDown = (event: KeyboardEvent) => {
      const panel = panelRef.current;
      if (panel === null) return;
      if (!panel.contains(document.activeElement)) {
        const target = event.target;
        if (!(target instanceof Node) || !panel.contains(target)) return;
      }
      const items = menuItems(panel);
      if (items.length === 0) return;
      const current = items.indexOf(document.activeElement as HTMLElement);
      let next = -1;
      switch (event.key) {
        case 'ArrowDown':
          next = current < 0 ? 0 : (current + 1) % items.length;
          break;
        case 'ArrowUp':
          next =
            current < 0
              ? items.length - 1
              : (current - 1 + items.length) % items.length;
          break;
        case 'Home':
          next = 0;
          break;
        case 'End':
          next = items.length - 1;
          break;
        default:
          return;
      }
      event.preventDefault();
      items[next]?.focus({ preventScroll: true });
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [open, keyboard]);

  if (!mounted) return null;

  return createPortal(
    <div
      ref={(node) => {
        panelRef.current = node;
        if (node !== null && pos === null) {
          const rect = node.getBoundingClientRect();
          if (anchor !== null && rect.height > 0) measure();
        }
      }}
      role={role === 'none' ? undefined : role}
      aria-label={ariaLabel}
      style={{
        top: pos?.top ?? 0,
        left: pos?.left ?? 0,
        width:
          matchWidth && anchor
            ? anchor.getBoundingClientRect().width
            : undefined,
        maxHeight,
        visibility: pos === null ? 'hidden' : undefined,
      }}
      className={`fixed z-[var(--oc-z-menu)] ${maxHeight === undefined ? '' : 'overflow-y-auto'} ${
        closing ? 'animate-popover-out' : 'animate-popover-in'
      } ${panelClassName}`}
    >
      {children}
    </div>,
    document.body,
  );
}

/** menuItems lists the focusable rows of a popover panel, in DOM order. */
export function menuItems(panel: HTMLElement): HTMLElement[] {
  return Array.from(
    panel.querySelectorAll<HTMLElement>('[role="option"], [role="menuitem"]'),
  ).filter(
    (el) => !el.hasAttribute('disabled') && el.getClientRects().length > 0,
  );
}

/** Exit duration of a popover, for callers that omit it. */
