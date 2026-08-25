import { useEffect, useState } from 'react';
import type { ComponentType } from 'react';
import {
  Bot,
  Download,
  Kanban,
  Loader2,
  Plug,
  RotateCw,
  Sparkles,
  Trash2,
  X,
  Zap,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { api } from '../lib/api';
import { useStore } from '../lib/store';
import type { MCPServer, MCPStatus } from '../lib/types';
import { KanbanSection } from './KanbanView';

export type ToolPage = 'mcp' | 'agents' | 'skills' | 'kanban';

// MCPLogo renders the official Model Context Protocol mark (cropped from
// the modelcontextprotocol.io brand logo) as inline SVG so it inherits
// the surrounding text color and matches the other sidebar icons.
export function MCPLogo({ className }: { className?: string }) {
  return (
    <svg
      viewBox="19 23 158 168"
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
      .catch((err) => setMCPError(String(err)));
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
      'flex items-center gap-1.5 rounded px-1.5 py-0.5 text-[10px] whitespace-nowrap';
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
          <Loader2 size={10} className="animate-spin" />
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

  return (
    <div className="space-y-3">
      <p className="text-xs text-dim">{t('config.mcpHint')}</p>
      {mcpRows.length === 0 && (
        <p className="text-sm text-dim">{t('config.mcpEmpty')}</p>
      )}
      <div className="space-y-3">
        {mcpRows.map((row) => (
          <div
            key={row.id}
            className="rounded-xl border border-edge bg-panel2 p-3 space-y-2"
          >
            <div className="flex items-center gap-2">
              <input
                value={row.name}
                onChange={(e) => updateMCP(row.id, { name: e.target.value })}
                placeholder={t('config.mcpName')}
                className="flex-1 rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
              />
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
              >
                <Trash2 size={14} />
              </button>
            </div>
            {row.transport === 'stdio' ? (
              <input
                value={row.command}
                onChange={(e) => updateMCP(row.id, { command: e.target.value })}
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
            <input
              value={row.argsText}
              onChange={(e) => updateMCP(row.id, { argsText: e.target.value })}
              placeholder={t('config.mcpArgs')}
              className="w-full rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
            />
            <textarea
              value={row.envText}
              onChange={(e) => updateMCP(row.id, { envText: e.target.value })}
              placeholder={t('config.mcpEnv')}
              rows={2}
              className="w-full resize-none rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
            />
            <div className="flex items-center gap-2">
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
                  <Loader2 size={12} className="animate-spin" />
                ) : (
                  <Zap size={12} />
                )}
                {testing === row.id
                  ? t('config.mcpTesting')
                  : t('config.mcpTest')}
              </button>
              {testResult?.id === row.id && (
                <span
                  className={`text-xs break-all ${
                    testResult.ok ? 'text-ok' : 'text-err'
                  }`}
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
          <Plug size={14} />
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
export function AgentsSection() {
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

  return (
    <div className="space-y-3">
      <p className="text-xs text-dim">{t('config.agentsHint')}</p>
      {error && <p className="text-xs text-err">{error}</p>}
      {agents.length === 0 ? (
        <p className="text-sm text-dim">{t('config.agentsEmpty')}</p>
      ) : (
        agents.map((a) => (
          <div
            key={a.name}
            className="flex items-start gap-3 rounded-xl border border-edge bg-panel2 p-3"
          >
            <Bot size={16} className="text-accent mt-0.5 shrink-0" />
            <div className="flex-1 min-w-0">
              <div className="text-sm font-medium">{a.name}</div>
              <p className="text-xs text-dim mt-0.5">{a.description}</p>
            </div>
            <button
              onClick={() => setConfirmDelete(a.name)}
              className="flex items-center gap-1 rounded-lg border border-edge px-2.5 py-1 text-xs text-dim hover:text-err hover:border-err/40"
            >
              <Trash2 size={12} />
              {t('config.agentsDelete')}
            </button>
          </div>
        ))
      )}
      {confirmDelete && (
        <div className="rounded-xl border border-err/40 bg-panel2 p-4">
          <p className="text-sm">
            {t('config.agentsDeleteConfirm', { name: confirmDelete })}
          </p>
          <div className="mt-3 flex gap-2">
            <button
              onClick={() => setConfirmDelete(null)}
              className="rounded-lg border border-edge px-4 py-1.5 text-sm text-dim hover:text-fg"
            >
              {t('interact.cancel')}
            </button>
            <button
              onClick={() => void deleteAgent(confirmDelete)}
              className="rounded-lg bg-err px-4 py-1.5 text-sm text-white hover:opacity-90"
            >
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
}

// SkillsSection lists skills discovered by the runtime; builtin skills
// are read-only, user skills can be deleted.
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

  useEffect(() => {
    void reloadSkills();
  }, []);

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

  return (
    <div className="space-y-3">
      <div className="flex items-start justify-between gap-3">
        <p className="text-xs text-dim">{t('config.skillsHint')}</p>
        <div className="flex items-center gap-2 shrink-0">
          <button
            onClick={() => setImportOpen((v) => !v)}
            className="flex items-center gap-1.5 rounded-lg border border-edge px-3 py-1.5 text-sm text-dim hover:text-fg"
          >
            <Download size={14} />
            {t('config.skillsImport')}
          </button>
          <button
            onClick={() => void reloadSkills()}
            className="flex items-center gap-1.5 rounded-lg border border-edge px-3 py-1.5 text-sm text-dim hover:text-fg"
          >
            <RotateCw size={14} />
            {t('config.skillsRefresh')}
          </button>
        </div>
      </div>
      {importOpen && (
        <div className="rounded-xl border border-edge bg-panel2 p-3 space-y-2">
          <p className="text-xs text-dim">{t('config.skillsImportHint')}</p>
          <input
            value={repo}
            onChange={(e) => setRepo(e.target.value)}
            placeholder={t('config.skillsImportRepo')}
            className="w-full rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
          />
          <div className="flex gap-2">
            <select
              value={scope}
              onChange={(e) => setScope(e.target.value)}
              title={t('config.skillsImportScope')}
              className="rounded-lg border border-edge bg-panel px-2 py-1.5 text-sm outline-none"
            >
              <option value="user">{t('config.skillsImportScopeUser')}</option>
              <option value="repo">{t('config.skillsImportScopeRepo')}</option>
            </select>
            <input
              value={subpath}
              onChange={(e) => setSubpath(e.target.value)}
              placeholder={t('config.skillsImportSubpath')}
              className="flex-1 min-w-0 rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
            />
          </div>
          <div className="flex items-center gap-2">
            <button
              onClick={() => void installSkill()}
              disabled={installing}
              className="flex items-center gap-1.5 rounded-lg bg-accent px-4 py-1.5 text-sm text-white hover:opacity-90 disabled:opacity-50"
            >
              {installing && <Loader2 size={14} className="animate-spin" />}
              {t('config.skillsImportRun')}
            </button>
            <button
              onClick={() => setImportOpen(false)}
              className="rounded-lg border border-edge px-3 py-1.5 text-sm text-dim hover:text-fg"
            >
              {t('interact.cancel')}
            </button>
          </div>
        </div>
      )}
      {error && <p className="text-xs text-err">{error}</p>}
      {skills.length === 0 ? (
        <p className="text-sm text-dim">{t('config.skillsEmpty')}</p>
      ) : (
        skills.map((s) => (
          <div
            key={s.name}
            className="rounded-xl border border-edge bg-panel2 p-3"
            title={s.path}
          >
            <div className="flex items-center gap-2">
              <Sparkles size={15} className="text-accent shrink-0" />
              <span className="text-sm font-medium">{s.name}</span>
              <span className="rounded bg-panel border border-edge px-1.5 text-xs text-dim">
                {s.scope}
              </span>
              <span className="flex-1" />
              {s.scope !== 'builtin' && (
                <button
                  onClick={() =>
                    setSkillToDelete({ name: s.name, path: s.path })
                  }
                  className="flex items-center gap-1 rounded-lg border border-edge px-2.5 py-1 text-xs text-dim hover:text-err hover:border-err/40"
                >
                  <Trash2 size={12} />
                  {t('config.skillsDelete')}
                </button>
              )}
            </div>
            {s.description && (
              <p className="text-xs text-dim mt-1">{s.description}</p>
            )}
          </div>
        ))
      )}
      {skillToDelete && (
        <div className="rounded-xl border border-err/40 bg-panel2 p-4">
          <p className="text-sm">
            {t('config.skillsDeleteConfirm', {
              name: skillToDelete.name,
            })}
          </p>
          <div className="mt-3 flex gap-2">
            <button
              onClick={() => setSkillToDelete(null)}
              className="rounded-lg border border-edge px-4 py-1.5 text-sm text-dim hover:text-fg"
            >
              {t('interact.cancel')}
            </button>
            <button
              onClick={() => void deleteSkill(skillToDelete.path)}
              className="rounded-lg bg-err px-4 py-1.5 text-sm text-white hover:opacity-90"
            >
              {t('config.skillsDelete')}
            </button>
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
  { id: 'mcp', icon: MCPLogo, label: (t) => t('config.tabMCP') },
  { id: 'agents', icon: Bot, label: (t) => t('config.tabAgents') },
  { id: 'skills', icon: Sparkles, label: (t) => t('config.tabSkills') },
  { id: 'kanban', icon: Kanban, label: (t) => t('kanban.title') },
];

// ToolsPanel is the right-side page shown when one of the sidebar tool
// buttons (MCP / subagents / skills) is active. It owns the section
// header and delegates the content to the section. Switching between
// sections happens through the left sidebar buttons.
export function ToolsPanel() {
  const view = useStore((s) => s.toolsView);
  const closeTools = useStore((s) => s.closeTools);
  const { t } = useTranslation();

  if (!view) return null;

  const meta = VIEW_META.find((v) => v.id === view);
  const HeaderIcon = meta?.icon ?? MCPLogo;

  return (
    <main className="flex-1 min-w-0 h-full flex flex-col min-h-0 bg-panel">
      <header className="h-11 shrink-0 border-b border-edge flex items-center gap-3 px-4 select-none">
        <HeaderIcon className="h-4 w-4 shrink-0 text-accent" />
        <h2 className="text-sm font-semibold">{meta?.label(t)}</h2>
        <span className="flex-1" />
        <button
          onClick={closeTools}
          className="text-dim hover:text-fg"
          title={t('tools.close')}
        >
          <X size={18} />
        </button>
      </header>
      <div className="flex-1 overflow-y-auto px-5 py-4">
        {view === 'mcp' && <MCPSection />}
        {view === 'agents' && <AgentsSection />}
        {view === 'skills' && <SkillsSection />}
        {view === 'kanban' && <KanbanSection />}
      </div>
    </main>
  );
}
