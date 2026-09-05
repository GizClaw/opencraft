import {
  Bot,
  ChevronDown,
  Clock,
  Download,
  FolderClosed,
  FolderOpen,
  Loader2,
  MessagesSquare,
  MoreHorizontal,
  Pencil,
  Plus,
  Puzzle,
  Settings,
  Sparkles,
  Trash2,
  Upload,
} from 'lucide-react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { WindowToggleMaximise } from '../../wailsjs/runtime/runtime';
import { api } from '../lib/api';
import {
  firstMessageTitle,
  pendingConversationIDs,
  useStore,
} from '../lib/store';
import {
  conversationWorkspace,
  useFocusState,
  useRunningConversations,
} from '../state/react';
import type { ComponentType } from 'react';
import type { SessionMeta, WorkspaceMeta } from '../lib/types';

function basename(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).pop() ?? path;
}

// SessionRow is one rendered session: a running conversation (which may
// not have a history record yet) or a stored session in a workspace
// list. workspacePath is the workspace the row belongs to.
interface SessionRow {
  id: string;
  title: string;
  running: boolean;
  tokens?: string;
  time?: string;
  turns?: number;
  meta?: SessionMeta;
  workspacePath: string;
}

// HistoryItem flattens the workspace/session tree into one ordered list
// the sidebar virtualizer can window, so expanding many workspaces never
// mounts thousands of session rows at once.
type HistoryItem =
  | {
      key: string;
      kind: 'workspace';
      w: WorkspaceMeta;
    }
  | {
      key: string;
      kind: 'session';
      row: SessionRow;
      actionsAllowed: boolean;
    }
  | {
      key: string;
      kind: 'loading';
      workspacePath: string;
    }
  | {
      key: string;
      kind: 'empty';
      workspacePath: string;
    }
  | {
      key: string;
      kind: 'more';
      workspacePath: string;
      count: number;
    };

const expandedStorageKey = 'oc.sidebarExpandedWorkspaces';

function readExpandedWorkspaces(): Set<string> {
  try {
    const raw = window.localStorage.getItem(expandedStorageKey);
    if (!raw) return new Set();
    const parsed = JSON.parse(raw);
    return new Set(Array.isArray(parsed) ? parsed : []);
  } catch {
    return new Set();
  }
}

