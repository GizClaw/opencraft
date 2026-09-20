import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { AlertTriangle, ShieldAlert, Trash2 } from 'lucide-react';
import { Overlay } from './Overlay';
import { Button } from './Button';
import { ICON } from './icon';

// ConfirmDialog — the one confirmation card. Six call sites used to
// hand-roll the same scrim + panel + cancel/confirm row (with three
// different widths and two different scrims) and none of them trapped
// focus or animated. The tone drives the header glyph and the frame:
//   danger    destructive (delete, remove, run unsandboxed)
//   warning   a consequence worth a pause (leaving a workspace, YOLO)
//   info      a plain yes/no
//
// Focus deliberately lands on the panel and the cancel button comes
// first: Enter on a freshly opened destructive confirm should not
// destroy anything.
export type ConfirmTone = 'danger' | 'warning' | 'info';

const TONE: Record<
  ConfirmTone,
  { frame: string; glyph: string; Icon: typeof AlertTriangle }
> = {
  danger: {
    frame: 'border-err/50',
    glyph: 'border-err/40 bg-err/15 text-err',
    Icon: Trash2,
  },
  warning: {
    frame: 'border-warn/50',
    glyph: 'border-warn/40 bg-warn/15 text-warn',
    Icon: AlertTriangle,
  },
  info: {
    frame: 'border-edge',
    glyph: 'border-edge bg-panel2 text-dim',
    Icon: ShieldAlert,
  },
};

export function ConfirmDialog({
  open,
  tone = 'danger',
  title,
  body,
  confirmLabel,
  cancelLabel,
  onConfirm,
  onCancel,
  /**
   * Gate the confirm button (a "type the branch name" style prompt). The
   * dialog stays open; only the button reports that it cannot go ahead.
   */
  confirmDisabled = false,
  width = '30rem',
  children,
}: {
  open: boolean;
  tone?: ConfirmTone;
  title: string;
  body?: ReactNode;
  confirmLabel: string;
  cancelLabel?: string;
  onConfirm: () => void;
  onCancel: () => void;
  confirmDisabled?: boolean;
  width?: string;
  children?: ReactNode;
}) {
  const { t } = useTranslation();
  const style = TONE[tone];
  const Icon = style.Icon;
  return (
    <Overlay
      open={open}
      onClose={onCancel}
      role="alertdialog"
      ariaLabel={title}
      panelClassName={`max-h-[calc(100vh-6rem)] overflow-y-auto rounded-card border bg-panel p-5 shadow-modal ${style.frame}`}
    >
      <div style={{ width }} className="max-w-[calc(100vw-3rem)]">
        <div className="flex items-start gap-3">
          <div
            className={`grid h-9 w-9 shrink-0 place-items-center rounded-card border ${style.glyph}`}
          >
            <Icon size={ICON.md} />
          </div>
          <div className="min-w-0 flex-1">
            <h2 className="text-title font-semibold leading-snug text-fg">
              {title}
            </h2>
            {typeof body === 'string' ? (
              <p className="mt-1 text-sm leading-relaxed text-dim">{body}</p>
            ) : (
              body
            )}
          </div>
        </div>
        {children}
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="quiet" onClick={onCancel}>
            {cancelLabel ?? t('interact.cancel')}
          </Button>
          <Button
            variant={tone === 'info' ? 'primary' : 'danger'}
            disabled={confirmDisabled}
            onClick={onConfirm}
          >
            {confirmLabel}
          </Button>
        </div>
      </div>
    </Overlay>
  );
}
