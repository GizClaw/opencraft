import { useState } from "react";
import {
  ArrowRight,
  Bot,
  ChevronDown,
  ChevronRight,
  X,
} from "lucide-react";
import { useTranslation } from "react-i18next";
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

function statusMeta(
  status: string,
  t: (k: string) => string,
): { label: string; bar: string; pill: string; spin?: boolean } {
  switch (status) {
    case "pending":
      return {
        label: t("kanban.pending"),
        bar: "border-l-dim",
        pill: "text-dim border-edge bg-panel",
      };
    case "claimed":
    case "suspended":
      return {
        label: t("kanban.running"),
        bar: "border-l-accent",
        pill: "text-accent border-accent/40 bg-accent/10",
        spin: true,
      };
    case "done":
      return {
        label: t("kanban.done"),
        bar: "border-l-ok",
        pill: "text-ok border-ok/40 bg-ok/10",
      };
    case "failed":
    case "canceled":
      return {
        label: t("kanban.failed"),
        bar: "border-l-err",
        pill: "text-err border-err/40 bg-err/10",
      };
    default:
      return {
        label: status,
        bar: "border-l-dim",
        pill: "text-dim border-edge bg-panel",
      };
  }
}

// SubagentCard renders one delegation as a violet-accented card that is
// visually distinct from the neutral ToolCard: rounded-xl with a colored
// left status bar, Bot icon and status pill, and a monospace detail
// grid when expanded.
function SubagentCard({ card }: { card: KanbanCard }) {
  const [open, setOpen] = useState(false);
  const { t } = useTranslation();
  const meta = statusMeta(card.status, t);

  return (
    <div className="rounded-xl border border-edge overflow-hidden bg-violet-400/5">
      <div className={`border-l-2 ${meta.bar}`}>
        <button
          onClick={() => setOpen(!open)}
          className="w-full flex items-center gap-2 px-2.5 py-2 text-left text-sm hover:bg-violet-400/10 transition-colors"
        >
          <Bot size={14} className="text-violet-400 shrink-0" />
          <span className="flex-1 min-w-0">
            <span className="block truncate font-medium">
              {card.consumer || card.target}
            </span>
            <span className="block truncate text-xs text-dim mt-0.5">
              {card.target}
            </span>
          </span>
          <span
            className={`flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] border shrink-0 ${meta.pill}`}
          >
            {meta.spin ? (
              <span className="h-1.5 w-1.5 rounded-full bg-accent animate-pulse" />
            ) : (
              <span className="h-1.5 w-1.5 rounded-full bg-current opacity-70" />
            )}
            {meta.label}
          </span>
          {open ? (
            <ChevronDown size={14} className="text-dim shrink-0" />
          ) : (
            <ChevronRight size={14} className="text-dim shrink-0" />
          )}
        </button>
        {open && (
          <div className="px-3 pb-3 space-y-2">
            <div className="flex items-center gap-1.5 text-xs text-dim">
              <span>{card.producer || "?"}</span>
              <ArrowRight size={10} />
              <span>{card.consumer}</span>
              {card.depth > 0 && <span className="text-dim">· d{card.depth}</span>}
            </div>
            <div className="rounded-lg border border-edge bg-panel/80 p-2 font-mono text-[11px] space-y-1">
              <DetailRow label={t("subagent.detailTarget")} value={card.target} />
              <DetailRow label={t("subagent.detailCaller")} value={card.caller} />
              <DetailRow label={t("subagent.detailRun")} value={card.run_id} />
              <DetailRow
                label={t("subagent.detailParent")}
                value={card.parent_run_id}
              />
              <DetailRow label={t("subagent.detailCall")} value={card.call_id} />
              <DetailRow
                label={t("subagent.detailCreated")}
                value={card.created_at}
              />
              <DetailRow
                label={t("subagent.detailUpdated")}
                value={card.updated_at}
              />
              {card.input && (
                <DetailRow
                  label={t("subagent.detailInput")}
                  value={card.input}
                  block
                />
              )}
              {card.output && (
                <DetailRow
                  label={t("subagent.detailOutput")}
                  value={card.output}
                  block
                />
              )}
              {card.error && (
                <DetailRow
                  label={t("subagent.error")}
                  value={card.error}
                  block
                  error
                />
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

function DetailRow({
  label,
  value,
  block,
  error,
}: {
  label: string;
  value?: string;
  block?: boolean;
  error?: boolean;
}) {
  if (!value) return null;
  return (
    <div>
      <div className={`text-dim ${error ? "text-err" : ""}`}>{label}</div>
      <div
        className={`text-fg break-all whitespace-pre-wrap ${
          block ? "max-h-40 overflow-y-auto" : "truncate"
        } ${error ? "text-err" : ""}`}
      >
        {value}
      </div>
    </div>
  );
}

// SubagentSidebar is the right-hand panel inside the chat area. It
// appears while the current conversation has delegation cards and
// lists the subagent runs with expandable details.
export function SubagentSidebar() {
  const cards = useStore((s) => s.subagentCards);
  const togglePanel = useStore((s) => s.toggleSubagentPanel);
  const { t } = useTranslation();

  return (
    <aside className="w-72 shrink-0 h-full border-l border-edge bg-panel flex flex-col min-h-0">
      <header className="h-11 shrink-0 border-b border-edge flex items-center gap-2 px-3 select-none">
        <Bot size={14} className="text-violet-400" />
        <span className="text-sm font-semibold">{t("subagent.title")}</span>
        <span className="rounded bg-violet-400/10 border border-violet-400/30 px-1.5 text-xs text-violet-400">
          {cards.length}
        </span>
        <span className="flex-1" />
        <button
          onClick={togglePanel}
          className="text-dim hover:text-fg"
          title={t("subagent.close")}
        >
          <X size={16} />
        </button>
      </header>
      <div className="flex-1 overflow-y-auto p-2.5 space-y-2">
        {cards.map((card) => (
          <SubagentCard key={card.id} card={card} />
        ))}
      </div>
    </aside>
  );
}
