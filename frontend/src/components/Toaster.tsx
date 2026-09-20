import { useEffect, useState } from 'react';
import { AlertTriangle, Info, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useStore, type ToastItem } from '../lib/store';
import { MOTION } from '../lib/overlay';
import { ICON } from './ui/icon';

// Toaster — transient notices. It renders at the top of the layer ladder
// (above dialogs: a warning raised by a settings save must not be hidden
// behind the settings page that raised it) and animates both in and out.
//
// The store drops a toast the moment it expires, so the queue keeps the
// entry mounted for the length of its exit animation.
function useToastQueue(items: ToastItem[]) {
  const [queue, setQueue] = useState<{ toast: ToastItem; closing: boolean }[]>(
    () => items.map((toast) => ({ toast, closing: false })),
  );

  useEffect(() => {
    setQueue((prev) => {
      const live = new Map(items.map((item) => [item.id, item]));
      const next: typeof prev = [];
      for (const entry of prev) {
        const updated = live.get(entry.toast.id);
        if (updated !== undefined)
          next.push({ toast: updated, closing: false });
        else if (!entry.closing) next.push({ ...entry, closing: true });
        // Else: already leaving; the sweep below drops it.
      }
      for (const item of items) {
        if (!prev.some((entry) => entry.toast.id === item.id)) {
          next.push({ toast: item, closing: false });
        }
      }
      return next;
    });
  }, [items]);

  useEffect(() => {
    if (!queue.some((entry) => entry.closing)) return;
    const timer = window.setTimeout(
      () => setQueue((prev) => prev.filter((entry) => !entry.closing)),
      MOTION.fast,
    );
    return () => window.clearTimeout(timer);
  }, [queue]);

  return queue;
}

export function Toaster() {
  const toasts = useStore((s) => s.toasts);
  const dismissToast = useStore((s) => s.dismissToast);
  const { t } = useTranslation();
  const queue = useToastQueue(toasts);
  if (queue.length === 0) return null;

  return (
    <div
      aria-live="polite"
      className="pointer-events-none fixed right-4 top-4 z-[var(--oc-z-toast)] flex w-80 max-w-[calc(100vw-2rem)] flex-col gap-2"
    >
      {queue.map(({ toast, closing }) => {
        const warning = toast.kind === 'warning';
        return (
          <div
            key={toast.id}
            role={warning ? 'alert' : 'status'}
            className={`pointer-events-auto flex items-start gap-2.5 rounded-card border px-3 py-2.5 text-sm shadow-modal ${
              closing ? 'animate-toast-out' : 'animate-toast-in'
            } ${
              warning ? 'border-warn/40 bg-panel3' : 'border-edge bg-panel3'
            }`}
          >
            <span className={warning ? 'text-warn' : 'text-accent'}>
              {warning ? (
                <AlertTriangle size={ICON.sm} className="mt-0.5 shrink-0" />
              ) : (
                <Info size={ICON.sm} className="mt-0.5 shrink-0" />
              )}
            </span>
            <span className="min-w-0 flex-1 break-words leading-relaxed">
              {toast.text}
            </span>
            <button
              type="button"
              onClick={() => dismissToast(toast.id)}
              aria-label={t('chat.dismiss')}
              data-tip={t('chat.dismiss')}
              className="-mr-1 -mt-0.5 shrink-0 rounded-tight p-1 text-dim transition-colors hover:bg-panel2 hover:text-fg"
            >
              <X size={ICON.xs} />
            </button>
          </div>
        );
      })}
    </div>
  );
}
