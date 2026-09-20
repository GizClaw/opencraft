import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { AlertTriangle } from 'lucide-react';
import { Overlay } from './ui/Overlay';
import { Button } from './ui/Button';
import { YOLO_RISKS } from '../lib/sessionModes';
import { ICON } from './ui/icon';

// YoloConfirmDialog is the shared "switch to YOLO" warning shared by the
// composer and the General settings tab. It renders on the window-level
// overlay rung, so it floats above both the chat and the settings page
// without the caller choosing a z-index.
export function YoloConfirmDialog({
  title,
  intro,
  scope,
  confirmLabel,
  cancelLabel,
  onCancel,
  onConfirm,
  children,
}: {
  title: string;
  intro: string;
  scope: string;
  confirmLabel: string;
  cancelLabel?: string;
  onCancel: () => void;
  onConfirm: () => void;
  children?: ReactNode;
}) {
  const { t } = useTranslation();
  return (
    <Overlay
      open
      onClose={onCancel}
      role="alertdialog"
      ariaLabel={title}
      panelClassName="max-h-[calc(100vh-6rem)] w-[34rem] max-w-[calc(100vw-3rem)] overflow-y-auto rounded-card border border-yolo/50 bg-panel p-5 shadow-modal"
    >
      <div className="flex items-start gap-3">
        <div className="grid h-9 w-9 shrink-0 place-items-center rounded-card border border-yolo/40 bg-yolo/15 text-yolo">
          <AlertTriangle size={ICON.md} />
        </div>
        <div className="min-w-0">
          <h2 className="text-title font-semibold leading-snug text-fg">
            {title}
          </h2>
          <p className="mt-1 text-sm leading-relaxed text-dim">{intro}</p>
        </div>
      </div>
      {children}
      <div className="mt-4 space-y-2">
        {YOLO_RISKS.map((risk) => {
          const Icon = risk.icon;
          return (
            <div
              key={risk.titleKey}
              className="flex items-start gap-2.5 rounded-card border border-edge bg-panel2 px-3 py-2.5"
            >
              <Icon size={ICON.sm} className="mt-0.5 shrink-0 text-yolo" />
              <div className="min-w-0">
                <div className="text-xs font-medium text-fg">
                  {t(risk.titleKey)}
                </div>
                <p className="mt-0.5 text-label leading-snug text-dim">
                  {t(risk.bodyKey)}
                </p>
              </div>
            </div>
          );
        })}
      </div>
      <p className="mt-4 rounded-control border border-yolo/25 bg-yolo/5 px-3 py-2 text-label leading-snug text-dim">
        {scope}
      </p>
      <div className="mt-4 flex justify-end gap-2">
        <Button variant="quiet" onClick={onCancel}>
          {cancelLabel ?? t('interact.cancel')}
        </Button>
        <Button variant="danger" onClick={onConfirm}>
          {confirmLabel}
        </Button>
      </div>
    </Overlay>
  );
}
