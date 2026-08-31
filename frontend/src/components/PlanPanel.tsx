import { useEffect, useState } from 'react';
import {
  Check,
  ChevronDown,
  ChevronRight,
  ClipboardList,
  Loader2,
  X,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { PlanSnapshot } from '../lib/plan';

export type { PlanPanelState, PlanSnapshot, PlanStepView } from '../lib/plan';

// PlanPanel renders the conversation's current plan as a collapsible
// card pinned to the top-left of the chat area. Fully completed plans
// start collapsed; active ones start expanded so the checklist stays
// visible while work is running. When the plan content updates, the
// open state resets accordingly (new progress re-expands, completion
// collapses), so a manually collapsed panel never hides fresh updates.
export function PlanPanel({
  plan,
  live,
  onClose,
}: {
  plan: PlanSnapshot;
  live: boolean;
  onClose?: () => void;
}) {
  const { t } = useTranslation();
  const planKey = plan.items.map((s) => `${s.status}|${s.step}`).join('\n');
  const autoOpen =
    plan.items.length === 0 ||
    !plan.items.every((s) => s.status === 'completed');
  const [open, setOpen] = useState(autoOpen);
  useEffect(() => {
    setOpen(autoOpen);
    // planKey is the stable identity of the plan content; autoOpen is
    // derived from it, so only re-evaluating when the key changes is
    // intentional.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [planKey]);
  const done = plan.items.filter((s) => s.status === 'completed').length;
  return (
    <div className="absolute left-4 top-3 z-30 w-80 max-w-[calc(100%-2rem)] overflow-hidden rounded-lg border border-edge bg-panel2 shadow-xl">
      <div className="flex items-center">
        <button
          onClick={() => setOpen((v) => !v)}
          className="flex min-w-0 flex-1 items-center gap-2 px-3 py-1.5 text-xs hover:bg-panel2/70"
        >
          {live ? (
            <Loader2
              size="0.9286rem"
              className="shrink-0 animate-spin text-accent"
            />
          ) : (
            <ClipboardList size="0.9286rem" className="shrink-0 text-ok" />
          )}
          <span className="truncate font-medium text-fg">
            {t('chat.planTitle')}
          </span>
          {plan.items.length > 0 && (
            <span className="text-dim tabular-nums">
              {done}/{plan.items.length}
            </span>
          )}
          <span className="flex-1" />
          {open ? (
            <ChevronDown size="1.0000rem" className="shrink-0 text-dim" />
          ) : (
            <ChevronRight size="1.0000rem" className="shrink-0 text-dim" />
          )}
        </button>
        {onClose && (
          <button
            onClick={onClose}
            title={t('tools.close')}
            aria-label={t('tools.close')}
            className="mr-1.5 shrink-0 rounded p-1 text-dim transition-colors hover:text-fg"
          >
            <X size="0.8571rem" />
          </button>
        )}
      </div>
      {open && (
        <div className="space-y-1.5 border-t border-edge px-3 py-2">
          {plan.items.length === 0 ? (
            <div className="flex items-center gap-2 text-xs text-dim">
              <Loader2 size="0.8571rem" className="animate-spin text-accent" />
              {t('chat.planLoading')}
            </div>
          ) : (
            <>
              {plan.explanation && (
                <div className="whitespace-pre-wrap text-xs text-dim">
                  {plan.explanation}
                </div>
              )}
              {plan.items.map((item, idx) => {
                const status = item.status ?? 'pending';
                const completed = status === 'completed';
                const inProgress = status === 'in_progress';
                return (
                  <div key={idx} className="flex items-start gap-2 text-xs">
                    {inProgress ? (
                      <Loader2
                        size="0.8571rem"
                        className="mt-0.5 shrink-0 animate-spin text-accent"
                      />
                    ) : completed ? (
                      <Check
                        size="0.8571rem"
                        className="mt-0.5 shrink-0 text-ok"
                      />
                    ) : (
                      <span className="mt-0.5 h-3 w-3 shrink-0 rounded-full border border-dim" />
                    )}
                    <span
                      className={`min-w-0 ${
                        completed ? 'text-dim line-through' : 'text-fg'
                      }`}
                    >
                      {item.step}
                    </span>
                  </div>
                );
              })}
            </>
          )}
        </div>
      )}
    </div>
  );
}
