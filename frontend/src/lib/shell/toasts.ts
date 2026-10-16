import type { StoreApi } from 'zustand';
import type { StoreState } from '../store';

// Toasts: the status line's counterpart for things that happened away
// from the window's focus. S4 moved these out of store.ts (move only).

export function createShellToasts(
  store: StoreApi<StoreState>,
): Pick<StoreState, 'flash' | 'toast' | 'dismissToast'> {
  const set = store.setState;
  const get = store.getState;

  let toastSeq = 0;

  return {
    flash: (text) => get().toast(text),

    toast: (text, kind = 'info') => {
      const id = ++toastSeq;
      set((state) => ({ toasts: [...state.toasts, { id, text, kind }] }));
      setTimeout(() => {
        set((state) => ({
          toasts: state.toasts.filter((t) => t.id !== id),
        }));
      }, 3500);
    },

    dismissToast: (id) =>
      set((state) => ({
        toasts: state.toasts.filter((t) => t.id !== id),
      })),
  };
}
