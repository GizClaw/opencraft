import { memo, useMemo, useState } from 'react';
import {
  Activity,
  ArrowRight,
  Bot,
  ChevronDown,
  ChevronRight,
  Clock,
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
            <Wrench size={11} className="text-violet-300 shrink-0" />
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
  const items = stream.flatMap((m) => m.items);
  if (items.length === 0) return null;
  return (
    <div className="rounded-lg border border-violet-400/20 bg-violet-400/5 p-2 space-y-1.5">
      <div className="flex items-center gap-1 text-[10px] uppercase tracking-wide text-violet-400/80">
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
}: {
  card: KanbanCard;
  stream?: MessageView[];
}) {
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
            <span className="block truncate font-medium">{card.target}</span>
            <span className="flex items-center gap-1 truncate text-xs text-dim mt-0.5">
              {card.producer && (
                <span className="truncate">{card.producer}</span>
              )}
              {card.producer && card.consumer && (
                <ArrowRight size={10} className="shrink-0" />
              )}
              {card.consumer && (
                <span className="truncate">{card.consumer}</span>
              )}
              {card.depth > 0 && (
                <span className="shrink-0">· d{card.depth}</span>
              )}
            </span>
          </span>
          {elapsed(card) && (
            <span className="flex items-center gap-1 text-[10px] text-dim shrink-0 tabular-nums">
              <Clock size={10} />
              {elapsed(card)}
            </span>
          )}
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
            <StreamView stream={stream ?? []} />
            <dl className="divide-y divide-edge/60 rounded-lg border border-edge bg-panel/80 overflow-hidden">
              <DetailRow
                label={t('subagent.detailTarget')}
                value={card.target}
              />
              <DetailRow
                label={t('subagent.detailCaller')}
                value={card.caller}
              />
              <DetailRow label={t('subagent.detailRun')} value={card.run_id} />
              <DetailRow
                label={t('subagent.detailParent')}
                value={card.parent_run_id}
              />
              <DetailRow
                label={t('subagent.detailCall')}
                value={card.call_id}
              />
              <DetailRow
                label={t('subagent.detailCreated')}
                value={card.created_at}
              />
              <DetailRow
                label={t('subagent.detailUpdated')}
                value={card.updated_at}
              />
              {card.input && (
                <DetailRow
                  label={t('subagent.detailInput')}
                  value={card.input}
                  block
                />
              )}
              {card.output && (
                <DetailRow
                  label={t('subagent.detailOutput')}
                  value={card.output}
                  block
                />
              )}
              {card.error && (
                <DetailRow
                  label={t('subagent.error')}
                  value={card.error}
                  block
                  error
                />
              )}
            </dl>
          </div>
        )}
      </div>
    </div>
  );
});

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
    <div className="grid grid-cols-[92px_1fr] gap-2 px-2.5 py-1.5 text-xs">
      <dt className={`truncate ${error ? 'text-err' : 'text-dim'}`}>{label}</dt>
      <dd
        className={`min-w-0 break-all ${error ? 'text-err' : 'text-fg'} ${
          block
            ? 'max-h-40 overflow-y-auto whitespace-pre-wrap'
            : 'truncate whitespace-pre-wrap'
        }`}
      >
        {value}
      </dd>
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
}: {
  node: TreeNode;
  streams: Record<string, MessageView[]>;
  depth: number;
}) {
  return (
    <div className={depth > 0 ? 'relative ml-4 pl-3' : ''}>
      {depth > 0 && (
        <>
          <span className="absolute left-0 top-0 h-full w-px bg-violet-400/15" />
          <span className="absolute left-0 top-3 h-3 w-3 rounded-bl-lg border-b border-l border-violet-400/15" />
        </>
      )}
      <SubagentCard
        card={node.card}
        stream={node.card.run_id ? streams[node.card.run_id] : undefined}
      />
      {node.children.length > 0 && (
        <div className="mt-2 space-y-2">
          {node.children.map((child) => (
            <SubagentNode
              key={child.card.id}
              node={child}
              streams={streams}
              depth={depth + 1}
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

  return (
    <aside className="w-72 shrink-0 h-full border-l border-edge bg-panel flex flex-col min-h-0">
      <header className="h-11 shrink-0 border-b border-edge flex items-center gap-2 px-3 select-none">
        <Bot size={14} className="text-violet-400" />
        <span className="text-sm font-semibold">{t('subagent.title')}</span>
        <span className="rounded bg-violet-400/10 border border-violet-400/30 px-1.5 text-xs text-violet-400">
          {cards.length}
        </span>
        <span className="flex-1" />
        <button
          onClick={togglePanel}
          className="text-dim hover:text-fg"
          title={t('subagent.close')}
        >
          <X size={16} />
        </button>
      </header>
      <div className="flex-1 overflow-y-auto p-2.5 space-y-2">
        {forest.map((node) => (
          <SubagentNode
            key={node.card.id}
            node={node}
            streams={streams}
            depth={0}
          />
        ))}
      </div>
    </aside>
  );
}
