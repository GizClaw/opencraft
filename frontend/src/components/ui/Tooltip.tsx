import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';

// Tooltip — the app's hover/focus hint, drawn by one shared layer.
//
// Every hint is an attribute (`data-tip`), and a single delegated layer
// renders at most one floating card. The alternative — a portal per hint —
// costs a subtree and a pair of listeners for each of the ~100 hints the
// app ships, most of them on static chrome. The native `title` attribute
// it replaces waits a browser-defined age before appearing, cannot wrap
// or theme itself, and is invisible on touch.
//
//   <button data-tip={t('chat.stop')} aria-label={t('chat.stop')}>…
//
// Hints appear after a short hover intent, immediately on keyboard focus,
// and never while a pointer is dragging. The layer is pointer-events-none
// so it cannot swallow the click that belongs to the control underneath.
const HOVER_DELAY_MS = 320;
const GAP = 6; // px between the anchor and the hint
const PAD = 8; // px kept between the hint and the window edge;

interface Hint {
  text: string;
  anchor: HTMLElement;
}

export function TooltipLayer() {
  const [hint, setHint] = useState<Hint | null>(null);
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null);
  const cardRef = useRef<HTMLDivElement | null>(null);
  const timer = useRef<number | null>(null);

  useEffect(() => {
    const cancel = () => {
      if (timer.current !== null) {
        window.clearTimeout(timer.current);
        timer.current = null;
      }
    };
    const hide = () => {
      cancel();
      setHint(null);
      setPos(null);
    };
    const show = (anchor: HTMLElement, delay: number) => {
      const text = anchor.dataset.tip ?? '';
      if (text === '') return;
      cancel();
      timer.current = window.setTimeout(() => {
        timer.current = null;
        setHint({ text, anchor });
      }, delay);
    };
    const owner = (target: EventTarget | null): HTMLElement | null =>
      target instanceof Element
        ? target.closest<HTMLElement>('[data-tip]')
        : null;

    const onPointerOver = (event: PointerEvent) => {
      const anchor = owner(event.target);
      if (anchor === null) {
        hide();
        return;
      }
      // A pointer that is already down is dragging (the sidebar resize
      // handle, selecting text); a hint would just be in the way.
      if (event.buttons !== 0) return;
      show(anchor, HOVER_DELAY_MS);
    };
    const onPointerOut = (event: PointerEvent) => {
      const anchor = owner(event.target);
      if (anchor === null) return;
      const next = event.relatedTarget;
      if (next instanceof Node && anchor.contains(next)) return;
      hide();
    };
    const onFocusIn = (event: FocusEvent) => {
      const anchor = owner(event.target);
      if (anchor === null) return;
      // Only keyboard focus earns an immediate hint; a click that lands
      // in a control should not flash a tooltip over it.
      if (!anchor.matches(':focus-visible')) return;
      show(anchor, 0);
    };
    const onFocusOut = () => hide();

    document.addEventListener('pointerover', onPointerOver);
    document.addEventListener('pointerout', onPointerOut);
    document.addEventListener('focusin', onFocusIn);
    document.addEventListener('focusout', onFocusOut);
    document.addEventListener('pointerdown', hide, true);
    document.addEventListener('keydown', hide, true);
    window.addEventListener('scroll', hide, true);
    window.addEventListener('blur', hide);
    return () => {
      cancel();
      document.removeEventListener('pointerover', onPointerOver);
      document.removeEventListener('pointerout', onPointerOut);
      document.removeEventListener('focusin', onFocusIn);
      document.removeEventListener('focusout', onFocusOut);
      document.removeEventListener('pointerdown', hide, true);
      document.removeEventListener('keydown', hide, true);
      window.removeEventListener('scroll', hide, true);
      window.removeEventListener('blur', hide);
    };
  }, []);

  // The hint is measured after it renders (its height depends on the
  // wrapped text), so it stays invisible for the first frame instead of
  // jumping into place.
  useLayoutEffect(() => {
    const card = cardRef.current;
    const anchor = hint?.anchor;
    if (card === null || anchor === undefined || !anchor.isConnected) return;
    const rect = anchor.getBoundingClientRect();
    const box = card.getBoundingClientRect();
    const vw = window.innerWidth;
    const vh = window.innerHeight;
    let top = rect.top - GAP - box.height;
    if (top < PAD) top = Math.min(vh - PAD - box.height, rect.bottom + GAP);
    const left = Math.max(
      PAD,
      Math.min(
        rect.left + rect.width / 2 - box.width / 2,
        vw - PAD - box.width,
      ),
    );
    setPos({ top: Math.max(PAD, top), left });
  }, [hint]);

  if (hint === null) return null;

  return createPortal(
    <div
      ref={cardRef}
      role="tooltip"
      data-testid="tooltip"
      style={{ top: pos?.top ?? 0, left: pos?.left ?? 0 }}
      className={`pointer-events-none fixed z-[var(--oc-z-tooltip)] max-w-[22rem] rounded-control border border-edge bg-panel3 px-2 py-1 text-xs leading-snug text-fg shadow-popover ${
        pos === null ? 'invisible' : 'animate-popover-in'
      }`}
    >
      {hint.text}
    </div>,
    document.body,
  );
}
