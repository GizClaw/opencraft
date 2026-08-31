import { useEffect, useState } from 'react';
import { Copy, Minus, Square, X } from 'lucide-react';
import {
  Environment,
  Quit,
  WindowIsMaximised,
  WindowMinimise,
  WindowToggleMaximise,
} from '../../wailsjs/runtime/runtime';

// WindowControls renders the custom minimize/maximize/close buttons for
// the frameless Windows/Linux window. macOS uses the native traffic
// lights, so it renders nothing there. The buttons live inside the
// draggable title strip, so they opt out of window dragging via the
// no-drag marker (CSS custom properties inherit through the DOM).
export function WindowControls() {
  const [isMac, setIsMac] = useState(false);
  const [maximised, setMaximised] = useState(false);

  useEffect(() => {
    let alive = true;
    void Environment().then((env) => {
      if (!alive) return;
      setIsMac(env.platform === 'darwin');
      if (env.platform !== 'darwin') {
        void WindowIsMaximised()
          .then((m) => {
            if (alive) setMaximised(m);
          })
          .catch(() => {});
      }
    });
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

  if (isMac) return null;

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
        <Minus size={15} />
      </button>
      <button
        onClick={toggleMaximise}
        aria-label={maximised ? 'Restore' : 'Maximize'}
        className="grid w-12 place-items-center text-dim transition-colors hover:bg-panel2 hover:text-fg"
      >
        {maximised ? <Copy size={13} /> : <Square size={12} />}
      </button>
      <button
        onClick={Quit}
        aria-label="Close"
        className="grid w-12 place-items-center text-dim transition-colors hover:bg-[#e81123] hover:text-white"
      >
        <X size={16} />
      </button>
    </div>
  );
}
