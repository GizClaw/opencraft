import { useEffect, useState } from 'react';
import { Copy, Minus, Square, X } from 'lucide-react';
import {
  WindowIsMaximised,
  WindowMinimise,
  WindowToggleMaximise,
} from '../../wailsjs/runtime/runtime';
import { api } from '../lib/api';

// WindowControls renders the custom minimize/maximize/close buttons for
// the frameless Windows/Linux window. It is only mounted inside the
// TopBar, which macOS never renders (macOS uses the native traffic
// lights instead). The buttons live inside the draggable top bar, so
// they opt out of window dragging via the no-drag marker (CSS custom
// properties inherit through the DOM).
export function WindowControls() {
  const [maximised, setMaximised] = useState(false);

  useEffect(() => {
    let alive = true;
    void WindowIsMaximised()
      .then((m) => {
        if (alive) setMaximised(m);
      })
      .catch(() => {});
    // Reconcile the icon whenever the window is resized (double-click
    // maximize, Windows snap, keyboard shortcuts, …).
    const onResize = () => {
      void WindowIsMaximised()
        .then(setMaximised)
        .catch(() => {});
    };
    window.addEventListener('resize', onResize);
    return () => {
      alive = false;
      window.removeEventListener('resize', onResize);
    };
  }, []);

  const toggleMaximise = () => {
    WindowToggleMaximise();
    // Optimistic flip; the resize listener reconciles afterwards.
    setMaximised((m) => !m);
  };

  return (
    <div
      className="flex h-full shrink-0 items-stretch"
      style={{ ['--wails-draggable' as string]: 'no-drag' }}
    >
      <button
        onClick={WindowMinimise}
        aria-label="Minimize"
        className="grid w-12 place-items-center text-dim transition-colors hover:bg-panel2 hover:text-fg"
      >
        <Minus size="1.0714rem" />
      </button>
      <button
        onClick={toggleMaximise}
        aria-label={maximised ? 'Restore' : 'Maximize'}
        className="grid w-12 place-items-center text-dim transition-colors hover:bg-panel2 hover:text-fg"
      >
        {maximised ? <Copy size="0.9286rem" /> : <Square size="0.8571rem" />}
      </button>
      <button
        // Route through Go: the "close to tray / quit" setting decides
        // whether this hides the window or terminates the app.
        onClick={() => void api.closeRequested()}
        aria-label="Close"
        className="grid w-12 place-items-center text-dim transition-colors hover:bg-[#e81123] hover:text-white"
      >
        <X size="1.1429rem" />
      </button>
    </div>
  );
}
