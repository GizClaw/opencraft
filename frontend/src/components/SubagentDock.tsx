import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ChevronDown, ChevronRight } from 'lucide-react';
import { Events } from '@wailsio/runtime';
import { api } from '../lib/api';
import { useStore } from '../lib/store';
import type { AgentSummary, UIEvent } from '../lib/types';
import type { PetActivityDTO } from '../pet/state';
import { ICON } from './ui/icon';

interface DockRow {
  /** Stable key: the run when there is one, else agent+conversation. */
  key: string;
  name: string;
  phase: PetActivityDTO['phase'];
  toolName?: string;
  conversationId?: string;
  runId?: string;
}

/** The stream payload fields the dock needs: which run this is, and
 *  which run delegated it. */
interface StreamPayload {
  run_id?: string;
  parent_run_id?: string;
}

// Phase dots use the semantic tokens so they flip with the theme
// (the raw Tailwind palette they replaced stayed dark-theme colored in
// the light theme).
function phaseDot(phase: PetActivityDTO['phase']): string {
  switch (phase) {
    case 'thinking':
      return 'bg-warn animate-pulse';
    case 'tool':
      return 'bg-accent animate-pulse';
    case 'answering':
      return 'bg-ok';
    case 'asking':
      return 'bg-subagent animate-bounce';
    case 'error':
      return 'bg-err';
    case 'done':
      // Finished and answering are both "good"; the finished dot is the
      // quieter one so a dock of idle rows does not read as activity.
      return 'bg-ok/50';
    default:
      return 'bg-dim';
  }
}

/** MAX_DEPTH bounds the nesting a delegation chain is drawn to. A chain
 *  deeper than this is still clickable, it is just drawn at the last
 *  indent instead of marching off the dock. */
const MAX_DEPTH = 3;

/**
 * SubagentDock lists subagents that currently have live activity. It
 * appears inside the main window (subagents never get desktop
 * windows); clicking a row opens the Subagents panel.
 *
 * The rows nest: every streamed delta carries the run that produced it
 * and the run that delegated it, so a subagent another subagent spawned
 * hangs under its parent instead of appearing as one more flat pill.
 * The parent link only lives as long as the deltas do — a run that
 * finished before the dock ever saw a delta is drawn as a root, which
 * is also what the assistant's own runs are.
 */
export function SubagentDock() {
  const { t } = useTranslation();
  const openTools = useStore((s) => s.openTools);
  const resume = useStore((s) => s.resume);
  const [rows, setRows] = useState<DockRow[]>([]);
  const [parents, setParents] = useState<Record<string, string>>({});
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});

  useEffect(() => {
    let alive = true;
    const refresh = async () => {
      try {
        const [agents, activities] = await Promise.all([
          api.listAgents(),
          api.petActivities(),
        ]);
        if (!alive) return;
        const byName = new Map(
          (agents ?? []).map((agent: AgentSummary) => [agent.name, agent]),
        );
        const next: DockRow[] = [];
        for (const activity of activities ?? []) {
          if (activity.agent_id === 'assistant') continue;
          const agent = byName.get(activity.agent_id);
          if (!agent) continue;
          next.push({
            key: activity.run_id ?? `${activity.agent_id}:${activity.ts}`,
            name: agent.name,
            phase: activity.phase,
            toolName: activity.tool?.name,
            conversationId: activity.conversation_id,
            runId: activity.run_id,
          });
        }
        setRows(next);
      } catch {
        // Polling is best-effort; a transient backend miss just keeps
        // the previous rows until the next tick.
      }
    };
    void refresh();
    const timer = window.setInterval(() => void refresh(), 2000);
    return () => {
      alive = false;
      window.clearInterval(timer);
    };
  }, []);

  // The delegation edge is not in the activity feed: it rides on the
  // streamed deltas, which name both the run and its parent.
  useEffect(() => {
    const off = Events.On('opencraft:ui', (event) => {
      const ui = (event as { data?: UIEvent }).data;
      if (!ui || ui.type !== 'stream') return;
      const payload = ui.data as StreamPayload | undefined;
      const runId = payload?.run_id;
      const parentRunId = payload?.parent_run_id;
      if (!runId || !parentRunId || runId === parentRunId) return;
      setParents((prev) =>
        prev[runId] === parentRunId ? prev : { ...prev, [runId]: parentRunId },
      );
    });
    return off;
  }, []);

  if (rows.length === 0) return null;

  const byRun = new Map<string, DockRow>();
  for (const row of rows) {
    if (row.runId !== undefined) byRun.set(row.runId, row);
  }
  const children = new Map<string, DockRow[]>();
  const roots: DockRow[] = [];
  for (const row of rows) {
    const parent = row.runId === undefined ? undefined : parents[row.runId];
    if (parent !== undefined && parent !== row.runId && byRun.has(parent)) {
      const siblings = children.get(parent) ?? [];
      siblings.push(row);
      children.set(parent, siblings);
      continue;
    }
    roots.push(row);
  }

  const open = (row: DockRow) => {
    if (row.conversationId) {
      void resume(row.conversationId);
    } else {
      openTools('agents');
    }
  };

  const pill = (row: DockRow, depth: number) => {
    const runId = row.runId ?? '';
    const nested = children.get(runId) ?? [];
    const folded = runId !== '' && collapsed[runId] === true;
    return (
      <div
        key={row.key}
        className="flex shrink-0 items-center gap-1.5"
        data-run={row.runId}
        data-depth={depth}
        data-parent={runId === '' ? undefined : parents[runId]}
      >
        <button
          onClick={() => open(row)}
          className="flex shrink-0 items-center gap-1.5 rounded-control border border-edge bg-panel px-2 py-1 text-xs text-fg transition-colors hover:border-accent/50"
          data-tip={`${row.phase}${row.toolName ? ` · ${row.toolName}` : ''}`}
        >
          <span className={`h-2 w-2 rounded-full ${phaseDot(row.phase)}`} />
          <span className="font-medium">{row.name}</span>
          {row.toolName && (
            <span className="max-w-28 truncate text-dim">{row.toolName}</span>
          )}
        </button>
        {nested.length > 0 && (
          <button
            onClick={() => {
              if (runId === '') return;
              setCollapsed((prev) => ({ ...prev, [runId]: !folded }));
            }}
            aria-expanded={!folded}
            aria-label={
              folded ? t('config.subagentExpand') : t('config.subagentCollapse')
            }
            className="flex shrink-0 items-center gap-0.5 rounded-control border border-edge px-1 py-0.5 text-micro text-dim hover:border-accent/50 hover:text-fg"
          >
            {folded ? (
              <ChevronRight size={ICON.xs} />
            ) : (
              <ChevronDown size={ICON.xs} />
            )}
            <span className="tabular-nums">{nested.length}</span>
          </button>
        )}
        {!folded &&
          depth < MAX_DEPTH &&
          nested.map((child) => pill(child, depth + 1))}
      </div>
    );
  };

  return (
    <div className="flex items-center gap-2 border-t border-edge bg-panel2 px-3 py-1.5">
      <span className="text-micro font-medium uppercase tracking-wide text-dim">
        {t('config.petSubagents')}
      </span>
      <div className="flex min-w-0 flex-1 items-center gap-1.5 overflow-x-auto">
        {roots.map((row) => pill(row, 0))}
      </div>
    </div>
  );
}
