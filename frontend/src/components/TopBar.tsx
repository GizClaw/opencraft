import { Window } from '@wailsio/runtime';
import { WindowControls } from './WindowControls';

// TopBar is the full-width window title strip for the frameless
// Windows/Linux window. It spans the entire app (sidebar + content),
// doubles as the window drag area, and hosts the custom window controls
// on the right. macOS keeps its native traffic lights anchored in the
// sidebar strip, so this renders nothing there.
export function TopBar({ isMac }: { isMac: boolean }) {
  if (isMac) return null;

  return (
    <div
      className="h-11 shrink-0 border-b border-edge bg-panel flex items-center select-none"
      style={{ ['--wails-draggable' as string]: 'drag' }}
      onDoubleClick={() => void Window.ToggleMaximise()}
    >
      <div className="ml-auto h-full">
        <WindowControls />
      </div>
    </div>
  );
}
