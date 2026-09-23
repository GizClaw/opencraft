import { useEffect, useRef } from 'react';
import { isEditableTarget, resolveShortcut, SHORTCUTS } from './keys';
import { overlayLayerOpen } from './overlay';

/**
 * useShortcuts installs the shell's one keyboard listener.
 *
 * It resolves the event against the table in lib/keys.ts and hands the
 * shortcut's id to `run`. The listener is a *capture*-phase listener on
 * the window: the shell sees the key before the focused surface does, so
 * ⌘K reaches the palette while the composer's editor holds the caret.
 * That precedence is why resolveShortcut — not the event's natural order —
 * decides who an Escape belongs to (see lib/keys.ts).
 *
 * `run` is read through a ref so a handler that closes over fresh state
 * does not re-register the listener on every render; only the platform
 * flag can change what a combo means, so only that re-registers.
 */
export function useShortcuts(run: (id: string) => void, isMac: boolean): void {
  const runRef = useRef(run);
  runRef.current = run;
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      const spec = resolveShortcut(SHORTCUTS, event, {
        isMac,
        overlayOpen: overlayLayerOpen(),
        editable: isEditableTarget(event.target),
      });
      if (spec === undefined) return;
      // The shell owns this key now: let go of it entirely, or the
      // focused surface also acts on it (StarterKit's link mark toggles
      // on ⌘K, a scroll container scrolls on ⌘↑).
      event.preventDefault();
      event.stopPropagation();
      runRef.current(spec.id);
    };
    window.addEventListener('keydown', onKeyDown, true);
    return () => window.removeEventListener('keydown', onKeyDown, true);
  }, [isMac]);
}
