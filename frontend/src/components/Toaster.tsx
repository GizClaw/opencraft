import { AlertTriangle, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useStore } from '../lib/store';

export function Toaster() {
  const toasts = useStore((s) => s.toasts);
  const dismissToast = useStore((s) => s.dismissToast);
  const { t } = useTranslation();
  if (toasts.length === 0) return null;
  return (
    <div className="fixed top-3 right-3 z-50 flex w-80 max-w-[calc(100vw-2rem)] flex-col gap-2">
      {toasts.map((item) => (
        <div
          key={item.id}
          className={`flex items-start gap-2 rounded-lg border px-3 py-2 text-sm shadow-xl ${
            item.kind === 'warning'
              ? 'border-warn/40 bg-warn/10'
              : 'border-edge bg-panel'
          }`}
        >
          {item.kind === 'warning' && (
            <AlertTriangle
              size="0.9286rem"
              className="mt-0.5 shrink-0 text-warn"
            />
          )}
          <span className="min-w-0 flex-1 break-words">{item.text}</span>
          <button
            onClick={() => dismissToast(item.id)}
            aria-label={t('chat.dismiss')}
            className="shrink-0 text-dim hover:text-fg"
          >
            <X size="0.9286rem" />
          </button>
        </div>
      ))}
    </div>
  );
}