export function Sidebar({ isMac }: { isMac: boolean }) {
  const workspace = useStore((s) => s.workspace);
  const openDraftChat = useStore((s) => s.openDraftChat);
  const openConfig = useStore((s) => s.openConfig);
  const openWorkspace = useStore((s) => s.openWorkspace);
  const sessions = useStore((s) => s.sessions);
  const conversations = useStore((s) => s.conversations);
  const focus = useFocusState();
  const currentSession = focus.name === 'active' ? focus.sessionID : '';
  const resume = useStore((s) => s.resume);
  const toolsView = useStore((s) => s.toolsView);
  const openTools = useStore((s) => s.openTools);
  const closeTools = useStore((s) => s.closeTools);
  const deleteSession = useStore((s) => s.deleteSession);
  const pendingPromptConvs = useStore((s) => s.pendingPromptConvs);
  const flash = useStore((s) => s.flash);
  const loadSessions = useStore((s) => s.loadSessions);
  const loadWorkspaces = useStore((s) => s.loadWorkspaces);
  const workspaces = useStore((s) => s.workspaces);
  const openSessionInWorkspace = useStore((s) => s.openSessionInWorkspace);
  const removeWorkspace = useStore((s) => s.removeWorkspace);
  const { t, i18n } = useTranslation();

  const [expandedWorkspaces, setExpandedWorkspaces] = useState<Set<string>>(
    readExpandedWorkspaces,
  );
  // wsLists caches one fetched session list per non-active workspace.
  // The active workspace always renders the store's live list instead.
  const [wsLists, setWsLists] = useState<Record<string, SessionMeta[]>>({});
  const [wsLoading, setWsLoading] = useState<Record<string, boolean>>({});
  // showAllSessions keeps one per-workspace flag for expanding past the
  // default 10 most recent sessions inline.
  const [showAllSessions, setShowAllSessions] = useState<Set<string>>(
    () => new Set(),
  );
  // attemptedFetch prevents an infinite refetch loop when a workspace
  // list fails to load; collapse/expand clears the entry to retry.
  const attemptedFetch = useRef<Set<string>>(new Set());
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [renameId, setRenameId] = useState<string | null>(null);
  const [renameValue, setRenameValue] = useState('');
  const [importing, setImporting] = useState(false);
  const [menuOpenId, setMenuOpenId] = useState<string | null>(null);
  const [workspaceInputOpen, setWorkspaceInputOpen] = useState(false);
  const [workspacePath, setWorkspacePath] = useState('');
  const [workspaceError, setWorkspaceError] = useState('');
  const [confirmWorkspace, setConfirmWorkspace] = useState<string | null>(null);
  // hoverCard pins a read-only summary beside a session row. It shows
  // details too wide for the row (tokens, absolute timestamps, full
  // name); pointer events stay off so it never steals the row hover.
  const [hoverCard, setHoverCard] = useState<{
    row: SessionRow;
    left: number;
    top: number;
  } | null>(null);

  const persistExpanded = (next: Set<string>) => {
    setExpandedWorkspaces(next);
    try {
      window.localStorage.setItem(
        expandedStorageKey,
        JSON.stringify([...next]),
      );
    } catch {
      // storage is best-effort; the in-memory tree still works
    }
  };

  // The workspace holding the active session is expanded by default so
  // the current conversation stays reachable after every switch.
  const lastWorkspace = useRef<string | null>(null);
  useEffect(() => {
    if (!workspace || lastWorkspace.current === workspace) return;
    lastWorkspace.current = workspace;
    if (!expandedWorkspaces.has(workspace)) {
      const next = new Set(expandedWorkspaces);
      next.add(workspace);
      persistExpanded(next);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workspace]);

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

  const handleOpenPath = async () => {
    const path = workspacePath.trim();
    if (!path) return;
    setWorkspaceInputOpen(false);
    setWorkspaceError('');
    setWorkspacePath('');
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
    const trim = (s: string) => s.replace(/\.?0+$/, '');
    if (n >= 1_000_000) return `${trim((n / 1_000_000).toFixed(2))}M`;
    if (n >= 1_000) return `${trim((n / 1_000).toFixed(1))}k`;
    return n > 0 ? String(n) : '';
  };

  // Running conversations stay visible under the workspace that owns
  // them even when the app is focused elsewhere (a stream from a
  // previous workspace or an automation host keeps flowing).
  const runningActors = useRunningConversations();
  const runningIdsByWorkspace = useMemo(() => {
    const byPath: Record<string, string[]> = {};
    for (const { conversationID } of runningActors) {
      const path = conversationWorkspace(conversationID);
      if (!path) continue;
      (byPath[path] ??= []).push(conversationID);
    }
    for (const id of pendingConversationIDs(pendingPromptConvs)) {
      const path = conversationWorkspace(id);
      if (!path || byPath[path]?.includes(id)) continue;
      (byPath[path] ??= []).push(id);
    }
    return byPath;
  }, [runningActors, pendingPromptConvs]);

  const ensureWorkspaceSessions = async (w: WorkspaceMeta) => {
    if (
      w.path === workspace ||
      wsLoading[w.path] ||
      wsLists[w.path] ||
      attemptedFetch.current.has(w.path)
    ) {
      return;
    }
    attemptedFetch.current.add(w.path);
    setWsLoading((prev) => ({ ...prev, [w.path]: true }));
    try {
      const list = (await api.listSessionsInWorkspace(w.path)) ?? [];
      setWsLists((prev) => ({ ...prev, [w.path]: list }));
    } catch (err) {
      flash(String(err));
    } finally {
      setWsLoading((prev) => ({ ...prev, [w.path]: false }));
    }
  };

  // Expanded non-active workspaces whose session lists were not loaded
  // yet (for example right after a UI refresh remounts the sidebar)
  // refetch automatically instead of showing an empty history until
  // the user collapses and re-expands the node.
  useEffect(() => {
    const missing = workspaces.filter(
      (w) =>
        expandedWorkspaces.has(w.path) &&
        w.path !== workspace &&
        !wsLists[w.path] &&
        !wsLoading[w.path],
    );
    for (const w of missing) {
      void ensureWorkspaceSessions(w);
    }
    // ensureWorkspaceSessions is recreated per render; its guards read
    // fresh state through the effect's dependencies above.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [expandedWorkspaces, workspace, workspaces, wsLists, wsLoading]);

  const toggleWorkspace = (w: WorkspaceMeta) => {
    const next = new Set(expandedWorkspaces);
    const expanding = !next.has(w.path);
    if (expanding) {
      attemptedFetch.current.delete(w.path);
      next.add(w.path);
      void ensureWorkspaceSessions(w);
    } else {
      next.delete(w.path);
    }
    persistExpanded(next);
  };

  const sessionRowsFor = (w: WorkspaceMeta): SessionRow[] => {
    const stored = w.path === workspace ? sessions : (wsLists[w.path] ?? []);
    const runningIds = runningIdsByWorkspace[w.path] ?? [];
    const running = runningIds.map((id) => {
      const meta =
        w.path === workspace
          ? sessions.find((s) => s.id === id)
          : (wsLists[w.path] ?? []).find((s) => s.id === id);
      const liveTitle = firstMessageTitle(conversations[id]?.messages ?? []);
      return {
        id,
        title:
          meta?.title && meta.title !== '(empty)'
            ? meta.title
            : liveTitle || t('sidebar.newSession'),
        running: true,
        tokens: meta ? fmtTokens(meta.total_tokens) : '',
        time: meta ? fmtTime(meta.updated_at) : '',
        turns: meta?.turns,
        meta,
        workspacePath: w.path,
      };
    });
    const fills = stored
      .filter((s) => !runningIds.includes(s.id))
      .map((s) => ({
        id: s.id,
        title: s.title,
        running: false,
        tokens: fmtTokens(s.total_tokens),
        time: fmtTime(s.updated_at),
        turns: s.turns,
        meta: s,
        workspacePath: w.path,
      }));
    return [...running, ...fills];
  };

  const openSession = (row: SessionRow) => {
    if (row.workspacePath === workspace) {
      void resume(row.id);
    } else {
      void openSessionInWorkspace(row.id, row.workspacePath).catch((err) =>
        flash(String(err)),
      );
    }
  };

  const renameSession = async (id: string, title: string) => {
    try {
      await api.renameSession(id, title);
      await loadSessions();
      setRenameId(null);
    } catch (err) {
      flash(String(err));
      setRenameId(null);
    }
  };

  const fmtCardTime = (iso: string) => {
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return '';
    const locale = i18n.language?.startsWith('zh') ? 'zh-CN' : 'en-US';
    return d.toLocaleString(locale, {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
    });
  };

  const openHoverCard =
    (row: SessionRow) => (e: React.MouseEvent<HTMLDivElement>) => {
      if (!row.meta) {
        setHoverCard(null);
        return;
      }
      const rect = e.currentTarget.getBoundingClientRect();
      const cardWidth = 288;
      const cardHeight = 152;
      const left = Math.max(
        8,
        Math.min(rect.right + 8, window.innerWidth - cardWidth - 8),
      );
      const top = Math.max(
        8,
        Math.min(rect.top - 4, window.innerHeight - cardHeight - 8),
      );
      setHoverCard({ row, left, top });
    };

  // Session meta becomes the second line of each history row: recency
  // and scale are visible without hovering. Running sessions without a
  // persisted record yet fall back to a plain "running" label.
  const renderSessionMeta = (row: SessionRow) => {
    if (row.running && !row.time && !row.turns && !row.tokens) {
      return t('sidebar.running');
    }
    const parts: string[] = [];
    if (row.time) parts.push(row.time);
    if (row.turns && row.turns > 0) {
      parts.push(t('sidebar.turnCount', { count: row.turns }));
    }
    return parts.join(' · ');
  };

  const renderSessionRow = (row: SessionRow, actionsAllowed: boolean) => {
    const isActive = row.id === currentSession;
    const meta = renderSessionMeta(row);
    return (
      <div key={row.id}>
        <div
          className={`group relative rounded-lg text-left ${
            isActive
              ? 'bg-accent/15 border border-accent/40'
              : 'border border-transparent hover:bg-panel2'
          }`}
          data-session-id={row.id}
          onMouseEnter={renameId === row.id ? undefined : openHoverCard(row)}
          onMouseLeave={() => setHoverCard(null)}
        >
          {renameId === row.id ? (
            <div className="flex items-center gap-2 px-1.5 py-1">
              <MessagesSquare size="0.9286rem" className="text-dim shrink-0" />
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
            </div>
          ) : (
            <>
              <button
                onClick={() => openSession(row)}
                className="flex w-full min-w-0 items-center gap-2 px-2 py-1.5"
                aria-label={row.title}
              >
                {row.running ? (
                  <Loader2
                    size="0.9286rem"
                    className="text-accent animate-spin shrink-0"
                  />
                ) : (
                  <MessagesSquare
                    size="0.9286rem"
                    className={`shrink-0 ${
                      isActive ? 'text-accent' : 'text-dim'
                    }`}
                  />
                )}
                <span className="flex min-w-0 flex-1 flex-col">
                  <span className="truncate text-sm leading-5 text-fg">
                    {row.title}
                  </span>
                  <span className="mt-px truncate text-[0.7143rem] leading-4 text-dim">
                    {meta}
                  </span>
                </span>
              </button>
              {actionsAllowed && (
                <div
                  className={`absolute inset-y-0 right-0 flex items-center pr-0.5 transition-opacity ${
                    menuOpenId === row.id
                      ? 'opacity-100'
                      : 'invisible opacity-0 group-hover:visible group-hover:opacity-100 group-focus-within:visible group-focus-within:opacity-100'
                  }`}
                >
                  <button
                    onClick={() => {
                      setHoverCard(null);
                      setMenuOpenId(menuOpenId === row.id ? null : row.id);
                    }}
                    className="rounded-md bg-panel/90 p-1 text-dim hover:bg-panel2 hover:text-fg"
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
                      <div className="absolute right-0 top-full z-30 mt-1 w-44 rounded-lg border border-edge bg-panel py-1 shadow-xl">
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
                              .then((path) =>
                                flash(t('sidebar.exportedTo', { path })),
                              )
                              .catch((err) => flash(String(err)));
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
                              .then((path) =>
                                flash(t('sidebar.exportedTo', { path })),
                              )
                              .catch((err) => flash(String(err)));
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
              )}
            </>
          )}
        </div>
      </div>
    );
  };

  const renderWorkspaceHeader = (w: WorkspaceMeta) => {
    const isCurrent = w.path === workspace;
    const expanded = expandedWorkspaces.has(w.path);
    const FolderIcon = expanded ? FolderOpen : FolderClosed;
    return (
      <div
        role="button"
        tabIndex={0}
        onClick={() => toggleWorkspace(w)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault();
            toggleWorkspace(w);
          }
        }}
        className={`group flex items-center gap-1 rounded-lg px-1.5 py-1 text-left text-sm cursor-pointer select-none ${
          isCurrent
            ? 'bg-accent/10 border border-accent/25'
            : 'border border-transparent hover:bg-panel2'
        }`}
        title={w.path}
      >
        <FolderIcon
          size="0.9286rem"
          className={`shrink-0 ${isCurrent ? 'text-accent' : 'text-dim'}`}
        />
        <span
          className={`flex-1 truncate font-medium ${
            isCurrent ? 'text-fg' : 'text-dim group-hover:text-fg'
          }`}
        >
          {w.title || basename(w.path)}
        </span>
        <button
          onClick={(e) => {
            e.stopPropagation();
            setConfirmWorkspace(w.id);
          }}
          className="text-dim opacity-0 group-hover:opacity-100 hover:text-err shrink-0"
          title={t('sidebar.removeWorkspace')}
          aria-label={t('sidebar.removeWorkspace')}
        >
          <Trash2 size="0.8571rem" />
        </button>
      </div>
    );
  };

  // historyScrollRef + historyItems flatten the tree into one
  // windowed list. Only the workspace rows and sessions near the
  // sidebar viewport are mounted, no matter how many workspaces and
  // sessions are expanded.
  const historyScrollRef = useRef<HTMLDivElement | null>(null);
  const historyItems = useMemo<HistoryItem[]>(() => {
    const items: HistoryItem[] = [];
    for (const w of workspaces) {
      items.push({ key: `ws:${w.id}`, kind: 'workspace', w });
      if (!expandedWorkspaces.has(w.path)) continue;
      const isCurrent = w.path === workspace;
      if (!isCurrent && !wsLists[w.path]) {
        const failed = attemptedFetch.current.has(w.path) && !wsLoading[w.path];
        items.push(
          failed
            ? { key: `empty:${w.path}`, kind: 'empty', workspacePath: w.path }
            : {
                key: `loading:${w.path}`,
                kind: 'loading',
                workspacePath: w.path,
              },
        );
        continue;
      }
      const rows = sessionRowsFor(w);
      const runningCount = runningIdsByWorkspace[w.path]?.length ?? 0;
      const storedCount = Math.max(0, rows.length - runningCount);
      const visibleStored = showAllSessions.has(w.path)
        ? storedCount
        : Math.min(storedCount, 10);
      const visible = rows.slice(0, runningCount + visibleStored);
      if (visible.length === 0) {
        items.push({
          key: `empty:${w.path}`,
          kind: 'empty',
          workspacePath: w.path,
        });
        continue;
      }
      for (const row of visible) {
        items.push({
          key: `${w.path}:${row.id}`,
          kind: 'session',
          row,
          actionsAllowed: isCurrent,
        });
      }
      const hidden = storedCount - visibleStored;
      if (hidden > 0) {
        items.push({
          key: `more:${w.path}`,
          kind: 'more',
          workspacePath: w.path,
          count: hidden,
        });
      }
    }
    return items;
    // sessionRowsFor is a local render helper; its inputs are all in
    // the dependency list above.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    expandedWorkspaces,
    runningIdsByWorkspace,
    sessions,
    showAllSessions,
    t,
    workspace,
    workspaces,
    wsLists,
    wsLoading,
    conversations,
  ]);
  const virtualizer = useVirtualizer({
    count: historyItems.length,
    getScrollElement: () => historyScrollRef.current,
    estimateSize: () => 44,
    overscan: 10,
    getItemKey: (index) => historyItems[index]?.key ?? String(index),
  });

  const expandWorkspaceSessions = (workspacePath: string) => {
    setShowAllSessions((prev) => {
      const next = new Set(prev);
      next.add(workspacePath);
      return next;
    });
  };

  const renderHistoryItem = (item: HistoryItem) => {
    switch (item.kind) {
      case 'workspace':
        return renderWorkspaceHeader(item.w);
      case 'session':
        return (
          <div className="ml-3 pt-1">
            {renderSessionRow(item.row, item.actionsAllowed)}
          </div>
        );
      case 'loading':
        return (
          <div className="ml-3 pt-1">
            <div className="h-9 animate-pulse rounded-lg bg-panel2" />
          </div>
        );
      case 'empty':
        return (
          <p className="ml-3 pt-1 pb-0.5 text-xs text-dim">
            {t('sidebar.noSessions')}
          </p>
        );
      case 'more':
        return (
          <div className="ml-3 pt-1">
            <button
              onClick={() => expandWorkspaceSessions(item.workspacePath)}
              className="flex w-full items-center gap-1 rounded-md px-1.5 py-1 text-xs text-dim hover:bg-panel2 hover:text-fg transition-colors"
            >
              <ChevronDown size="0.8571rem" className="shrink-0" />
              {t('sidebar.moreSessions')}
              <span className="ml-auto tabular-nums text-[0.7143rem] text-dim/80">
                +{item.count}
              </span>
            </button>
          </div>
        );
    }
  };

  const removingWorkspace = workspaces.find((w) => w.id === confirmWorkspace);

  const confirmRemoveWorkspace = () => {
    if (!removingWorkspace) return;
    const target = removingWorkspace;
    setWsLists((prev) => {
      const next = { ...prev };
      delete next[target.path];
      return next;
    });
    setWsLoading((prev) => {
      const next = { ...prev };
      delete next[target.path];
      return next;
    });
    attemptedFetch.current.delete(target.path);
    const next = new Set(expandedWorkspaces);
    next.delete(target.path);
    persistExpanded(next);
    setConfirmWorkspace(null);
    void removeWorkspace(target.id);
  };

  const renderHoverCard = () => {
    const card = hoverCard;
    if (!card) return null;
    const meta = card.row.meta;
    const updated = meta ? fmtCardTime(meta.updated_at) : '';
    const created = meta ? fmtCardTime(meta.created_at) : '';
    const rows = [
      card.row.tokens
        ? { label: t('sidebar.hoverTokens'), value: card.row.tokens }
        : null,
      updated ? { label: t('sidebar.hoverUpdated'), value: updated } : null,
      created ? { label: t('sidebar.hoverCreated'), value: created } : null,
    ].filter((r): r is { label: string; value: string } => r !== null);
    if (rows.length === 0) return null;
    return (
      <div
        className="pointer-events-none fixed z-[70] rounded-lg border border-edge bg-panel2/95 p-3 shadow-xl backdrop-blur"
        style={{ left: card.left, top: card.top, width: 288 }}
      >
        <p className="break-words text-sm font-medium leading-5 text-fg line-clamp-3">
          {card.row.title}
        </p>
        <dl className="mt-2 flex flex-col gap-1">
          {rows.map((r) => (
            <div
              key={r.label}
              className="flex items-baseline justify-between gap-4 text-xs leading-4"
            >
              <dt className="shrink-0 text-dim">{r.label}</dt>
              <dd className="min-w-0 text-right text-fg">{r.value}</dd>
            </div>
          ))}
        </dl>
      </div>
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
          onClick={openDraftChat}
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

      <div className="px-3 pt-4">
        <div className="flex items-center justify-between mb-2">
          <h3 className="text-xs uppercase tracking-wider text-dim">
            {t('sidebar.history')}
          </h3>
          <div className="flex items-center gap-1.5">
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
            <button
              onClick={() => void handleAddWorkspace()}
              className="text-dim hover:text-fg"
              title={t('sidebar.addWorkspace')}
              aria-label={t('sidebar.addWorkspace')}
            >
              <Plus size="0.9286rem" />
            </button>
          </div>
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
      </div>
      <div ref={historyScrollRef} className="flex-1 overflow-y-auto px-3 pb-4">
        {workspaces.length === 0 ? (
          <p className="pt-2 text-xs text-dim">
            {workspace ? '—' : t('sidebar.workspaceEmpty')}
          </p>
        ) : (
          <div
            className="relative"
            style={{ height: `${virtualizer.getTotalSize()}px` }}
          >
            {virtualizer.getVirtualItems().map((virtualItem) => {
              const item = historyItems[virtualItem.index];
              return (
                <div
                  key={item.key}
                  data-index={virtualItem.index}
                  ref={virtualizer.measureElement}
                  className="absolute left-0 right-0"
                  style={{ top: virtualItem.start }}
                >
                  {renderHistoryItem(item)}
                </div>
              );
            })}
          </div>
        )}
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
        <div
          className="fixed bottom-0 top-11 left-0 right-0 z-50 grid place-items-center bg-black/60 p-6"
          onClick={() => setConfirmDelete(null)}
        >
          <div
            className="w-[26.0000rem] rounded-2xl border border-edge bg-panel p-5 shadow-2xl"
            onClick={(e) => e.stopPropagation()}
          >
            <h3 className="text-base font-semibold">
              {t('sidebar.deleteSessionTitle', {
                title:
                  sessions.find((s) => s.id === confirmDelete)?.title ?? '',
              })}
            </h3>
            <p className="mt-2 text-sm leading-relaxed text-dim">
              {t('sidebar.deleteSessionBody')}
            </p>
            <div className="mt-4 flex justify-end gap-2">
              <button
                onClick={() => setConfirmDelete(null)}
                className="rounded-lg border border-edge px-3 py-1.5 text-sm text-dim hover:text-fg"
              >
                {t('interact.cancel')}
              </button>
              <button
                onClick={() => {
                  void deleteSession(confirmDelete);
                  setConfirmDelete(null);
                }}
                className="rounded-lg bg-err px-3 py-1.5 text-sm text-white hover:opacity-90"
              >
                {t('sidebar.deleteSession')}
              </button>
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
            <h3 className="text-base font-semibold">
              {t('sidebar.removeWorkspaceTitle', {
                title: removingWorkspace.title,
              })}
            </h3>
            <p className="mt-2 text-sm leading-relaxed text-dim">
              {t('sidebar.removeWorkspaceBody')}
            </p>
            <div className="mt-4 flex justify-end gap-2">
              <button
                onClick={() => setConfirmWorkspace(null)}
                className="rounded border border-edge px-3 py-1.5 text-sm text-dim hover:text-fg"
              >
                {t('interact.cancel')}
              </button>
              <button
                onClick={confirmRemoveWorkspace}
                className="rounded bg-err px-3 py-1.5 text-sm text-white hover:opacity-90"
              >
                {t('sidebar.removeWorkspace')}
              </button>
            </div>
          </div>
        </div>
      )}
      {renderHoverCard()}
    </aside>
  );
}
