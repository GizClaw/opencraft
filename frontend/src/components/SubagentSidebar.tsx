import { memo, useEffect, useMemo, useRef, useState } from 'react';
import {
  Activity,
  Bot,
  ChevronDown,
  ChevronRight,
  Clock,
  Minimize2,
  Wrench,
  X,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useStore } from '../lib/store';
import type { KanbanCard } from '../lib/types';
import type { AssistantItem, MessageView } from '../lib/store';

// StreamItem renders one assistant stream item (text, reasoning, tool
// call, or tool result) in the compact violet sidebar style.
function StreamItem({ item }: { item: AssistantItem }) {
  const { t } = useTranslation();
  switch (item.kind) {
    case 'text':
      if (!item.text) return null;
      return <p className="text-xs text-fg whitespace-pre-wrap">{item.text}</p>;
    case 'reasoning':
      if (!item.text) return null;
      return (
        <details className="text-xs">
          <summary className="cursor-pointer text-violet-300/90 select-none">
            {t('subagent.reasoning')}
          </summary>
          <div className="mt-1 whitespace-pre-wrap text-dim max-h-32 overflow-y-auto">
            {item.text}
          </div>
        </details>
      );
    case 'tool_call':
      return (
        <div>
          <div
            className="flex items-center gap-1.5 font-mono text-xs"
            title={item.tool.args}
          >
            <Wrench size={11} className="text-dim shrink-0" />
            <span className="text-fg truncate">{item.tool.name}</span>
            {item.tool.status === 'running' ? (
              <span className="text-accent animate-pulse shrink-0">…</span>
            ) : item.tool.status === 'error' ? (
              <span className="text-err shrink-0">⛔</span>
            ) : (
              <span className="text-ok shrink-0">✓</span>
            )}
          </div>
          {item.tool.result !== undefined && (
            <div
              className={`ml-4 border-l pl-2 text-[11px] whitespace-pre-wrap max-h-28 overflow-y-auto ${
                item.tool.status === 'error' ? 'text-err' : 'text-dim'
              }`}
            >
              {item.tool.result}
            </div>
          )}
        </div>
      );
    default:
      return null;
  }
}

// StreamView renders the live stream of one subagent run: every
// assistant item in arrival order.
const StreamView = memo(function StreamView({
  stream,
}: {
  stream: MessageView[];
}) {
  const { t } = useTranslation();
  // update_plan renders once in the main chat's plan panel, so it is
  // dropped from subagent streams too.
  const items = stream
    .flatMap((m) => m.items)
    .filter(
      (it) => !(it.kind === 'tool_call' && it.tool.name === 'update_plan'),
    );
  if (items.length === 0) return null;
  return (
    <div className="rounded-lg border border-edge bg-panel2 p-2 space-y-1.5">
      <div className="flex items-center gap-1 text-[10px] uppercase tracking-wide text-dim">
        <Activity size={10} />
        {t('subagent.stream')}
      </div>
      {items.map((item) => (
        <StreamItem key={item.id} item={item} />
      ))}
    </div>
  );
});

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

function statusMeta(
  status: string,
  t: (k: string) => string,
): { label: string; bar: string; pill: string; spin?: boolean } {
  switch (status) {
    case 'pending':
      return {
        label: t('kanban.pending'),
        bar: 'border-l-dim',
        pill: 'text-dim border-edge bg-panel',
      };
    case 'claimed':
    case 'suspended':
      return {
        label: t('kanban.running'),
        bar: 'border-l-accent',
        pill: 'text-accent border-accent/40 bg-accent/10',
        spin: true,
      };
    case 'done':
      return {
        label: t('kanban.done'),
        bar: 'border-l-ok',
        pill: 'text-ok border-ok/40 bg-ok/10',
      };
    case 'failed':
    case 'canceled':
      return {
        label: t('kanban.failed'),
        bar: 'border-l-err',
        pill: 'text-err border-err/40 bg-err/10',
      };
    default:
      return {
        label: status,
        bar: 'border-l-dim',
        pill: 'text-dim border-edge bg-panel',
      };
  }
}

