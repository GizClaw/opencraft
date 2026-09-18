// Mount helpers for tests that need the window between a commit and
// React's passive effects.
//
// Testing Library wraps rendering and events in act(), which flushes
// passive effects before control returns to the test. That hides a real
// ordering hazard: in the browser (and on a loaded CI runner) React
// commits the DOM in one task and runs the passive effects that install
// event listeners in another, so a listener registered with useEffect is
// absent for a moment after its UI is already on screen. A key event
// landing in that window is delivered to nobody — and if a surface
// underneath (a dialog's page, a parent panel) owns the same key, it
// handles it instead.
//
// These helpers mount outside act on purpose and wake the test in the
// microtask right after the DOM mutation, i.e. before the passive-effect
// task. A listener installed by useLayoutEffect is present at that point;
// one installed by useEffect is not. Use them for contracts of the shape
// "once this is on screen, it owns this key".
import { act } from '@testing-library/react';
import { createRoot, type Root } from 'react-dom/client';
import type { ReactNode } from 'react';

// The flag react-dom reads to decide whether act() is in charge.
// Reaching it through a cast keeps this decoupled from the global type
// declarations shifting between React versions.
type ActEnvironment = { IS_REACT_ACT_ENVIRONMENT?: boolean };
const actEnvironment = globalThis as ActEnvironment;

export interface OutsideActMount {
  container: HTMLElement;
  // waitForDom resolves in a microtask after the predicate starts
  // matching, so nothing scheduled as a macrotask has run yet.
  waitForDom: (predicate: () => boolean) => Promise<void>;
  unmount: () => Promise<void>;
}

/**
 * Mounts a tree with act() disabled so passive effects stay pending
 * until the event loop advances.
 */
export function mountOutsideAct(node: ReactNode): OutsideActMount {
  const container = document.createElement('div');
  document.body.appendChild(container);
  const previous = actEnvironment.IS_REACT_ACT_ENVIRONMENT;
  actEnvironment.IS_REACT_ACT_ENVIRONMENT = false;
  const root: Root = createRoot(container);
  root.render(node);

  const waitForDom = (predicate: () => boolean) =>
    new Promise<void>((resolve) => {
      if (predicate()) {
        resolve();
        return;
      }
      const observer = new MutationObserver(() => {
        if (!predicate()) return;
        observer.disconnect();
        resolve();
      });
      observer.observe(container, { childList: true, subtree: true });
    });

  const unmount = async () => {
    root.unmount();
    container.remove();
    actEnvironment.IS_REACT_ACT_ENVIRONMENT = previous;
    // Restoring the flag re-enables act(); draining here flushes the
    // pending passive cleanup so no listener outlives the test.
    await act(async () => {});
  };

  return { container, waitForDom, unmount };
}

/** Dispatches Escape the way a browser would, on the document target. */
export function pressEscape(): void {
  document.dispatchEvent(
    new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }),
  );
}
