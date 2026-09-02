import { lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react';
import type { ComponentType } from 'react';
import {
  Bot,
  ChevronDown,
  ChevronRight,
  Clock,
  Download,
  ExternalLink,
  Loader2,
  MoreHorizontal,
  Plug,
  Puzzle,
  RotateCw,
  Search,
  Sparkles,
  Trash2,
  Workflow,
  X,
  Zap,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { api } from '../lib/api';
import { useStore } from '../lib/store';
import type { MCPServer, MCPStatus } from '../lib/types';
import { MCP_CATALOG, SKILL_CATALOG } from '../lib/catalog';
import type { MCPCatalogEntry, SkillCatalogEntry } from '../lib/catalog';
import { GitHubSearch } from './GitHubSearch';
import type { GitHubRepo } from './GitHubSearch';
import { probeMCPServerLaunch } from './GitHubSearch';
import { SkillDetailDrawer } from './SkillDetailDrawer';
import { PluginManager } from '../plugins/components/PluginManager';
const AgentGraphEditor = lazy(() =>
  import('./GraphView').then((m) => ({ default: m.AgentGraphEditor })),
);
const AutomationsView = lazy(() =>
  import('./AutomationsView').then((m) => ({ default: m.AutomationsView })),
);

export type ToolPage = 'agents' | 'skills' | 'plugins' | 'automations';

// MCPLogo renders the official Model Context Protocol mark (cropped from
// the modelcontextprotocol.io brand logo) as inline SVG so it inherits
// the surrounding text color and matches the other sidebar icons.
export function MCPLogo({
  className,
  size = '1.0714rem',
}: {
  className?: string;
  size?: string | number;
}) {
  return (
    <svg
      viewBox="19 23 158 168"
      width={size}
      height={size}
      fill="none"
      aria-hidden="true"
      className={className}
    >
      <path
        d="M25 97.8528L92.8823 29.9706C102.255 20.598 117.451 20.598 126.823 29.9706V29.9706C136.196 39.3431 136.196 54.5391 126.823 63.9117L75.5581 115.177"
        stroke="currentColor"
        strokeWidth="12"
        strokeLinecap="round"
      />
      <path
        d="M76.2653 114.47L126.823 63.9117C136.196 54.5391 151.392 54.5391 160.765 63.9117L161.118 64.2652C170.491 73.6378 170.491 88.8338 161.118 98.2063L99.7248 159.6C96.6006 162.724 96.6006 167.789 99.7248 170.913L112.331 183.52"
        stroke="currentColor"
        strokeWidth="12"
        strokeLinecap="round"
      />
      <path
        d="M109.853 46.9411L59.6482 97.1457C50.2757 106.518 50.2757 121.714 59.6482 131.087V131.087C69.0208 140.459 84.2168 140.459 93.5894 131.087L143.794 80.8822"
        stroke="currentColor"
        strokeWidth="12"
        strokeLinecap="round"
      />
    </svg>
  );
}

interface MCPRow {
  id: string;
  name: string;
  transport: string;
  command: string;
  url: string;
  argsText: string;
  envText: string;
  source?: string; // repo url when the row came from GitHub search
}

const newMCPID = () =>
  `mcp-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;

// MCPSection manages the MCP tool-server list: load on mount, edit rows,
// and persist with one save action (mirrors the former Settings tab).
export function MCPSection() {
  const { t } = useTranslation();
  const [mcpRows, setMCPRows] = useState<MCPRow[]>([]);
  const [mcpError, setMCPError] = useState('');
  const [saving, setSaving] = useState(false);
  const [statuses, setStatuses] = useState<Record<string, MCPStatus>>({});
  const [testing, setTesting] = useState<string | null>(null);
  const [testResult, setTestResult] = useState<{
    id: string;
    ok: boolean;
    msg: string;
  } | null>(null);
  const [discoverOpen, setDiscoverOpen] = useState(false);
  const [addingRepo, setAddingRepo] = useState(false);
  const [mcpLoading, setMCPLoading] = useState(true);
  const toast = useStore((s) => s.toast);
  const addedNames = useMemo(
    () => new Set(mcpRows.map((r) => r.name.trim()).filter(Boolean)),
    [mcpRows],
  );

  useEffect(() => {
    void api
      .mcpConfig()
      .then((servers) =>
        setMCPRows(
          (servers ?? []).map((s) => ({
            id: newMCPID(),
            name: s.name,
            transport: s.transport,
            command: s.command ?? '',
            url: s.url ?? '',
            argsText: (s.args ?? []).join(', '),
            envText: Object.entries(s.env ?? {})
              .map(([k, v]) => `${k}=${v}`)
              .join('\n'),
          })),
        ),
      )
      .catch((err) => setMCPError(String(err)))
      .finally(() => setMCPLoading(false));
    let cancelled = false;
    const refreshStatus = () => {
      void api
        .mcpStatus()
        .then((list) => {
          if (cancelled) return;
          const map: Record<string, MCPStatus> = {};
          for (const s of list) map[s.name] = s;
          setStatuses(map);
        })
        .catch(() => {
          // status polling is best-effort; the save/error path surfaces
          // real failures
        });
    };
    void refreshStatus();
    const timer = setInterval(refreshStatus, 3000);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, []);

  const updateMCP = (id: string, patch: Partial<MCPRow>) => {
    setMCPRows((prev) =>
      prev.map((r) => (r.id === id ? { ...r, ...patch } : r)),
    );
  };

  const rowToServer = (r: MCPRow): MCPServer => {
    const srv: MCPServer = {
      name: r.name.trim(),
      transport: r.transport,
    };
    if (r.transport === 'http') {
      srv.url = r.url.trim();
    } else {
      srv.command = r.command.trim();
    }
    const args = r.argsText
      .split(',')
      .map((a) => a.trim())
      .filter(Boolean);
    if (args.length > 0) srv.args = args;
    const env: Record<string, string> = {};
    for (const line of r.envText.split('\n')) {
      const eq = line.indexOf('=');
      if (eq <= 0) continue;
      env[line.slice(0, eq).trim()] = line.slice(eq + 1).trim();
    }
    if (Object.keys(env).length > 0) srv.env = env;
    return srv;
  };

  const saveMCP = async () => {
    setMCPError('');
    setSaving(true);
    const servers: MCPServer[] = mcpRows.map(rowToServer);
    try {
      await api.saveMCP(servers);
      setMCPError('');
      toast(t('config.mcpSaved'));
      void api
        .mcpStatus()
        .then((list) => {
          const map: Record<string, MCPStatus> = {};
          for (const s of list) map[s.name] = s;
          setStatuses(map);
        })
        .catch(() => {});
    } catch (err) {
      setMCPError(String(err));
    } finally {
      setSaving(false);
    }
  };

  const testRow = async (row: MCPRow) => {
    setTesting(row.id);
    setTestResult(null);
    try {
      await api.testMCP(rowToServer(row));
      setTestResult({ id: row.id, ok: true, msg: '' });
    } catch (err) {
      setTestResult({ id: row.id, ok: false, msg: String(err) });
    } finally {
      setTesting(null);
    }
  };

  const statusPill = (row: MCPRow) => {
    const st = statuses[row.name.trim()];
    if (!st) return null;
    const base =
      'flex items-center gap-1.5 rounded px-1.5 py-0.5 text-[0.7143rem] whitespace-nowrap';
    if (st.status === 'connected') {
      return (
        <span className={`${base} text-ok border border-ok/30 bg-ok/10`}>
          <span className="h-1.5 w-1.5 rounded-full bg-ok" />
          {t('config.mcpStatusConnected')}
        </span>
      );
    }
    if (st.status === 'connecting') {
      return (
        <span
          className={`${base} text-dim border border-edge bg-panel`}
          title={t('config.mcpStatusConnectingHint')}
        >
          <Loader2 size="0.7143rem" className="animate-spin" />
          {t('config.mcpStatusConnecting')}
        </span>
      );
    }
    return (
      <span
        className={`${base} text-err border border-err/40 bg-err/10`}
        title={st.error}
      >
        <span className="h-1.5 w-1.5 rounded-full bg-err" />
        {t('config.mcpStatusError')}
      </span>
    );
  };

  const addCatalogMCP = (entry: MCPCatalogEntry) => {
    setMCPRows((prev) => [
      ...prev,
      {
        id: newMCPID(),
        name: entry.name,
        transport: entry.transport,
        command: entry.command,
        url: entry.url ?? '',
        argsText: entry.args.join(', '),
        envText: '',
      },
    ]);
  };

  const addGitHubMCP = async (repo: GitHubRepo) => {
    setAddingRepo(true);
    let command = '';
    let args: string[] = [];
    try {
      const probe = await probeMCPServerLaunch(repo.full_name);
      command = probe.command;
      args = probe.args;
    } catch {
      // keep the row empty; the user fills the command from the README
    } finally {
      setAddingRepo(false);
    }
    setMCPRows((prev) => [
      ...prev,
      {
        id: newMCPID(),
        name: repo.full_name.split('/')[1] ?? repo.full_name,
        transport: 'stdio',
        command,
        url: '',
        argsText: args.join(', '),
        envText: '',
        source: repo.html_url,
      },
    ]);
  };

  return (
    <div className="space-y-3">
      <p className="text-xs text-dim">{t('config.mcpHint')}</p>
      <div className="flex items-center gap-2 text-xs text-dim">
        <Plug size="0.9286rem" className="text-accent" />
        {t('config.mcpCount', { count: mcpRows.length })}
      </div>
      <div className="rounded-xl border border-edge bg-panel2">
        <button
          onClick={() => setDiscoverOpen((v) => !v)}
          className="w-full flex items-center gap-2 px-3 py-2 text-left text-sm hover:bg-panel2/70"
        >
          <Sparkles size="1.0000rem" className="text-accent shrink-0" />
          <span className="flex-1">{t('config.mcpDiscover')}</span>
          {discoverOpen ? (
            <ChevronDown size="1.0000rem" className="text-dim" />
          ) : (
            <ChevronRight size="1.0000rem" className="text-dim" />
          )}
        </button>
        {discoverOpen && (
          <div className="border-t border-edge px-3 py-2 space-y-1">
            <p className="text-xs text-dim pb-1">
              {t('config.mcpDiscoverHint')}
            </p>
            {MCP_CATALOG.map((entry) => (
              <div
                key={entry.name}
                className="flex items-center gap-2 rounded-lg px-2 py-1 hover:bg-panel"
              >
                <span className="text-sm min-w-0 truncate">{entry.name}</span>
                <span className="flex-1 text-xs text-dim min-w-0 truncate">
                  {entry.description}
                </span>
                <code className="text-[0.7143rem] text-dim shrink-0 hidden sm:inline">
                  {entry.command} {entry.args.join(' ')}
                </code>
                {addedNames.has(entry.name) ? (
                  <span className="shrink-0 rounded-md border border-edge px-2 py-0.5 text-xs text-dim">
                    {t('config.mcpAdded')}
                  </span>
                ) : (
                  <button
                    onClick={() => addCatalogMCP(entry)}
                    className="shrink-0 rounded-md border border-accent/40 px-2 py-0.5 text-xs text-accent hover:bg-accent/10"
                  >
                    {t('config.mcpAdd')}
                  </button>
                )}
              </div>
            ))}
            <div className="border-t border-edge/60 pt-2 mt-1">
              <GitHubSearch
                topic="mcp-server"
                placeholder={t('config.mcpSearchPlaceholder')}
                actionLabel={t('config.mcpAdd')}
                onPick={(repo) => void addGitHubMCP(repo)}
                busy={addingRepo}
              />
              <p className="text-[0.7857rem] text-dim">
                {t('config.mcpSearchHint')}
              </p>
            </div>
          </div>
        )}
      </div>
      {mcpLoading && mcpRows.length === 0 ? (
        <div className="space-y-3">
          {[0, 1].map((i) => (
            <div
              key={i}
              className="h-28 animate-pulse rounded-xl border border-edge bg-panel2"
            />
          ))}
        </div>
      ) : mcpRows.length === 0 ? (
        <p className="text-sm text-dim">{t('config.mcpEmpty')}</p>
      ) : null}
      <div className="space-y-3">
        {mcpRows.map((row) => (
          <div
            key={row.id}
            className="rounded-xl border border-edge bg-panel2 p-3 space-y-2"
          >
            <div className="flex items-center gap-2">
              <Plug size="0.9286rem" className="text-accent shrink-0" />
              <input
                value={row.name}
                onChange={(e) => updateMCP(row.id, { name: e.target.value })}
                placeholder={t('config.mcpName')}
                title={row.source ?? ''}
                className="flex-1 rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
              />
              {row.source && (
                <button
                  onClick={() => void api.openExternal(row.source!)}
                  className="text-dim hover:text-fg"
                  title={t('config.mcpOpenRepo')}
                >
                  <ExternalLink size="0.9286rem" />
                </button>
              )}
              <select
                value={row.transport}
                onChange={(e) =>
                  updateMCP(row.id, { transport: e.target.value })
                }
                className="rounded-lg border border-edge bg-panel px-2 py-1.5 text-sm outline-none"
              >
                <option value="stdio">stdio</option>
                <option value="http">http</option>
              </select>
              {statusPill(row)}
              <button
                onClick={() =>
                  setMCPRows((prev) => prev.filter((r) => r.id !== row.id))
                }
                className="text-dim hover:text-err"
                title={t('config.mcpRemove')}
                aria-label={t('config.mcpRemove')}
              >
                <Trash2 size="1.0000rem" />
              </button>
            </div>
            <div className="space-y-2">
              <div>
                <div className="mb-1 text-[0.7857rem] text-dim">
                  {t('config.mcpCommandLabel')}
                </div>
                {row.transport === 'stdio' ? (
                  <input
                    value={row.command}
                    onChange={(e) =>
                      updateMCP(row.id, { command: e.target.value })
                    }
                    placeholder={t('config.mcpCommand')}
                    className="w-full rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
                  />
                ) : (
                  <input
                    value={row.url}
                    onChange={(e) => updateMCP(row.id, { url: e.target.value })}
                    placeholder={t('config.mcpURL')}
                    className="w-full rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
                  />
                )}
              </div>
              <div>
                <div className="mb-1 text-[0.7857rem] text-dim">
                  {t('config.mcpArgsLabel')}
                </div>
                <input
                  value={row.argsText}
                  onChange={(e) =>
                    updateMCP(row.id, { argsText: e.target.value })
                  }
                  placeholder={t('config.mcpArgs')}
                  className="w-full rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
                />
              </div>
              <div>
                <div className="mb-1 text-[0.7857rem] text-dim">
                  {t('config.mcpEnvLabel')}
                </div>
                <textarea
                  value={row.envText}
                  onChange={(e) =>
                    updateMCP(row.id, { envText: e.target.value })
                  }
                  placeholder={t('config.mcpEnv')}
                  rows={2}
                  className="w-full resize-none rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
                />
              </div>
            </div>
            <div className="flex items-center gap-2 border-t border-edge/60 pt-2">
              <button
                onClick={() => void testRow(row)}
                disabled={
                  testing === row.id ||
                  !row.name.trim() ||
                  (row.transport === 'stdio'
                    ? !row.command.trim()
                    : !row.url.trim())
                }
                className="flex items-center gap-1.5 rounded-lg border border-edge px-2.5 py-1 text-xs text-dim hover:text-fg disabled:opacity-40"
              >
                {testing === row.id ? (
                  <Loader2 size="0.8571rem" className="animate-spin" />
                ) : (
                  <Zap size="0.8571rem" />
                )}
                {testing === row.id
                  ? t('config.mcpTesting')
                  : t('config.mcpTest')}
              </button>
              {testResult?.id === row.id && (
                <span
                  className={`max-w-[60%] truncate rounded-lg border px-2 py-1 text-xs ${
                    testResult.ok
                      ? 'border-ok/30 bg-ok/10 text-ok'
                      : 'border-err/30 bg-err/10 text-err'
                  }`}
                  title={testResult.msg || undefined}
                >
                  {testResult.ok
                    ? t('config.mcpTestSuccess')
                    : t('config.mcpTestFailed', { error: testResult.msg })}
                </span>
              )}
            </div>
          </div>
        ))}
      </div>
      <div className="flex items-center gap-2">
        <button
          onClick={() =>
            setMCPRows((prev) => [
              ...prev,
              {
                id: newMCPID(),
                name: '',
                transport: 'stdio',
                command: '',
                url: '',
                argsText: '',
                envText: '',
              },
            ])
          }
          className="flex items-center gap-1.5 rounded-lg border border-edge px-3 py-1.5 text-sm text-dim hover:text-fg"
        >
          <Plug size="1.0000rem" />
          {t('config.mcpAdd')}
        </button>
        <button
          onClick={() => void saveMCP()}
          disabled={saving}
          className="rounded-lg bg-accent px-4 py-1.5 text-sm text-white hover:opacity-90 disabled:opacity-50"
        >
          {t('setup.saveApply')}
        </button>
        {mcpError && <span className="text-xs text-err">{mcpError}</span>}
      </div>
    </div>
  );
}

// AgentsSection lists registered subagents (created by the assistant
// through create_agent) and lets the user delete them.
export function AgentsSection({ onEdit }: { onEdit: (name: string) => void }) {
  const agents = useStore((s) => s.agents);
  const refreshAgents = useStore((s) => s.refreshAgents);
  const { t } = useTranslation();
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    void refreshAgents();
  }, [refreshAgents]);

  const deleteAgent = async (name: string) => {
    setError('');
    try {
      await api.unregisterAgent(name);
      setConfirmDelete(null);
      await refreshAgents();
    } catch (err) {
      setError(String(err));
      setConfirmDelete(null);
    }
  };

  const fmtWhen = (iso: string) => {
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return '';
    const diff = Date.now() - d.getTime();
    if (diff < 60_000) return t('sidebar.justNow');
    if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m`;
    if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h`;
    return d.toLocaleDateString();
  };

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <h3 className="text-sm font-semibold">{t('sidebar.subagents')}</h3>
        <span className="rounded border border-edge bg-panel2 px-1.5 py-0.5 text-xs text-dim tabular-nums">
          {agents.length}
        </span>
      </div>
      <p className="text-xs text-dim">{t('config.agentsHint')}</p>
      {error && <p className="text-xs text-err">{error}</p>}
      {agents.length === 0 ? (
        <div className="grid place-items-center rounded-xl border border-dashed border-edge bg-panel2/50 py-10 text-sm text-dim">
          {t('config.agentsEmpty')}
        </div>
      ) : (
        <ul className="space-y-2">
          {agents.map((a) => (
            <li
              key={a.name}
              onClick={() => onEdit(a.name)}
              className="group flex cursor-pointer items-center gap-3 rounded-xl border border-edge bg-panel2 p-3 transition-colors hover:border-accent/40"
            >
              <span className="grid h-9 w-9 shrink-0 place-items-center rounded-lg border border-accent/20 bg-accent/10">
                <Bot size="1.1429rem" className="text-accent" />
              </span>
              <div className="flex-1 min-w-0">
                <div className="flex items-baseline gap-2">
                  <span className="truncate text-sm font-medium">{a.name}</span>
                  {a.created_at && (
                    <span className="shrink-0 text-[0.7143rem] text-dim tabular-nums">
                      {fmtWhen(a.created_at)}
                    </span>
                  )}
                </div>
                {a.description ? (
                  <p className="mt-0.5 line-clamp-2 text-xs text-dim">
                    {a.description}
                  </p>
                ) : (
                  <p className="mt-0.5 text-xs italic text-dim/60">
                    {t('config.agentsNoDesc')}
                  </p>
                )}
              </div>
              <div className="flex shrink-0 items-center gap-1.5">
                <button
                  onClick={(e) => {
                    e.stopPropagation();
                    onEdit(a.name);
                  }}
                  className="flex items-center gap-1 rounded-lg border border-edge px-2.5 py-1 text-xs text-dim hover:border-accent/40 hover:text-accent"
                >
                  <Workflow size="0.8571rem" />
                  {t('config.agentsEditGraph')}
                </button>
                <button
                  onClick={(e) => {
                    e.stopPropagation();
                    setConfirmDelete(a.name);
                  }}
                  className="flex items-center gap-1 rounded-lg border border-edge px-2.5 py-1 text-xs text-dim hover:border-err/40 hover:text-err"
                >
                  <Trash2 size="0.8571rem" />
                  {t('config.agentsDelete')}
                </button>
              </div>
            </li>
          ))}
        </ul>
      )}
      {confirmDelete && (
        <div className="rounded-xl border border-err/40 bg-err/5 p-4">
          <p className="text-sm">
            {t('config.agentsDeleteConfirm', { name: confirmDelete })}
          </p>
          <div className="mt-3 flex justify-end gap-2">
            <button
              onClick={() => setConfirmDelete(null)}
              className="rounded-lg border border-edge px-4 py-1.5 text-sm text-dim hover:text-fg"
            >
              {t('interact.cancel')}
            </button>
            <button
              onClick={() => void deleteAgent(confirmDelete)}
              className="flex items-center gap-1.5 rounded-lg bg-err px-4 py-1.5 text-sm text-white hover:opacity-90"
            >
              <Trash2 size="0.9286rem" />
              {t('config.agentsDelete')}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

interface SkillRow {
  name: string;
  description: string;
  scope: string;
  path: string;
  plugin_id?: string;
  plugin_name?: string;
}

// SkillsSection lists skills discovered by the runtime; builtin and
// plugin-provided skills are read-only, user skills can be deleted.
export function SkillsSection() {
  const { t } = useTranslation();
  const flash = useStore((s) => s.flash);
  const [skills, setSkills] = useState<SkillRow[]>([]);
  const [skillToDelete, setSkillToDelete] = useState<{
    name: string;
    path: string;
  } | null>(null);
  const [error, setError] = useState('');
  const [importOpen, setImportOpen] = useState(false);
  const [repo, setRepo] = useState('');
  const [scope, setScope] = useState('user');
  const [subpath, setSubpath] = useState('');
  const [installing, setInstalling] = useState(false);
  const [discoverOpen, setDiscoverOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [menuFor, setMenuFor] = useState<string | null>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const [selectedSkill, setSelectedSkill] = useState<SkillRow | null>(null);
  const installedNames = useMemo(
    () => new Set(skills.map((s) => s.name)),
    [skills],
  );
  // Discovery reports the frontmatter `name`, which can differ from the
  // install directory name (e.g. codex's code-review-breaking-changes
  // declares `name: code-breaking-changes`). Match the parent directory
  // of each SKILL.md as well, since that is the real install target.
  const installedDirs = useMemo(() => {
    const dirs = new Set<string>();
    for (const s of skills) {
      const cleaned = s.path.replace(/\/SKILL\.md$/i, '');
      const parts = cleaned.split('/').filter(Boolean);
      const dir = parts[parts.length - 1];
      if (dir) dirs.add(dir);
    }
    return dirs;
  }, [skills]);

  const isCatalogSkillInstalled = (entry: SkillCatalogEntry) =>
    installedNames.has(entry.name) || installedDirs.has(entry.name);

  const filteredSkills = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return skills;
    return skills.filter((s) =>
      [s.name, s.description, s.path, s.scope, s.plugin_id, s.plugin_name].some(
        (v) => v?.toLowerCase().includes(q),
      ),
    );
  }, [query, skills]);

  useEffect(() => {
    void reloadSkills();
  }, []);

  useEffect(() => {
    if (!menuFor) return;
    const onDown = (e: PointerEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        setMenuFor(null);
      }
    };
    document.addEventListener('pointerdown', onDown);
    return () => document.removeEventListener('pointerdown', onDown);
  }, [menuFor]);

  const reloadSkills = () => {
    setError('');
    return api
      .skills()
      .then(setSkills)
      .catch((err) => setError(String(err)));
  };

  const deleteSkill = async (path: string) => {
    setError('');
    try {
      await api.deleteSkill(path);
      setSkills((prev) => prev.filter((s) => s.path !== path));
      setSkillToDelete(null);
    } catch (err) {
      setError(String(err));
      setSkillToDelete(null);
    }
  };

  const installSkill = async () => {
    if (!repo.trim()) {
      setError(t('config.skillsImportRepoRequired'));
      return;
    }
    setInstalling(true);
    setError('');
    try {
      const path = await api.installSkill(repo.trim(), scope, subpath.trim());
      setRepo('');
      setSubpath('');
      setImportOpen(false);
      await reloadSkills();
      flash(t('config.skillsImported', { path }));
    } catch (err) {
      setError(String(err));
    } finally {
      setInstalling(false);
    }
  };

  const installCatalogSkill = async (entry: SkillCatalogEntry) => {
    setInstalling(true);
    setError('');
    try {
      const path = await api.installSkill(
        entry.repo,
        entry.scope,
        entry.subpath,
      );
      await reloadSkills();
      flash(t('config.skillsImported', { path }));
    } catch (err) {
      setError(String(err));
    } finally {
      setInstalling(false);
    }
  };

  const installGitHubSkill = async (repo: GitHubRepo) => {
    setInstalling(true);
    setError('');
    try {
      const path = await api.installSkill(repo.clone_url, 'user', '');
      await reloadSkills();
      flash(t('config.skillsImported', { path }));
    } catch (err) {
      setError(String(err));
    } finally {
      setInstalling(false);
    }
  };

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-start justify-between gap-3">
        <p className="min-w-0 flex-1 text-xs text-dim">
          {t('config.skillsHint')}
        </p>
        <div className="flex shrink-0 items-center gap-2">
          <button
            onClick={() => void reloadSkills()}
            className="flex items-center gap-1.5 rounded-lg border border-edge bg-panel2 px-2.5 py-1 text-xs text-dim hover:text-fg"
          >
            <RotateCw size="0.8571rem" />
            {t('config.skillsRefresh')}
          </button>
          <button
            onClick={() => setImportOpen(true)}
            className="flex items-center gap-1.5 rounded-lg bg-accent px-2.5 py-1 text-xs text-white hover:opacity-90"
          >
            <Download size="0.8571rem" />
            {t('config.skillsImport')}
          </button>
        </div>
      </div>
      {error && <p className="text-xs text-err">{error}</p>}
      <div className="relative">
        <Search
          size="0.8571rem"
          className="absolute left-2.5 top-1/2 -translate-y-1/2 text-dim"
        />
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t('config.skillsSearch')}
          className="w-full rounded-lg border border-edge bg-panel pl-8 pr-3 py-1.5 text-sm outline-none focus:border-accent"
        />
      </div>
      <div className="rounded-xl border border-edge bg-panel2">
        <button
          onClick={() => setDiscoverOpen((v) => !v)}
          className="w-full flex items-center gap-2 px-3 py-2 text-left text-sm hover:bg-panel2/70"
        >
          <Sparkles size="1.0000rem" className="text-accent shrink-0" />
          <span className="flex-1">{t('config.skillsDiscover')}</span>
          {discoverOpen ? (
            <ChevronDown size="1.0000rem" className="text-dim" />
          ) : (
            <ChevronRight size="1.0000rem" className="text-dim" />
          )}
        </button>
        {discoverOpen && (
          <div className="border-t border-edge px-3 py-2 space-y-1">
            <p className="text-xs text-dim pb-1">
              {t('config.skillsDiscoverHint')}
            </p>
            {SKILL_CATALOG.map((entry) => (
              <div
                key={entry.name}
                className="flex items-center gap-2 rounded-lg px-2 py-1 hover:bg-panel"
              >
                <span className="text-sm min-w-0 truncate">{entry.name}</span>
                <span className="flex-1 text-xs text-dim min-w-0 truncate">
                  {entry.description}
                </span>
                {isCatalogSkillInstalled(entry) ? (
                  <span className="shrink-0 rounded-md border border-edge px-2 py-0.5 text-xs text-dim">
                    {t('config.skillsInstalled')}
                  </span>
                ) : (
                  <button
                    onClick={() => void installCatalogSkill(entry)}
                    disabled={installing}
                    className="shrink-0 rounded-md border border-accent/40 px-2 py-0.5 text-xs text-accent hover:bg-accent/10 disabled:opacity-40"
                  >
                    {installing
                      ? t('config.skillsInstalling')
                      : t('config.skillsInstall')}
                  </button>
                )}
              </div>
            ))}
            <div className="border-t border-edge/60 pt-2 mt-1">
              <GitHubSearch
                topic="codex-skill"
                placeholder={t('config.skillsSearchPlaceholder')}
                actionLabel={t('config.skillsInstall')}
                onPick={installGitHubSkill}
                busy={installing}
              />
            </div>
          </div>
        )}
      </div>
      {filteredSkills.length === 0 ? (
        <div className="rounded-xl border border-edge bg-panel2 p-6 text-center text-sm text-dim">
          {skills.length === 0
            ? t('config.skillsEmpty')
            : t('config.skillsSearchEmpty')}
        </div>
      ) : (
        <ul className="flex flex-col gap-2">
          {filteredSkills.map((s) => {
            const removable = s.scope !== 'builtin' && !s.plugin_id;
            const scopeLabel =
              s.scope === 'builtin'
                ? t('config.skillsScopeBuiltin')
                : s.scope === 'user'
                  ? t('config.skillsScopeUser')
                  : s.scope === 'repo'
                    ? t('config.skillsScopeRepo')
                    : s.scope;
            return (
              <li
                key={s.path}
                className="rounded-xl border border-edge bg-panel2 p-3 transition-colors hover:border-accent/40"
              >
                <div className="flex items-start gap-2">
                  <button
                    type="button"
                    onClick={() => setSelectedSkill(s)}
                    className="flex min-w-0 flex-1 items-start gap-2 text-left"
                    title={s.name}
                  >
                    <Sparkles
                      size="1.0714rem"
                      className="mt-0.5 shrink-0 text-accent"
                    />
                    <span className="min-w-0 flex-1">
                      <span className="flex flex-wrap items-center gap-1.5">
                        <span className="min-w-0 truncate text-sm font-semibold">
                          {s.name}
                        </span>
                        {s.plugin_id ? (
                          <span className="shrink-0 rounded border border-accent/30 bg-accent/10 px-1.5 py-0.5 text-[0.7143rem] text-accent">
                            {t('config.skillsPluginFrom', {
                              name: s.plugin_name || s.plugin_id,
                            })}
                          </span>
                        ) : (
                          <span className="shrink-0 rounded border border-edge bg-panel px-1.5 py-0.5 text-[0.7143rem] text-dim">
                            {scopeLabel}
                          </span>
                        )}
                      </span>
                      <span
                        className="mt-0.5 block truncate font-mono text-xs text-dim"
                        title={s.path}
                      >
                        {s.path}
                      </span>
                      {s.description && (
                        <span
                          className="mt-1 block truncate text-xs text-dim"
                          title={s.description}
                        >
                          {s.description}
                        </span>
                      )}
                    </span>
                  </button>
                  {removable && (
                    <div className="relative shrink-0">
                      <button
                        onClick={() =>
                          setMenuFor(menuFor === s.path ? null : s.path)
                        }
                        aria-label={t('config.skillsMore')}
                        title={t('config.skillsMore')}
                        className="rounded-lg p-1.5 text-dim hover:bg-panel hover:text-fg"
                      >
                        <MoreHorizontal size="1.0000rem" />
                      </button>
                      {menuFor === s.path && (
                        <div
                          ref={menuRef}
                          className="absolute right-0 top-full z-40 mt-1.5 w-44 rounded-lg border border-edge bg-panel p-1 shadow-xl"
                        >
                          <button
                            onClick={() => {
                              setMenuFor(null);
                              setSkillToDelete({ name: s.name, path: s.path });
                            }}
                            className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs text-dim hover:bg-err/10 hover:text-err"
                          >
                            <Trash2 size="0.8571rem" className="shrink-0" />
                            <span className="flex-1 text-left">
                              {t('config.skillsDelete')}
                            </span>
                          </button>
                        </div>
                      )}
                    </div>
                  )}
                </div>
                {skillToDelete?.path === s.path && (
                  <div className="mt-2 flex items-center gap-2 rounded-lg border border-err/40 bg-err/10 px-2 py-1.5 text-xs text-dim">
                    <span className="min-w-0 flex-1 truncate">
                      {t('config.skillsDeleteConfirm', { name: s.name })}
                    </span>
                    <button
                      onClick={() => void deleteSkill(s.path)}
                      className="shrink-0 rounded bg-err px-2 py-1 text-white hover:opacity-90"
                    >
                      {t('config.skillsDelete')}
                    </button>
                    <button
                      onClick={() => setSkillToDelete(null)}
                      className="shrink-0 rounded border border-edge px-2 py-1 hover:text-fg"
                    >
                      {t('interact.cancel')}
                    </button>
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      )}
      {selectedSkill && (
        <SkillDetailDrawer
          skill={selectedSkill}
          onClose={() => setSelectedSkill(null)}
        />
      )}
      {importOpen && (
        <div
          className="fixed inset-0 z-50 grid place-items-center bg-black/60 p-6"
          onClick={() => setImportOpen(false)}
        >
          <div
            className="w-[34rem] max-w-full rounded-2xl border border-edge bg-panel shadow-2xl"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="flex items-center justify-between border-b border-edge px-4 py-3">
              <h3 className="text-sm font-semibold">
                {t('config.skillsImport')}
              </h3>
              <button
                onClick={() => setImportOpen(false)}
                className="text-dim hover:text-fg"
                aria-label={t('tools.close')}
              >
                <X size="1.1429rem" />
              </button>
            </div>
            <div className="flex flex-col gap-3 p-4">
              <p className="text-xs text-dim">{t('config.skillsImportHint')}</p>
              <input
                value={repo}
                onChange={(e) => setRepo(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') void installSkill();
                }}
                placeholder={t('config.skillsImportRepo')}
                className="w-full rounded-lg border border-edge bg-panel2 px-2.5 py-1.5 text-xs outline-none focus:border-accent"
                autoFocus
              />
              <div className="flex gap-2">
                <select
                  value={scope}
                  onChange={(e) => setScope(e.target.value)}
                  title={t('config.skillsImportScope')}
                  className="shrink-0 rounded-lg border border-edge bg-panel2 px-2 py-1.5 text-xs outline-none"
                >
                  <option value="user">
                    {t('config.skillsImportScopeUser')}
                  </option>
                  <option value="repo">
                    {t('config.skillsImportScopeRepo')}
                  </option>
                </select>
                <input
                  value={subpath}
                  onChange={(e) => setSubpath(e.target.value)}
                  placeholder={t('config.skillsImportSubpath')}
                  className="min-w-0 flex-1 rounded-lg border border-edge bg-panel2 px-2.5 py-1.5 text-xs outline-none focus:border-accent"
                />
              </div>
              {error && (
                <p className="text-[0.7857rem] text-err break-words">{error}</p>
              )}
              <div className="flex justify-end gap-2">
                <button
                  onClick={() => setImportOpen(false)}
                  className="rounded-lg px-3 py-1.5 text-xs text-dim hover:text-fg"
                >
                  {t('config.cancel')}
                </button>
                <button
                  onClick={() => void installSkill()}
                  disabled={installing || !repo.trim()}
                  className="flex items-center gap-1.5 rounded-lg bg-accent px-3 py-1.5 text-xs text-white hover:opacity-90 disabled:opacity-50"
                >
                  {installing && (
                    <Loader2 size="0.8571rem" className="animate-spin" />
                  )}
                  {t('config.skillsImportRun')}
                </button>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

const VIEW_META: {
  id: ToolPage;
  icon: ComponentType<{ className?: string }>;
  label: (t: (k: string) => string) => string;
}[] = [
  { id: 'agents', icon: Bot, label: (t) => t('config.tabAgents') },
  { id: 'skills', icon: Sparkles, label: (t) => t('config.tabSkills') },
  { id: 'plugins', icon: Puzzle, label: (t) => t('config.tabPlugins') },
  {
    id: 'automations',
    icon: Clock,
    label: (t) => t('sidebar.automations'),
  },
];

// ToolsPanel is the right-side page shown when one of the sidebar tool
// buttons (MCP / subagents / skills) is active. It owns the section
// header and delegates the content to the section. Switching between
// sections happens through the left sidebar buttons.
export function ToolsPanel() {
  const view = useStore((s) => s.toolsView);
  const closeTools = useStore((s) => s.closeTools);
  const refreshAgents = useStore((s) => s.refreshAgents);
  const { t } = useTranslation();
  const [editingAgent, setEditingAgent] = useState<string | null>(null);

  if (!view) return null;

  const meta = VIEW_META.find((v) => v.id === view);
  if (!meta) return null;
  const HeaderIcon = meta?.icon ?? MCPLogo;

  return (
    <main className="flex-1 min-w-0 h-full flex flex-col min-h-0 bg-panel">
      <header
        className="h-11 shrink-0 border-b border-edge flex items-center gap-3 px-4 select-none"
        style={{ ['--wails-draggable' as string]: 'drag' }}
      >
        <HeaderIcon className="h-4 w-4 shrink-0 text-accent" />
        <h2 className="text-sm font-semibold">{meta?.label(t)}</h2>
        <span className="flex-1" />
        <div
          className="flex items-center"
          style={{ ['--wails-draggable' as string]: 'no-drag' }}
        >
          <button
            onClick={closeTools}
            className="text-dim hover:text-fg"
            title={t('tools.close')}
          >
            <X size="1.2857rem" />
          </button>
        </div>
      </header>
      <div
        className={
          view === 'agents' && editingAgent
            ? 'min-h-0 flex-1 p-4'
            : 'flex-1 overflow-y-auto px-5 py-4'
        }
      >
        {view === 'agents' && editingAgent ? (
          <Suspense
            fallback={
              <div className="grid h-full place-items-center text-dim text-sm">
                {t('app.starting')}
              </div>
            }
          >
            <AgentGraphEditor
              agentName={editingAgent}
              onClose={() => setEditingAgent(null)}
              onSaved={() => void refreshAgents()}
            />
          </Suspense>
        ) : (
          <>
            {view === 'agents' && <AgentsSection onEdit={setEditingAgent} />}
            {view === 'skills' && <SkillsSection />}
            {view === 'plugins' && <PluginManager showTitle={false} />}
            {view === 'automations' && (
              <Suspense
                fallback={
                  <div className="grid h-full place-items-center text-dim text-sm">
                    {t('app.starting')}
                  </div>
                }
              >
                <AutomationsView />
              </Suspense>
            )}
          </>
        )}
      </div>
    </main>
  );
}