// SubagentCard renders one delegation as a violet-accented card that is
// visually distinct from the neutral ToolCard: rounded-xl with a colored
// left status bar, Bot icon, status pill, elapsed time, and a
// label/value detail grid when expanded.
const SubagentCard = memo(function SubagentCard({
  card,
  stream,
  collapseKey,
}: {
  card: KanbanCard;
  stream?: MessageView[];
  collapseKey: number;
}) {
  const [open, setOpen] = useState(false);
  const { t } = useTranslation();
  const meta = statusMeta(card.status, t);
  const running = card.status === 'claimed' || card.status === 'suspended';
  const failed = card.status === 'failed' || card.status === 'canceled';
  const wasRunning = useRef(running);
  // Running cards stay expanded so progress is visible; they collapse
  // automatically once the run finishes.
  useEffect(() => {
    if (running) {
      setOpen(true);
    } else if (wasRunning.current) {
      setOpen(false);
    }
    wasRunning.current = running;
  }, [running]);
  // "Collapse all" from the panel header.
  useEffect(() => {
    if (collapseKey > 0) setOpen(false);
  }, [collapseKey]);

  return (
    <div className="overflow-hidden rounded-xl border border-edge bg-panel2">
      <div className={`border-l-2 ${meta.bar}`}>
        <button
          onClick={() => setOpen(!open)}
          className="flex w-full items-center gap-2 px-2.5 py-2 text-left text-sm transition-colors hover:bg-panel"
        >
          <span
            className={`h-2 w-2 shrink-0 rounded-full ${
              running
                ? 'animate-pulse bg-accent'
                : card.status === 'done'
                  ? 'bg-ok'
                  : card.status === 'failed' || card.status === 'canceled'
                    ? 'bg-err'
                    : 'bg-dim/50'
            }`}
          />
          <span className="min-w-0 flex-1 truncate font-medium">
            {card.target}
          </span>
          {elapsed(card) && (
            <span className="flex shrink-0 items-center gap-1 text-[10px] text-dim tabular-nums">
              <Clock size={10} />
              {elapsed(card)}
            </span>
          )}
          {failed && (
            <span
              className={`shrink-0 rounded border px-1.5 py-0.5 text-[10px] ${meta.pill}`}
            >
              {meta.label}
            </span>
          )}
          {open ? (
            <ChevronDown size={14} className="shrink-0 text-dim" />
          ) : (
            <ChevronRight size={14} className="shrink-0 text-dim" />
          )}
        </button>
        {open && (
          <div className="space-y-2 px-3 pb-3">
            <StreamView stream={stream ?? []} />
            {card.input && (
              <Block label={t('subagent.detailInput')} value={card.input} />
            )}
            {card.output && (
              <Block label={t('subagent.detailOutput')} value={card.output} />
            )}
            {card.error && (
              <Block label={t('subagent.error')} value={card.error} error />
            )}
          </div>
        )}
      </div>
    </div>
  );
});

function Block({
  label,
  value,
  error,
}: {
  label: string;
  value?: string;
  error?: boolean;
}) {
  if (!value) return null;
  return (
    <div className="space-y-1">
      <div
        className={`text-[10px] uppercase tracking-wide ${error ? 'text-err' : 'text-dim'}`}
      >
        {label}
      </div>
      <pre
        className={`max-h-40 overflow-y-auto whitespace-pre-wrap break-all rounded-lg border border-edge bg-panel/80 p-2 text-xs ${
          error ? 'text-err' : 'text-fg'
        }`}
      >
        {value}
      </pre>
    </div>
  );
}

// TreeNode is one delegation node plus its delegated children. The
// kanban board stores cards flat; parent_run_id links each subagent
// run to the caller run that delegated it, which restores the graph
// of nested delegations for visualization.
type TreeNode = {
  card: KanbanCard;
  children: TreeNode[];
};

