import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  SIDEBAR_DEFAULT_WIDTH,
  SIDEBAR_FINE_STEP,
  SIDEBAR_MAX_WIDTH,
  SIDEBAR_MIN_WIDTH,
  SIDEBAR_STEP,
  clampSidebarWidth,
  writeSidebarWidth,
} from '../lib/sidebarWidth';

// SidebarResizeHandle — the boundary between the sidebar and the work
// column, and the only control that moves it.
//
// It owns the hairline the two columns meet on (.oc-sidebar-handle), so
// the pointer, the keyboard and the value it reports all describe the same
// 1px line — which is why a width is a whole number of pixels. A drag
// takes the pointer capture, because the pointer spends most of the
// gesture out over the transcript, and reports every move live; the stored
// preference is written once the gesture ends instead of a few hundred
// times on the way.
//
// The shell owns the width (it sizes the column); the handle owns the
// gesture and the stored preference, because how it was last set belongs
// to the handle rather than to the layout.
export interface SidebarResizeHandleProps {
  /** Current column width in whole pixels. */
  value: number;
  /** Called with each new width: live during a drag, once per nudge. */
  onChange: (width: number) => void;
}

export function SidebarResizeHandle({
  value,
  onChange,
}: SidebarResizeHandleProps) {
  const { t } = useTranslation();
  const [resizing, setResizing] = useState(false);
  const origin = useRef<{ x: number; width: number } | null>(null);

  useEffect(() => {
    if (resizing) return;
    writeSidebarWidth(value);
  }, [resizing, value]);

  // A resize owns the pointer for its whole gesture: the cursor is the
  // handle's wherever the pointer goes, and a drag cannot select the text
  // it passes over. Scoped to the body, because the seam is not where the
  // pointer is.
  useEffect(() => {
    document.body.classList.toggle('oc-resizing', resizing);
    return () => document.body.classList.remove('oc-resizing');
  }, [resizing]);

  const onPointerDown = (event: React.PointerEvent<HTMLDivElement>) => {
    // Keeps the press from starting a text selection or a native drag the
    // window would chase; focus is taken by hand because preventDefault
    // suppresses the browser's own.
    event.preventDefault();
    event.currentTarget.focus();
    event.currentTarget.setPointerCapture(event.pointerId);
    origin.current = { x: event.clientX, width: value };
    setResizing(true);
  };

  const onPointerMove = (event: React.PointerEvent<HTMLDivElement>) => {
    const start = origin.current;
    if (start === null) return;
    onChange(clampSidebarWidth(start.width + event.clientX - start.x));
  };

  const endDrag = (event: React.PointerEvent<HTMLDivElement>) => {
    if (origin.current === null) return;
    origin.current = null;
    setResizing(false);
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
  };

  // The seam is the only way to resize the column, so it answers to the
  // arrow keys as well as to a drag: a press moves it a notch, Shift asks
  // for the single pixel a pointer cannot land.
  const onKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    const step = event.shiftKey ? SIDEBAR_FINE_STEP : SIDEBAR_STEP;
    const delta =
      event.key === 'ArrowLeft' ? -step : event.key === 'ArrowRight' ? step : 0;
    if (delta === 0) return;
    event.preventDefault();
    onChange(clampSidebarWidth(value + delta));
  };

  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label={t('sidebar.resize')}
      aria-valuenow={value}
      aria-valuemin={SIDEBAR_MIN_WIDTH}
      aria-valuemax={SIDEBAR_MAX_WIDTH}
      tabIndex={0}
      data-dragging={resizing || undefined}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={endDrag}
      onPointerCancel={endDrag}
      onLostPointerCapture={endDrag}
      onKeyDown={onKeyDown}
      onDoubleClick={() => onChange(SIDEBAR_DEFAULT_WIDTH)}
      className="oc-sidebar-handle"
    >
      {/* The knob carries the hint: it is the part of the seam a pointer
          aims at, so the hint lands next to the pointer instead of at the
          top of a full-height element. */}
      <span
        className="oc-sidebar-knob"
        data-tip={t('sidebar.resizeHint')}
        aria-hidden="true"
      />
    </div>
  );
}
