import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  useSyncExternalStore,
  type RefObject,
} from 'react';
import { imeKeyOwner } from './ime';

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
const layerListeners = new Set<() => void>();

function notifyLayers() {
  for (const listener of layerListeners) listener();
}

function subscribeLayers(listener: () => void): () => void {
  layerListeners.add(listener);
  return () => {
    layerListeners.delete(listener);
  };
}

function overlayOpen(): boolean {
  return layers.length > 0;
}

function onEscape(event: KeyboardEvent) {
  // The IME owns its keys: a composition cancel is not a request to close
  // the dialog, and the stray Escape Chromium delivers after the cancel
  // (lib/ime.ts) is cancelled at the source before it reaches this layer.
  if (event.key !== 'Escape' || imeKeyOwner(event) !== null) return;
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
  notifyLayers();
  if (!listening) {
    window.addEventListener('keydown', onEscape, true);
    listening = true;
  }
  return () => {
    const index = layers.indexOf(layer);
    if (index >= 0) layers.splice(index, 1);
    notifyLayers();
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
 * focusBack returns focus to a stored anchor. The anchor can outlive the
 * layer that stored it (a menu trigger inside a dialog that just closed);
 * focusing an orphan would throw focus to the body.
 */
function focusBack(target: HTMLElement | null) {
  if (target?.isConnected) target.focus({ preventScroll: true });
}

/**
 * panelKeepsFocus reports whether a closing layer should hand focus back
 * to its anchor: true while the layer, or nothing at all, owns the
 * keyboard. A close caused by a click on another control has already
 * moved focus there, and pulling it back would steal the caret from what
 * the user just reached for.
 */
function panelKeepsFocus(panel: HTMLElement | null): boolean {
  const current = document.activeElement;
  if (current === null || current === document.body) return true;
  return panel !== null && panel.contains(current);
}

/**
 * useOverlayOpen reports whether any overlay layer currently owns the
 * keyboard. Surfaces outside the layer registry (the transcript's
 * interaction card) use it to stay passive while a menu, a palette or a
 * dialog is open — and to take the keyboard as soon as the last one
 * closes.
 */
export function useOverlayOpen(): boolean {
  return useSyncExternalStore(subscribeLayers, overlayOpen, overlayOpen);
}

/**
 * overlayLayerOpen reads the registry directly rather than subscribing:
 * a surface that checks it inside an effect sees a layer that
 * registered earlier in the same commit, which the render-time snapshot
 * above cannot.
 */
export function overlayLayerOpen(): boolean {
  return layers.length > 0;
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
  // Where focus goes when the layer closes. The layout effect below
  // reads it at open time; the effect after it re-asserts the same
  // target once the commit is over.
  const restoreToRef = useRef<HTMLElement | null>(null);

  useLayoutEffect(() => {
    if (!active) return;
    const restoreTo = document.activeElement as HTMLElement | null;
    restoreToRef.current = restoreTo;
    const release = trackLatest(() => dismissRef.current);
    if (lock) lockScroll();
    return () => {
      release();
      if (lock) unlockScroll();
      if (restoreFocus && panelKeepsFocus(containerRef.current)) {
        focusBack(restoreTo);
      }
    };
  }, [active, lock, restoreFocus]);

  // React restores focus itself at the end of the commit's mutation
  // phase: it remembers what had it when the commit started and puts it
  // back. A panel that is still on screen for its exit animation is a
  // live element, so the restore above gets undone and the focus dies
  // with the panel — the composer lost the caret every time the ⌘K
  // palette closed. Re-assert once the commit is over, and only when the
  // panel took focus back, so a surface that opened in the same commit
  // keeps it.
  useEffect(() => {
    if (active || !restoreFocus) return;
    const panel = containerRef.current;
    if (panel === null || !panel.contains(document.activeElement)) return;
    focusBack(restoreToRef.current);
  }, [active, containerRef, restoreFocus]);

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
 *
 * `mounted` catches up to `open` in a layout effect, so a caller that
 * renders nothing while it is false spends the opening commit off-DOM —
 * and the effects that wire the surface up (initial focus, the Tab trap,
 * measuring) then run against a null ref and never run again. Surfaces
 * whose wiring must see the panel in that commit render on
 * `open || mounted` instead.
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