// buildForest turns the flat, newest-first card list into a forest of
// delegation trees. A card whose parent run is not itself on the board
// (the main conversation run) becomes a root; children are ordered by
// creation time so the delegation order reads top to bottom.
function buildForest(cards: KanbanCard[]): TreeNode[] {
  const byRun = new Map<string, KanbanCard>();
  for (const c of cards) {
    if (c.run_id) byRun.set(c.run_id, c);
  }
  const childLists = new Map<string, KanbanCard[]>();
  const roots: KanbanCard[] = [];
  for (const c of cards) {
    const parent = c.parent_run_id ? byRun.get(c.parent_run_id) : undefined;
    if (parent) {
      const list = childLists.get(parent.run_id!) ?? [];
      list.push(c);
      childLists.set(parent.run_id!, list);
    } else {
      roots.push(c);
    }
  }
  // visited guards against a malformed cycle in the parent links.
  const visited = new Set<string>();
  const build = (card: KanbanCard): TreeNode => {
    const runID = card.run_id ?? '';
    const children = (childLists.get(runID) ?? [])
      .filter((c) => {
        const id = c.run_id ?? '';
        if (!id || visited.has(id)) return false;
        visited.add(id);
        return true;
      })
      .sort((a, b) => a.created_at.localeCompare(b.created_at))
      .map(build);
    return { card, children };
  };
  return roots.map(build);
}

// SubagentNode renders one delegation node with its children nested
// beneath it, connected by an L-shaped branch line.
function SubagentNode({
  node,
  streams,
  depth,
  collapseKey,
}: {
  node: TreeNode;
  streams: Record<string, MessageView[]>;
  depth: number;
  collapseKey: number;
}) {
  return (
    <div
      className={depth > 0 ? 'ml-3 space-y-2 border-l border-edge/60 pl-3' : ''}
    >
      <SubagentCard
        card={node.card}
        stream={node.card.run_id ? streams[node.card.run_id] : undefined}
        collapseKey={collapseKey}
      />
      {node.children.length > 0 && (
        <div className="mt-2 space-y-2">
          {node.children.map((child) => (
            <SubagentNode
              key={child.card.id}
              node={child}
              streams={streams}
              depth={depth + 1}
              collapseKey={collapseKey}
            />
          ))}
        </div>
      )}
    </div>
  );
}

// SubagentSidebar is the right-hand panel inside the chat area. It
// appears while the current conversation has delegation cards and
// lists the subagent runs as a delegation tree: a delegated subagent
// that spawns further subagents nests beneath its parent card.
export function SubagentSidebar() {
  const cards = useStore((s) => s.subagentCards);
  const streams = useStore((s) => s.subagentStreams);
  const togglePanel = useStore((s) => s.toggleSubagentPanel);
  const { t } = useTranslation();
  const forest = useMemo(() => buildForest(cards), [cards]);
  const [collapseKey, setCollapseKey] = useState(0);
  const hasRunning = cards.some(
    (c) => c.status === 'claimed' || c.status === 'suspended',
  );

  return (
    <aside className="flex h-full min-h-0 w-72 shrink-0 flex-col border-l border-edge bg-panel">
      <header className="flex h-11 shrink-0 select-none items-center gap-2 border-b border-edge px-3">
        <Bot size={14} className="text-subagent" />
        <span className="text-sm font-semibold">{t('subagent.title')}</span>
        {hasRunning && (
          <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-accent" />
        )}
        <span className="rounded bg-subagent/10 border border-subagent/30 px-1.5 text-xs text-subagent">
          {cards.length}
        </span>
        <span className="flex-1" />
        <button
          onClick={() => setCollapseKey((n) => n + 1)}
          className="text-dim hover:text-fg"
          title={t('subagent.collapseAll')}
          aria-label={t('subagent.collapseAll')}
        >
          <Minimize2 size={14} />
        </button>
        <button
          onClick={togglePanel}
          className="text-dim hover:text-fg"
          title={t('subagent.close')}
        >
          <X size={16} />
        </button>
      </header>
      <div className="flex-1 space-y-2 overflow-y-auto p-2.5">
        {forest.map((node) => (
          <SubagentNode
            key={node.card.id}
            node={node}
            streams={streams}
            depth={0}
            collapseKey={collapseKey}
          />
        ))}
      </div>
    </aside>
  );
}
