import { AlertTriangle } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { YOLO_RISKS } from '../lib/sessionModes';
import { ICON } from './ui/icon';

// YoloConfirmDialog is the shared "switch to YOLO" warning shared by
// the composer and the General settings tab. Layer must be a literal
// Tailwind z-index token chosen by the caller to match its overlay
// context.
export function YoloConfirmDialog({
  layer = 'z-40',
  title,
  intro,
  scope,
  confirmLabel,
  cancelLabel,
  onCancel,
  onConfirm,
}: {
  layer?: string;
  title: string;
  intro: string;
  scope: string;
  confirmLabel: string;
  cancelLabel?: string;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div
      className={`fixed bottom-0 top-11 left-0 right-0 grid place-items-center bg-black/60 p-6 ${layer}`}
    >
      <div
        role="alertdialog"
        aria-modal="true"
        className="w-[34rem] max-w-[calc(100vw-3rem)] max-h-[calc(100vh-6rem)] overflow-y-auto rounded-card border border-yolo/50 bg-panel p-5 shadow-modal"
      >
        <div className="flex items-start gap-3">
          <div className="grid h-9 w-9 shrink-0 place-items-center rounded-card border border-yolo/40 bg-yolo/15 text-yolo">
            <AlertTriangle size={ICON.md} />
          </div>
          <div className="min-w-0">
            <h2 className="text-sm font-semibold leading-snug text-fg">
              {title}
            </h2>
            <p className="mt-1 text-xs leading-relaxed text-dim">{intro}</p>
          </div>
        </div>
        <div className="mt-4 space-y-2">
          {YOLO_RISKS.map((risk) => {
            const Icon = risk.icon;
            return (
              <div
                key={risk.titleKey}
                className="flex items-start gap-2.5 rounded-card border border-edge/70 bg-panel2/60 px-3 py-2.5"
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
          <button
            onClick={onCancel}
            className="rounded-control border border-edge px-4 py-1.5 text-sm text-dim hover:text-fg"
          >
            {cancelLabel ?? t('interact.cancel')}
          </button>
          <button
            onClick={onConfirm}
            className="rounded-control bg-yolo px-4 py-1.5 text-sm font-medium text-white hover:opacity-90"
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
