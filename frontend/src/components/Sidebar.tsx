import {
  Bot,
  Clock,
  Download,
  FolderOpen,
  Loader2,
  MessageSquare,
  MoreHorizontal,
  Pencil,
  Plus,
  Puzzle,
  Search,
  Settings,
  Sparkles,
  Trash2,
  Upload,
  X,
} from 'lucide-react';
import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { WindowToggleMaximise } from '../../wailsjs/runtime/runtime';
import { api } from '../lib/api';
import { displaySessionID, isTurnBusy, useStore } from '../lib/store';
import type { SessionMeta } from '../lib/types';
import type { ComponentType } from 'react';

function basename(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).pop() ?? path;
}

// SessionRow is one rendered session: a running conversation (which may
// not have a history record yet) or a stored session filling the list.
interface SessionRow {
  id: string;
  title: string;
  running: boolean;
  tokens?: string;
  time?: string;
  meta?: SessionMeta;
}

export function Sidebar({ isMac }: { isMac: boolean }) {
  const workspace = useStore((s) => s.workspace);
  const newChat = useStore((s) => s.newChat);
  const openConfig = useStore((s) => s.openConfig);
  const sessions = useStore((s) => s.sessions);
  const sessionsLoading = useStore((s) => s.sessionsLoading);
  const currentSession = useStore((s) => displaySessionID(s.navigation));
  const resume = useStore((s) => s.resume);
  const toolsView = useStore((s) => s.toolsView);
  const openTools = useStore((s) => s.openTools);
  const closeTools = useStore((s) => s.closeTools);
  const deleteSession = useStore((s) => s.deleteSession);
  const conversations = useStore((s) => s.conversations);
  const flash = useStore((s) => s.flash);
  const loadSessions = useStore((s) => s.loadSessions);
  const loadWorkspaces = useStore((s) => s.loadWorkspaces);
  const workspaces = useStore((s) => s.workspaces);
  const openWorkspace = useStore((s) => s.openWorkspace);
  const removeWorkspace = useStore((s) => s.removeWorkspace);
  const { t } = useTranslation();

  const [sessionsOpen, setSessionsOpen] = useState(false);
  const [workspacesOpen, setWorkspacesOpen] = useState(false);
  const [sessionQuery, setSessionQuery] = useState('');
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [renameId, setRenameId] = useState<string | null>(null);
  const [renameValue, setRenameValue] = useState('');
  const [importing, setImporting] = useState(false);
  const [menuOpenId, setMenuOpenId] = useState<string | null>(null);
  const [workspaceInputOpen, setWorkspaceInputOpen] = useState(false);
  const [workspacePath, setWorkspacePath] = useState('');
  const [workspaceError, setWorkspaceError] = useState('');
  const [confirmWorkspace, setConfirmWorkspace] = useState<string | null>(null);

  const toolButtons: {
    id: string;
    label: string;
    icon: ComponentType<{ className?: string }>;
    active: boolean;
    onClick: () => void;
  }[] = [
    {
      id: 'agents',
      label: t('sidebar.subagents'),
      icon: Bot,
      active: toolsView === 'agents',
      onClick: () =>
        toolsView === 'agents' ? closeTools() : openTools('agents'),
    },
    {
      id: 'skills',
      label: t('sidebar.skills'),
      icon: Sparkles,
      active: toolsView === 'skills',
      onClick: () =>
        toolsView === 'skills' ? closeTools() : openTools('skills'),
    },
    {
      id: 'plugins',
      label: t('config.tabPlugins'),
      icon: Puzzle,
      active: toolsView === 'plugins',
      onClick: () =>
        toolsView === 'plugins' ? closeTools() : openTools('plugins'),
    },
    {
      id: 'automations',
      label: t('sidebar.automations'),
      icon: Clock,
      active: toolsView === 'automations',
      onClick: () =>
        toolsView === 'automations' ? closeTools() : openTools('automations'),
    },
  ];

  // The native folder picker can fail or never return in some
  // environments (e.g. wails dev running the bare binary). When it
  // errors, fall back to a typed path so adding a workspace always
  // works.
  const handleAddWorkspace = async () => {
    let path = '';
    try {
      path = (await api.chooseWorkspace()) ?? '';
    } catch (err) {
      setWorkspaceError(String(err));
      setWorkspaceInputOpen(true);
      return;
    }
    if (!path) return; // cancelled
    setWorkspaceError('');
    await openWorkspace(path);
    await loadWorkspaces();
  };

  const handleImportSession = async () => {
    let path = '';
    try {
      path =
        (await api.pickFile(t('sidebar.importSessionTitle'), '*.json')) ?? '';
    } catch (err) {
      flash(t('sidebar.importFailed', { error: String(err) }));
      return;
    }
    if (!path) return; // cancelled
    setImporting(true);
    try {
      const imported = await api.importSession(path);
      await loadSessions();
      if (imported.session_id) {
        await resume(imported.session_id);
      }
      flash(t('sidebar.importedSession'));
    } catch (err) {
      flash(t('sidebar.importFailed', { error: String(err) }));
    } finally {
      setImporting(false);
    }
  };

  const handleOpenPath = async () => {
    const path = workspacePath.trim();
    if (!path) return;
    setWorkspaceInputOpen(false);
    setWorkspaceError('');
    setWorkspacePath('');
    await openWorkspace(path);
    await loadWorkspaces();
  };

  const fmtTime = (iso: string) => {
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return '';
    const now = Date.now();
    const diff = now - d.getTime();
    if (diff < 60_000) return t('sidebar.justNow');
    if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m`;
    if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h`;
    return d.toLocaleDateString();
  };

  const fmtTokens = (n: number) => {
    if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`;
    if (n >= 1_000) return `${(n / 1_000).toFixed(2)}k`;
    return n > 0 ? String(n) : '';
  };

  // Running conversations always stay visible; stored sessions fill the
  // list up to five entries total.
  const runningIds = useMemo(
    () =>
      Object.entries(conversations)
        .filter(([, c]) => isTurnBusy(c.turn) || c.pendingInteracts.length > 0)
        .map(([id]) => id),
    [conversations],
  );
  const visibleSessions = useMemo<SessionRow[]>(() => {
    const running = runningIds.map((id) => {
      const meta = sessions.find((s) => s.id === id);
      return {
        id,
        title:
          meta?.title && meta.title !== '(empty)'
            ? meta.title
            : t('sidebar.newSession'),
        running: true,
        tokens: meta ? fmtTokens(meta.total_tokens) : '',
        time: meta ? fmtTime(meta.updated_at) : '',
        meta,
      };
    });
    const fill = sessions
      .filter((s) => !runningIds.includes(s.id))
      .slice(0, Math.max(0, 5 - running.length));
    return [
      ...running,
      ...fill.map((s) => ({
        id: s.id,
        title: s.title,
        running: false,
        tokens: fmtTokens(s.total_tokens),
        time: fmtTime(s.updated_at),
        meta: s,
      })),
    ];
  }, [runningIds, sessions, t]);

  const filteredSessions = useMemo(
    () =>
      sessions.filter((s) =>
        s.title.toLowerCase().includes(sessionQuery.toLowerCase()),
      ),
    [sessions, sessionQuery],
  );
  const visibleWorkspaces = workspaces.slice(0, 5);
  const removingWorkspace = workspaces.find((w) => w.id === confirmWorkspace);
  const runningOnly = runningIds.filter(
    (id) => !sessions.some((s) => s.id === id),
  ).length;
  const showMoreSessions = sessions.length + runningOnly > 5;

  const renameSession = (id: string, title: string) => {
    void api
      .renameSession(id, title)
      .then(loadSessions)
      .then(() => setRenameId(null));
  };

  const renderSessionRow = (row: SessionRow) => (
    <li key={row.id}>
      <div
        className={`group relative flex items-center gap-1 rounded-lg px-1.5 py-1 text-left text-sm ${
          row.id === currentSession
            ? 'bg-accent/15 border border-accent/40'
            : 'border border-transparent hover:bg-panel2'
        }`}
        title={row.id}
      >
        <button
          onClick={() => void resume(row.id)}
          className="flex flex-1 min-w-0 items-center gap-2"
        >
          {row.running ? (
            <Loader2
              size="0.9286rem"
              className="text-accent animate-spin shrink-0"
            />
          ) : (
            <MessageSquare size="0.9286rem" className="text-dim shrink-0" />
          )}
          {renameId === row.id ? (
            <input
              autoFocus
              value={renameValue}
              onChange={(e) => setRenameValue(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  renameSession(row.id, renameValue);
                } else if (e.key === 'Escape') {
                  setRenameId(null);
                }
              }}
              onBlur={() => setRenameId(null)}
              className="flex-1 min-w-0 rounded border border-accent bg-panel px-1 py-0 text-xs outline-none"
            />
          ) : (
            <span className="flex-1 truncate">{row.title}</span>
          )}
        </button>
        <div className="relative shrink-0">
          <button
            onClick={() => setMenuOpenId(menuOpenId === row.id ? null : row.id)}
            className="text-dim opacity-0 group-hover:opacity-100 hover:text-fg rounded p-0.5"
            title={t('sidebar.sessionActions')}
            aria-label={t('sidebar.sessionActions')}
          >
            <MoreHorizontal size="1.0000rem" />
          </button>
          {menuOpenId === row.id && (
            <>
              <div
                className="fixed inset-0 z-20"
                onClick={() => setMenuOpenId(null)}
              />
              <div className="absolute right-0 top-6 z-30 w-44 rounded-lg border border-edge bg-panel shadow-xl py-1">
                <button
                  onClick={() => {
                    setMenuOpenId(null);
                    setRenameId(row.id);
                    setRenameValue(row.meta?.title ?? row.title);
                  }}
                  className="w-full flex items-center gap-2 px-3 py-1.5 text-left text-xs hover:bg-panel2"
                >
                  <Pencil size="0.8571rem" className="text-dim" />
                  {t('sidebar.renameSession')}
                </button>
                <button
                  onClick={() => {
                    setMenuOpenId(null);
                    void api
                      .exportSession(row.id)
                      .then((path) => flash(t('sidebar.exportedTo', { path })));
                  }}
                  className="w-full flex items-center gap-2 px-3 py-1.5 text-left text-xs hover:bg-panel2"
                >
                  <Download size="0.8571rem" className="text-dim" />
                  {t('sidebar.exportSession')}
                </button>
                <button
                  onClick={() => {
                    setMenuOpenId(null);
                    void api
                      .exportSessionBundle(row.id)
                      .then((path) => flash(t('sidebar.exportedTo', { path })));
                  }}
                  className="w-full flex items-center gap-2 px-3 py-1.5 text-left text-xs hover:bg-panel2"
                >
                  <Download size="0.8571rem" className="text-dim" />
                  {t('sidebar.exportSessionBundle')}
                </button>
                <button
                  onClick={() => {
                    setMenuOpenId(null);
                    setConfirmDelete(row.id);
                  }}
                  className="w-full flex items-center gap-2 px-3 py-1.5 text-left text-xs text-err hover:bg-panel2"
                >
                  <Trash2 size="0.8571rem" className="text-err" />
                  {t('sidebar.deleteSession')}
                </button>
              </div>
            </>
          )}
        </div>
        {(row.tokens || row.time) && (
          <div className="pointer-events-none absolute left-1/2 -translate-x-1/2 bottom-full mb-1.5 z-10 hidden group-hover:block whitespace-nowrap rounded-md border border-edge bg-panel2 px-2 py-1 text-[0.7143rem] text-dim shadow-lg">
            {[row.tokens, row.time].filter(Boolean).join(' · ')}
          </div>
        )}
      </div>
    </li>
  );

  const renderWorkspaceRow = (w: {
    id: string;
    title: string;
    path: string;
  }) => {
    const active = w.path === workspace;
    return (
      <li key={w.id}>
        <div
          className={`group flex items-center gap-1 rounded-lg px-1.5 py-1 text-left text-sm ${
            active
              ? 'bg-accent/15 border border-accent/40'
              : 'border border-transparent hover:bg-panel2'
          }`}
          title={w.path}
        >
          <button
            onClick={() => void openWorkspace(w.path)}
            className="flex flex-1 min-w-0 items-center gap-2"
          >
            <FolderOpen size="0.9286rem" className="text-dim shrink-0" />
            <span className="flex-1 truncate">{w.title}</span>
          </button>
          <button
            onClick={() => setConfirmWorkspace(w.id)}
            className="text-dim opacity-0 group-hover:opacity-100 hover:text-err shrink-0"
            title={t('sidebar.removeWorkspace')}
            aria-label={t('sidebar.removeWorkspace')}
          >
            <Trash2 size="0.8571rem" />
          </button>
        </div>
      </li>
    );
  };

  return (
    <aside className="h-full border-r border-edge bg-panel flex flex-col min-h-0">
      {/* macOS: the native traffic lights float here, so the strip
          leaves them room and doubles as the window drag area; its
          height matches the chat header (h-11) so both rows align.
          Windows/Linux use the full-width TopBar instead. */}
      {isMac && (
        <div
          className="h-11 shrink-0 border-b border-edge flex items-center select-none pl-[5.5714rem]"
          style={{ ['--wails-draggable' as string]: 'drag' }}
          onDoubleClick={() => WindowToggleMaximise()}
        />
      )}

      <div className="px-4 pt-3 pb-2 select-none">
        <div className="font-mono text-base leading-tight text-fg font-semibold">
          Open
        </div>
        <div className="font-mono text-base leading-tight text-accent">
          Craft&gt;_
        </div>
      </div>

      <div className="px-3 pt-1">
        <button
          onClick={newChat}
          className="w-full flex items-center gap-2 rounded-lg border border-edge bg-panel2 px-3 py-2 text-sm hover:border-accent/50 transition-colors"
        >
          <Plus size="1.0714rem" className="text-accent" />
          {t('sidebar.newChat')}
        </button>
      </div>

      <div className="px-3 pt-2 space-y-1">
        {toolButtons.map((btn) => {
          const Icon = btn.icon;
          return (
            <button
              key={btn.id}
              onClick={btn.onClick}
              className={`w-full flex items-center gap-2 rounded-lg px-3 py-1.5 text-sm text-left transition-colors ${
                btn.active
                  ? 'bg-accent/15 border border-accent/40'
                  : 'border border-transparent text-dim hover:text-fg hover:bg-panel2'
              }`}
            >
              <Icon className="h-4 w-4 shrink-0 text-accent" />
              {btn.label}
            </button>
          );
        })}
      </div>

      <div className="flex-1 overflow-y-auto px-3 py-4 space-y-4">
        <section>
          <div className="flex items-center justify-between mb-2">
            <h3 className="text-xs uppercase tracking-wider text-dim">
              {t('sidebar.sessions')}
            </h3>
            <button
              onClick={() => void handleImportSession()}
              disabled={!workspace || importing}
              className="text-dim hover:text-fg disabled:text-dim/40 disabled:hover:text-dim/40"
              title={t('sidebar.importSession')}
              aria-label={t('sidebar.importSession')}
            >
              {importing ? (
                <Loader2 size="0.9286rem" className="animate-spin" />
              ) : (
                <Upload size="0.9286rem" />
              )}
            </button>
          </div>
          {visibleSessions.length === 0 ? (
            <p className="text-xs text-dim">—</p>
          ) : (
            <ul className="space-y-1">
              {visibleSessions.map(renderSessionRow)}
            </ul>
          )}
          {showMoreSessions && (
            <button
              onClick={() => setSessionsOpen(true)}
              className="w-full mt-2 flex items-center gap-2 rounded-lg border border-edge bg-panel2 px-3 py-2 text-sm hover:border-accent/50 transition-colors"
            >
              <Search size="1.0000rem" className="text-dim" />
              {t('sidebar.moreSessions')}
            </button>
          )}
        </section>

        <section>
          <div className="flex items-center justify-between mb-2">
            <h3 className="text-xs uppercase tracking-wider text-dim">
              {t('sidebar.workspaces')}
            </h3>
            <button
              onClick={() => void handleAddWorkspace()}
              className="text-dim hover:text-fg"
              title={t('sidebar.addWorkspace')}
              aria-label={t('sidebar.addWorkspace')}
            >
              <Plus size="0.9286rem" />
            </button>
          </div>
          {workspaceInputOpen && (
            <div className="mb-2 flex flex-col gap-1.5 rounded-lg border border-edge bg-panel2 p-2">
              {workspaceError && (
                <p className="text-[0.7857rem] text-red-400 break-words">
                  {workspaceError}
                </p>
              )}
              <p className="text-[0.7857rem] text-dim">
                {t('sidebar.pickerFallback')}
              </p>
              <input
                value={workspacePath}
                onChange={(e) => setWorkspacePath(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') void handleOpenPath();
                  if (e.key === 'Escape') setWorkspaceInputOpen(false);
                }}
                placeholder="/path/to/workspace"
                className="w-full rounded-md border border-edge bg-panel px-2 py-1 text-xs text-fg outline-none focus:border-accent"
                autoFocus
              />
              <div className="flex gap-2">
                <button
                  onClick={() => void handleOpenPath()}
                  className="rounded-md bg-accent px-2 py-1 text-xs text-white hover:opacity-90"
                >
                  {t('sidebar.open')}
                </button>
                <button
                  onClick={() => setWorkspaceInputOpen(false)}
                  className="rounded-md px-2 py-1 text-xs text-dim hover:text-fg"
                >
                  {t('sidebar.cancel')}
                </button>
              </div>
            </div>
          )}
          {workspaces.length === 0 ? (
            <p className="text-xs text-dim">
              {workspace ? '—' : t('sidebar.workspaceEmpty')}
            </p>
          ) : (
            <>
              <ul className="space-y-1">
                {visibleWorkspaces.map(renderWorkspaceRow)}
              </ul>
              {workspaces.length > 5 && (
                <button
                  onClick={() => setWorkspacesOpen(true)}
                  className="w-full mt-2 flex items-center gap-2 rounded-lg border border-edge bg-panel2 px-3 py-2 text-sm hover:border-accent/50 transition-colors"
                >
                  <FolderOpen size="1.0000rem" className="text-dim" />
                  {t('sidebar.moreWorkspaces')}
                </button>
              )}
            </>
          )}
        </section>
      </div>

      <div className="border-t border-edge p-3 space-y-2">
        <button
          onClick={() => openConfig()}
          className="flex items-center gap-1.5 text-xs text-dim hover:text-fg"
        >
          <Settings size="0.9286rem" />
          {t('sidebar.settings')}
        </button>
      </div>

      {confirmDelete && (
        <div className="mt-2 mx-3 rounded-lg border border-err/40 bg-panel2 p-2 text-xs">
          <p>
            {t('sidebar.deleteSessionConfirm', {
              title: sessions.find((s) => s.id === confirmDelete)?.title ?? '',
            })}
          </p>
          <div className="mt-2 flex gap-2">
            <button
              onClick={() => setConfirmDelete(null)}
              className="rounded border border-edge px-2 py-0.5 text-dim hover:text-fg"
            >
              {t('interact.cancel')}
            </button>
            <button
              onClick={() => {
                void deleteSession(confirmDelete);
                setConfirmDelete(null);
              }}
              className="rounded bg-err px-2 py-0.5 text-white hover:opacity-90"
            >
              {t('sidebar.deleteSession')}
            </button>
          </div>
        </div>
      )}

      {sessionsOpen && (
        <div
          className="fixed bottom-0 top-11 left-0 right-0 z-40 grid place-items-center bg-black/60 p-6"
          onClick={() => setSessionsOpen(false)}
        >
          <div
            className="w-[40.0000rem] max-h-[80vh] flex flex-col rounded-2xl border border-edge bg-panel shadow-2xl"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="flex items-center gap-2 px-4 py-3 border-b border-edge">
              <Search size="1.0000rem" className="text-dim shrink-0" />
              <input
                autoFocus
                value={sessionQuery}
                onChange={(e) => setSessionQuery(e.target.value)}
                placeholder={t('sidebar.searchSessions')}
                className="flex-1 min-w-0 bg-transparent outline-none text-sm"
              />
              <button
                onClick={() => setSessionsOpen(false)}
                className="text-dim hover:text-fg shrink-0"
              >
                <X size="1.1429rem" />
              </button>
            </div>
            <div className="flex-1 overflow-y-auto p-2">
              {filteredSessions.length === 0 ? (
                sessionsLoading ? (
                  <div className="space-y-2 p-2">
                    {[0, 1, 2].map((i) => (
                      <div
                        key={i}
                        className="h-8 animate-pulse rounded-lg bg-panel2"
                      />
                    ))}
                  </div>
                ) : (
                  <p className="text-xs text-dim p-3">—</p>
                )
              ) : (
                <ul className="space-y-1">
                  {filteredSessions.map((s) =>
                    renderSessionRow({
                      id: s.id,
                      title: s.title,
                      running: runningIds.includes(s.id),
                      tokens: fmtTokens(s.total_tokens),
                      time: fmtTime(s.updated_at),
                      meta: s,
                    }),
                  )}
                </ul>
              )}
            </div>
          </div>
        </div>
      )}

      {workspacesOpen && (
        <div
          className="fixed bottom-0 top-11 left-0 right-0 z-40 grid place-items-center bg-black/60 p-6"
          onClick={() => setWorkspacesOpen(false)}
        >
          <div
            className="w-[30.0000rem] max-h-[70vh] flex flex-col rounded-2xl border border-edge bg-panel shadow-2xl"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="flex items-center justify-between px-4 py-3 border-b border-edge">
              <h3 className="text-sm font-semibold">
                {t('sidebar.workspaces')}
              </h3>
              <button
                onClick={() => setWorkspacesOpen(false)}
                className="text-dim hover:text-fg"
              >
                <X size="1.1429rem" />
              </button>
            </div>
            <div className="flex-1 overflow-y-auto p-2">
              {workspaces.length === 0 ? (
                <p className="text-xs text-dim p-3">
                  {workspace ? '—' : t('sidebar.workspaceEmpty')}
                </p>
              ) : (
                <ul className="space-y-1">
                  {workspaces.map(renderWorkspaceRow)}
                </ul>
              )}
            </div>
          </div>
        </div>
      )}

      {confirmWorkspace && removingWorkspace && (
        <div
          className="fixed bottom-0 top-11 left-0 right-0 z-50 grid place-items-center bg-black/60 p-6"
          onClick={() => setConfirmWorkspace(null)}
        >
          <div
            className="w-[26.0000rem] rounded-2xl border border-edge bg-panel p-5 shadow-2xl"
            onClick={(e) => e.stopPropagation()}
          >
            <p className="text-sm">
              {t(
                removingWorkspace.path === workspace
                  ? 'sidebar.removeWorkspaceConfirmActive'
                  : 'sidebar.removeWorkspaceConfirm',
                { title: removingWorkspace.title },
              )}
            </p>
            <div className="mt-4 flex justify-end gap-2">
              <button
                onClick={() => setConfirmWorkspace(null)}
                className="rounded border border-edge px-3 py-1.5 text-sm text-dim hover:text-fg"
              >
                {t('interact.cancel')}
              </button>
              <button
                onClick={() => {
                  const id = confirmWorkspace;
                  setConfirmWorkspace(null);
                  setWorkspacesOpen(false);
                  void removeWorkspace(id);
                }}
                className="rounded bg-err px-3 py-1.5 text-sm text-white hover:opacity-90"
              >
                {t('sidebar.removeWorkspace')}
              </button>
            </div>
          </div>
        </div>
      )}
    </aside>
  );
}
