import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api } from '../lib/api';
import { useStore } from '../lib/store';
import type { AgentSummary } from '../lib/types';
import type { PetActivityDTO } from '../pet/state';

interface DockRow {
  name: string;
  phase: PetActivityDTO['phase'];
  toolName?: string;
  conversationId?: string;
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

/**
 * SubagentDock lists subagents that currently have live activity. It
 * appears inside the main window (subagents never get desktop
 * windows); clicking a row opens the Subagents panel.
 */
export function SubagentDock() {
  const { t } = useTranslation();
  const openTools = useStore((s) => s.openTools);
  const resume = useStore((s) => s.resume);
  const [rows, setRows] = useState<DockRow[]>([]);

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
            name: agent.name,
            phase: activity.phase,
            toolName: activity.tool?.name,
            conversationId: activity.conversation_id,
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

  if (rows.length === 0) return null;

  return (
    <div className="flex items-center gap-2 border-t border-edge bg-panel2 px-3 py-1.5">
      <span className="text-micro font-medium uppercase tracking-wide text-dim">
        {t('config.petSubagents')}
      </span>
      <div className="flex min-w-0 flex-1 items-center gap-1.5 overflow-x-auto">
        {rows.map((row) => (
          <button
            key={row.name}
            onClick={() => {
              if (row.conversationId) {
                void resume(row.conversationId);
              } else {
                openTools('agents');
              }
            }}
            className="flex shrink-0 items-center gap-1.5 rounded-control border border-edge bg-panel px-2 py-1 text-xs text-fg transition-colors hover:border-accent/50"
            title={`${row.phase}${row.toolName ? ` · ${row.toolName}` : ''}`}
          >
            <span className={`h-2 w-2 rounded-full ${phaseDot(row.phase)}`} />
            <span className="font-medium">{row.name}</span>
            {row.toolName && (
              <span className="max-w-28 truncate text-dim">{row.toolName}</span>
            )}
          </button>
        ))}
      </div>
    </div>
  );
}
