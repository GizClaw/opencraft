import { useLayoutEffect, useRef, useState, type RefObject } from 'react';

// Floating-surface behaviour, shared by every overlay in the app: the
// Escape stack, the focus trap, the scroll lock and the enter/exit
// presence. Each one used to be re-implemented per dialog (ten window
// keydown listeners, no focus trap anywhere, no exit animation), which
// is why Escape closed the wrong surface and menus closed the dialog
// underneath them.
//
// Layers register in mount order. Escape is handled once, by a single
// capturing listener on the window, and belongs to the topmost layer:
// the listener stops propagation either way, so the layers below (and
// any legacy window-level handler) never see the key. A layer without an
// onDismiss still swallows Escape — a menu that cannot be dismissed must
// not close the dialog behind it.
//
// Registration is a *layout* effect on purpose. React commits the DOM in
// one task and runs passive effects in another, so a surface wired in
// useEffect is on screen for a moment while nobody owns the key yet, and
// an Escape landing in that window falls through to whatever is
// underneath (test/outsideAct.ts mounts outside act to pin this down).
// The listener sits on the window rather than the document so a key
// dispatched at either of them reaches it.

/** Exit durations, mirroring the --animate-*-out tokens in style.css. */
export const MOTION = { fast: 120, base: 160, drawer: 180 } as const;

interface Layer {
  dismiss: (() => void) | undefined;
}

const layers: Layer[] = [];
let listening = false;

function onEscape(event: KeyboardEvent) {
  if (event.key !== 'Escape' || event.isComposing) return;
  if (event.defaultPrevented) return;
  const top = layers[layers.length - 1];
  if (top === undefined) return;
  // Escape never reaches a lower layer, whether or not this one closes.
  event.preventDefault();
  event.stopPropagation();
  top.dismiss?.();
}

function track(dismiss: (() => void) | undefined): () => void {
  const layer: Layer = { dismiss };
  layers.push(layer);
  if (!listening) {
    window.addEventListener('keydown', onEscape, true);
    listening = true;
  }
  return () => {
    const index = layers.indexOf(layer);
    if (index >= 0) layers.splice(index, 1);
    if (layers.length === 0 && listening) {
      window.removeEventListener('keydown', onEscape, true);
      listening = false;
    }
  };
}

// The dismiss callback changes identity on every render; the layer keeps
// calling the newest one without re-registering (which would reorder the
// stack while a nested surface is open).
function trackLatest(get: () => (() => void) | undefined): () => void {
  return track(() => get()?.());
}

let scrollLocks = 0;
let overflowBefore = '';

function lockScroll() {
  if (scrollLocks === 0) {
    overflowBefore = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
  }
  scrollLocks += 1;
}

function unlockScroll() {
  scrollLocks = Math.max(0, scrollLocks - 1);
  if (scrollLocks === 0) document.body.style.overflow = overflowBefore;
}

const FOCUSABLE = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled]):not([type="hidden"])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(',');

function focusable(container: HTMLElement): HTMLElement[] {
  return Array.from(container.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
    (el) =>
      el.offsetParent !== null ||
      el === document.activeElement ||
      el.getClientRects().length > 0,
  );
}

/**
 * useOverlayLayer wires one floating surface into the shared overlay
 * behaviour while `active`. `onDismiss` runs when this layer owns
 * Escape; omit it for a surface Escape must not close (a destructive
 * confirm, a blocking prompt) — the key is still consumed.
 *
 * `containerRef` points at the panel that receives focus, traps Tab and
 * restores focus from.
 */
export function useOverlayLayer({
  active,
  containerRef,
  onDismiss,
  // Menus and tooltips hand focus back to their trigger when they close;
  // dialogs restore whatever was focused before they opened.
  restoreFocus = true,
  trap = true,
  lock = true,
  initialFocus,
}: {
  active: boolean;
  containerRef: RefObject<HTMLElement | null>;
  onDismiss?: () => void;
  restoreFocus?: boolean;
  trap?: boolean;
  lock?: boolean;
  /**
   * Where focus goes on open: a selector, or `false` for the panel
   * itself. Anything already focused inside the panel wins — that is how
   * a form field marked `autoFocus` (or a menu item focused by its own
   * effect) keeps focus instead of being stolen by the trap.
   */
  initialFocus?: string | false;
}) {
  const dismissRef = useRef(onDismiss);
  dismissRef.current = onDismiss;

  useLayoutEffect(() => {
    if (!active) return;
    const restoreTo = document.activeElement as HTMLElement | null;
    const release = trackLatest(() => dismissRef.current);
    if (lock) lockScroll();
    return () => {
      release();
      if (lock) unlockScroll();
      if (restoreFocus && restoreTo?.isConnected) {
        // The anchor can outlive us (a menu trigger inside a dialog that
        // just closed); focusing an orphan would throw focus to the body.
        restoreTo.focus({ preventScroll: true });
      }
    };
  }, [active, lock, restoreFocus]);

  useLayoutEffect(() => {
    if (!active) return;
    const panel = containerRef.current;
    if (panel === null) return;
    const current = document.activeElement;
    const keep = current instanceof HTMLElement && panel.contains(current);
    const target = keep
      ? current
      : ((typeof initialFocus === 'string'
          ? panel.querySelector<HTMLElement>(initialFocus)
          : null) ??
        panel.querySelector<HTMLElement>('[data-autofocus]') ??
        // A panel that is not focusable would drop the focus on the body
        // and let Tab escape the trap.
        panel);
    // A panel that is not focusable would drop the focus on the body and
    // let Tab escape the trap.
    if (!target.hasAttribute('tabindex') && target === panel) {
      panel.setAttribute('tabindex', '-1');
    }
    if (target !== current) target.focus({ preventScroll: true });

    if (!trap) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Tab') return;
      const items = focusable(panel);
      if (items.length === 0) {
        event.preventDefault();
        panel.focus({ preventScroll: true });
        return;
      }
      const first = items[0];
      const last = items[items.length - 1];
      const current = document.activeElement;
      if (event.shiftKey && (current === first || !panel.contains(current))) {
        event.preventDefault();
        last.focus({ preventScroll: true });
        return;
      }
      if (!event.shiftKey && current === last) {
        event.preventDefault();
        first.focus({ preventScroll: true });
      }
    };
    panel.addEventListener('keydown', onKeyDown);
    return () => panel.removeEventListener('keydown', onKeyDown);
  }, [active, containerRef, initialFocus, trap]);
}

/**
 * usePresence keeps a surface mounted for the length of its exit
 * animation. `open` drives the children; `closing` is true from the
 * moment it flips false until the unmount, so the caller can swap in the
 * exit animation class.
 */
export function usePresence(open: boolean, exitMs: number = MOTION.fast) {
  const [mounted, setMounted] = useState(open);
  const [closing, setClosing] = useState(false);

  useLayoutEffect(() => {
    if (open) {
      setMounted(true);
      setClosing(false);
      return;
    }
    if (!mounted) return;
    setClosing(true);
    const timer = window.setTimeout(() => {
      setClosing(false);
      setMounted(false);
    }, exitMs);
    return () => window.clearTimeout(timer);
  }, [open, mounted, exitMs]);

  return { mounted, closing };
}
