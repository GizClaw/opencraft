import { useEffect } from 'react';
import {
  ArrowRight,
  Check,
  Clock,
  Kanban,
  Loader2,
  RotateCcw,
  X,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { api } from '../lib/api';
import { useStore } from '../lib/store';
import type { KanbanCard } from '../lib/types';

function elapsed(card: KanbanCard): string {
  const start = new Date(card.created_at).getTime();
  const end = new Date(card.updated_at).getTime();
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) {
    return '';
  }
  const s = Math.floor((end - start) / 1000);
  const m = Math.floor(s / 60);
  return m > 0 ? `${m}m${s % 60}s` : `${s}s`;
}

function CardView({
  card,
  onChanged,
}: {
  card: KanbanCard;
  onChanged: () => void;
}) {
  const { t } = useTranslation();
  const cancellable =
    card.status === 'pending' ||
    card.status === 'claimed' ||
    card.status === 'suspended';
  const retryable = card.status === 'failed' || card.status === 'canceled';
  const running = card.status === 'claimed' || card.status === 'suspended';
  const done = card.status === 'done';
  const failed = card.status === 'failed' || card.status === 'canceled';
  const border = running
    ? 'border-l-accent'
    : done
      ? 'border-l-ok'
      : failed
        ? 'border-l-err'
        : 'border-l-edge';
  return (
    <div
      className={`rounded-lg border border-edge border-l-2 ${border} bg-panel2 p-2.5 space-y-1`}
    >
      <div className="flex items-start justify-between gap-2">
        <div className="text-sm font-medium break-words leading-snug">
          {card.target}
        </div>
        {running && (
          <Loader2
            size={13}
            className="mt-0.5 shrink-0 animate-spin text-accent"
          />
        )}
      </div>
      {(card.producer || card.consumer) && (
        <div className="flex items-center gap-1 text-xs text-dim">
          {card.producer && <span>{card.producer}</span>}
          <ArrowRight size={10} />
          {card.consumer && <span>{card.consumer}</span>}
        </div>
      )}
      {card.input && (
        <p className="text-xs text-dim line-clamp-3 whitespace-pre-wrap break-words">
          {card.input}
        </p>
      )}
      {card.output && (
        <p
          className={`text-xs line-clamp-2 whitespace-pre-wrap break-words ${
            done ? 'text-fg/80' : 'text-dim'
          }`}
        >
          {card.output}
        </p>
      )}
      {card.error && (
        <p className="text-xs text-err line-clamp-3 whitespace-pre-wrap break-words">
          {t('kanban.error')}: {card.error}
        </p>
      )}
      {elapsed(card) && (
        <div className="flex items-center gap-1 text-xs text-dim">
          <Clock size={11} />
          {elapsed(card)}
        </div>
      )}
      {(cancellable || retryable) && (
        <div className="flex gap-2 pt-1">
          {cancellable && (
            <button
              onClick={() => void api.cancelCard(card.id).then(onChanged)}
              className="flex items-center gap-1 rounded border border-edge px-2 py-0.5 text-xs text-dim hover:text-err"
              aria-label={t('kanban.cancel')}
            >
              <X size={11} />
              {t('kanban.cancel')}
            </button>
          )}
          {retryable && (
            <button
              onClick={() => void api.retryCard(card.id).then(onChanged)}
              className="flex items-center gap-1 rounded border border-edge px-2 py-0.5 text-xs text-dim hover:text-accent"
              aria-label={t('kanban.retry')}
            >
              <RotateCcw size={11} />
              {t('kanban.retry')}
            </button>
          )}
        </div>
      )}
    </div>
  );
}

// KanbanSection is the delegation board rendered as a tab inside the
// tools page (alongside MCP / subagents / skills). It polls the card
// list while mounted so the board stays fresh.
export function KanbanSection() {
  const cards = useStore((s) => s.cards);
  const loadCards = useStore((s) => s.loadCards);
  const { t } = useTranslation();

  useEffect(() => {
    void loadCards();
    const timer = setInterval(() => void loadCards(), 2000);
    return () => clearInterval(timer);
  }, [loadCards]);

  const columns: {
    key: string;
    label: string;
    icon: 'pending' | 'running' | 'done' | 'failed';
    filter: (c: KanbanCard) => boolean;
  }[] = [
    {
      key: 'pending',
      label: t('kanban.pending'),
      icon: 'pending',
      filter: (c) => c.status === 'pending',
    },
    {
      key: 'running',
      label: t('kanban.running'),
      icon: 'running',
      filter: (c) => c.status === 'claimed' || c.status === 'suspended',
    },
    {
      key: 'done',
      label: t('kanban.done'),
      icon: 'done',
      filter: (c) => c.status === 'done',
    },
    {
      key: 'failed',
      label: t('kanban.failed'),
      icon: 'failed',
      filter: (c) => c.status === 'failed' || c.status === 'canceled',
    },
  ];

  return (
    <div className="space-y-3">
      <p className="text-xs text-dim">{t('kanban.title')}</p>
      {cards.length === 0 ? (
        <div className="rounded-xl border border-dashed border-edge px-6 py-10 text-center">
          <Kanban size={28} className="mx-auto mb-2 text-dim/60" />
          <p className="text-sm text-dim">{t('kanban.empty')}</p>
        </div>
      ) : (
        <div className="grid grid-cols-2 gap-3 xl:grid-cols-4">
          {columns.map((col) => {
            const items = cards.filter(col.filter);
            const Icon =
              col.icon === 'running'
                ? Loader2
                : col.icon === 'done'
                  ? Check
                  : col.icon === 'failed'
                    ? X
                    : Clock;
            const iconColor =
              col.icon === 'running'
                ? 'text-accent animate-spin'
                : col.icon === 'done'
                  ? 'text-ok'
                  : col.icon === 'failed'
                    ? 'text-err'
                    : 'text-dim';
            return (
              <div key={col.key} className="min-w-0 space-y-2">
                <div className="flex items-center gap-2 text-xs uppercase tracking-wider text-dim">
                  <Icon size={13} className={iconColor} />
                  {col.label}
                  <span className="rounded bg-panel2 border border-edge px-1.5 tabular-nums">
                    {items.length}
                  </span>
                </div>
                {items.length === 0 ? (
                  <p className="text-xs text-dim">{t('kanban.empty')}</p>
                ) : (
                  items.map((c) => (
                    <CardView
                      key={c.id}
                      card={c}
                      onChanged={() => void loadCards()}
                    />
                  ))
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
