import { useEffect } from "react";
import { ArrowRight } from "lucide-react";
import { useTranslation } from "react-i18next";
import { api } from "../lib/api";
import { useStore } from "../lib/store";
import type { KanbanCard } from "../lib/types";

function elapsed(card: KanbanCard): string {
  const start = new Date(card.created_at).getTime();
  const end = new Date(card.updated_at).getTime();
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) {
    return "";
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
    card.status === "pending" ||
    card.status === "claimed" ||
    card.status === "suspended";
  const retryable = card.status === "failed" || card.status === "canceled";
  return (
    <div className="rounded-lg border border-edge bg-panel2 p-2.5 space-y-1">
      <div className="text-sm font-medium break-words leading-snug">
        {card.target}
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
        <p className="text-xs text-dim line-clamp-2 whitespace-pre-wrap break-words">
          {card.output}
        </p>
      )}
      {card.error && (
        <p className="text-xs text-err line-clamp-3 whitespace-pre-wrap break-words">
          {t("kanban.error")}: {card.error}
        </p>
      )}
      {elapsed(card) && (
        <div className="text-xs text-dim">{elapsed(card)}</div>
      )}
      {(cancellable || retryable) && (
        <div className="flex gap-2 pt-1">
          {cancellable && (
            <button
              onClick={() =>
                void api.cancelCard(card.id).then(onChanged)
              }
              className="rounded border border-edge px-2 py-0.5 text-xs text-dim hover:text-err"
            >
              {t("kanban.cancel")}
            </button>
          )}
          {retryable && (
            <button
              onClick={() =>
                void api.retryCard(card.id).then(onChanged)
              }
              className="rounded border border-edge px-2 py-0.5 text-xs text-dim hover:text-accent"
            >
              {t("kanban.retry")}
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
    filter: (c: KanbanCard) => boolean;
  }[] = [
    {
      key: "pending",
      label: t("kanban.pending"),
      filter: (c) => c.status === "pending",
    },
    {
      key: "running",
      label: t("kanban.running"),
      filter: (c) => c.status === "claimed" || c.status === "suspended",
    },
    {
      key: "done",
      label: t("kanban.done"),
      filter: (c) => c.status === "done",
    },
    {
      key: "failed",
      label: t("kanban.failed"),
      filter: (c) => c.status === "failed" || c.status === "canceled",
    },
  ];

  return (
    <div className="space-y-3">
      <p className="text-xs text-dim">{t("kanban.title")}</p>
      <div className="grid grid-cols-4 gap-3">
        {columns.map((col) => {
          const items = cards.filter(col.filter);
          return (
            <div key={col.key} className="space-y-2 min-w-0">
              <div className="flex items-center gap-2 text-xs uppercase tracking-wider text-dim">
                {col.label}
                <span className="rounded bg-panel2 border border-edge px-1.5">
                  {items.length}
                </span>
              </div>
              {items.length === 0 ? (
                <p className="text-xs text-dim">{t("kanban.empty")}</p>
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
    </div>
  );
}
