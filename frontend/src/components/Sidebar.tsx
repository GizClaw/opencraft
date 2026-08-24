import {
  Download,
  FolderOpen,
  Kanban,
  Loader2,
  MessageSquare,
  MoreHorizontal,
  Pencil,
  Plus,
  Search,
  Settings,
  Trash2,
  X,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Environment, WindowToggleMaximise } from "../../wailsjs/runtime/runtime";
import { api } from "../lib/api";
import { useStore } from "../lib/store";
import type { SessionMeta } from "../lib/types";

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

export function Sidebar() {
  const workspace = useStore((s) => s.workspace);
  const newChat = useStore((s) => s.newChat);
  const openConfig = useStore((s) => s.openConfig);
  const sessions = useStore((s) => s.sessions);
  const currentSession = useStore((s) => s.current);
  const resume = useStore((s) => s.resume);
  const openKanban = useStore((s) => s.openKanban);
  const deleteSession = useStore((s) => s.deleteSession);
  const conversations = useStore((s) => s.conversations);
  const flash = useStore((s) => s.flash);
  const loadSessions = useStore((s) => s.loadSessions);
  const workspaces = useStore((s) => s.workspaces);
  const openWorkspace = useStore((s) => s.openWorkspace);
  const removeWorkspace = useStore((s) => s.removeWorkspace);
  const { t } = useTranslation();

  const [isMac, setIsMac] = useState(false);
  const [sessionsOpen, setSessionsOpen] = useState(false);
  const [workspacesOpen, setWorkspacesOpen] = useState(false);
  const [sessionQuery, setSessionQuery] = useState("");
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [renameId, setRenameId] = useState<string | null>(null);
  const [renameValue, setRenameValue] = useState("");
  const [menuOpenId, setMenuOpenId] = useState<string | null>(null);

  useEffect(() => {
    void Environment().then((env) => setIsMac(env.platform === "darwin"));
  }, []);

  const fmtTime = (iso: string) => {
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return "";
    const now = Date.now();
    const diff = now - d.getTime();
    if (diff < 60_000) return "刚刚";
    if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m`;
    if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h`;
    return d.toLocaleDateString();
  };

  const fmtTokens = (n: number) => {
    if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`;
    if (n >= 1_000) return `${(n / 1_000).toFixed(2)}k`;
    return n > 0 ? String(n) : "";
  };

  // Running conversations always stay visible; stored sessions fill the
  // list up to five entries total.
  const runningIds = useMemo(
    () =>
      Object.entries(conversations)
        .filter(([, c]) => c.busy || c.pendingInteracts.length > 0)
        .map(([id]) => id),
    [conversations],
  );
  const visibleSessions = useMemo<SessionRow[]>(() => {
    const running = runningIds.map((id) => {
      const meta = sessions.find((s) => s.id === id);
      return {
        id,
        title: meta?.title && meta.title !== "(empty)" ? meta.title : t("sidebar.newSession"),
        running: true,
        tokens: meta ? fmtTokens(meta.total_tokens) : "",
        time: meta ? fmtTime(meta.updated_at) : "",
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
            ? "bg-accent/15 border border-accent/40"
            : "border border-transparent hover:bg-panel2"
        }`}
        title={row.id}
      >
        <button
          onClick={() => void resume(row.id)}
          className="flex flex-1 min-w-0 items-center gap-2"
        >
          <MessageSquare size={13} className="text-dim shrink-0" />
          {renameId === row.id ? (
            <input
              autoFocus
              value={renameValue}
              onChange={(e) => setRenameValue(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  renameSession(row.id, renameValue);
                } else if (e.key === "Escape") {
                  setRenameId(null);
                }
              }}
              onBlur={() => setRenameId(null)}
              className="flex-1 min-w-0 rounded border border-accent bg-panel px-1 py-0 text-xs outline-none"
            />
          ) : (
            <span className="flex-1 truncate">{row.title}</span>
          )}
          {row.running && (
            <Loader2 size={12} className="text-accent animate-spin shrink-0" />
          )}
        </button>
        <div className="relative shrink-0">
          <button
            onClick={() =>
              setMenuOpenId(menuOpenId === row.id ? null : row.id)
            }
            className="text-dim opacity-0 group-hover:opacity-100 hover:text-fg rounded p-0.5"
            title={t("sidebar.sessionActions")}
          >
            <MoreHorizontal size={14} />
          </button>
          {menuOpenId === row.id && (
            <>
              <div
                className="fixed inset-0 z-20"
                onClick={() => setMenuOpenId(null)}
              />
              <div className="absolute right-0 top-6 z-30 min-w-32 rounded-lg border border-edge bg-panel shadow-xl py-1">
                <button
                  onClick={() => {
                    setMenuOpenId(null);
                    setRenameId(row.id);
                    setRenameValue(row.meta?.title ?? row.title);
                  }}
                  className="w-full flex items-center gap-2 px-3 py-1.5 text-left text-sm hover:bg-panel2"
                >
                  <Pencil size={12} className="text-dim" />
                  {t("sidebar.renameSession")}
                </button>
                <button
                  onClick={() => {
                    setMenuOpenId(null);
                    void api
                      .exportSession(row.id)
                      .then((path) =>
                        flash(t("sidebar.exportedTo", { path })),
                      );
                  }}
                  className="w-full flex items-center gap-2 px-3 py-1.5 text-left text-sm hover:bg-panel2"
                >
                  <Download size={12} className="text-dim" />
                  {t("sidebar.exportSession")}
                </button>
                <button
                  onClick={() => {
                    setMenuOpenId(null);
                    setConfirmDelete(row.id);
                  }}
                  className="w-full flex items-center gap-2 px-3 py-1.5 text-left text-sm text-err hover:bg-panel2"
                >
                  <Trash2 size={12} className="text-err" />
                  {t("sidebar.deleteSession")}
                </button>
              </div>
            </>
          )}
        </div>
        {(row.tokens || row.time) && (
          <div className="pointer-events-none absolute left-1/2 -translate-x-1/2 bottom-full mb-1.5 z-10 hidden group-hover:block whitespace-nowrap rounded-md border border-edge bg-panel2 px-2 py-1 text-[10px] text-dim shadow-lg">
            {[row.tokens, row.time].filter(Boolean).join(" · ")}
          </div>
        )}
      </div>
    </li>
  );

  const renderWorkspaceRow = (w: { id: string; title: string; path: string }) => {
    const active = w.path === workspace;
    return (
      <li key={w.id}>
        <div
          className={`group flex items-center gap-1 rounded-lg px-1.5 py-1 text-left text-sm ${
            active
              ? "bg-accent/15 border border-accent/40"
              : "border border-transparent hover:bg-panel2"
          }`}
          title={w.path}
        >
          <button
            onClick={() => void openWorkspace(w.path)}
            className="flex flex-1 min-w-0 items-center gap-2"
          >
            <FolderOpen size={13} className="text-dim shrink-0" />
            <span className="flex-1 truncate">{w.title}</span>
          </button>
          <button
            onClick={() => void removeWorkspace(w.id)}
            className="text-dim opacity-0 group-hover:opacity-100 hover:text-err shrink-0"
            title={t("sidebar.removeWorkspace")}
          >
            <Trash2 size={12} />
          </button>
        </div>
      </li>
    );
  };

  return (
    <aside className="h-full border-r border-edge bg-panel flex flex-col min-h-0">
      {/* macOS: the native traffic lights float here, so the strip
          leaves them room and doubles as the window drag area; its
          height matches the chat header (h-11) so both rows align. */}
      <div
        className={`h-11 shrink-0 border-b border-edge flex items-center select-none ${
          isMac ? "pl-[78px]" : "px-4"
        }`}
        style={{ ["--wails-draggable" as string]: "drag" }}
        onDoubleClick={() => WindowToggleMaximise()}
      >
        {!isMac && (
          <div className="font-semibold tracking-wide">OpenCraft</div>
        )}
      </div>

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
          <Plus size={15} className="text-accent" />
          {t("sidebar.newChat")}
        </button>
      </div>

      <div className="flex-1 overflow-y-auto px-3 py-4 space-y-4">
        <section>
          <div className="flex items-center justify-between mb-2">
            <h3 className="text-xs uppercase tracking-wider text-dim">
              {t("sidebar.sessions")}
            </h3>
            <button
              onClick={openKanban}
              className="flex items-center gap-1 text-dim hover:text-fg text-xs"
              title={t("sidebar.kanban")}
            >
              <Kanban size={12} />
              {t("sidebar.kanban")}
            </button>
          </div>
          {visibleSessions.length === 0 ? (
            <p className="text-xs text-dim">—</p>
          ) : (
            <ul className="space-y-1">{visibleSessions.map(renderSessionRow)}</ul>
          )}
          {showMoreSessions && (
            <button
              onClick={() => setSessionsOpen(true)}
              className="w-full mt-2 flex items-center gap-2 rounded-lg border border-edge bg-panel2 px-3 py-2 text-sm hover:border-accent/50 transition-colors"
            >
              <Search size={14} className="text-dim" />
              {t("sidebar.moreSessions")}
            </button>
          )}
        </section>

        {workspaces.length > 0 && (
          <section>
            <h3 className="text-xs uppercase tracking-wider text-dim mb-2">
              {t("sidebar.workspaces")}
            </h3>
            <ul className="space-y-1">
              {visibleWorkspaces.map(renderWorkspaceRow)}
            </ul>
            {workspaces.length > 5 && (
              <button
                onClick={() => setWorkspacesOpen(true)}
                className="w-full mt-2 flex items-center gap-2 rounded-lg border border-edge bg-panel2 px-3 py-2 text-sm hover:border-accent/50 transition-colors"
              >
                <FolderOpen size={14} className="text-dim" />
                {t("sidebar.moreWorkspaces")}
              </button>
            )}
          </section>
        )}
      </div>

      <div className="border-t border-edge p-3 space-y-2">
        <button
          onClick={() => void api.chooseWorkspace()}
          className="flex items-center gap-1.5 text-xs text-dim hover:text-fg"
        >
          <FolderOpen size={13} />
          {t("sidebar.switchWorkspace")}
        </button>
        <button
          onClick={openConfig}
          className="flex items-center gap-1.5 text-xs text-dim hover:text-fg"
        >
          <Settings size={13} />
          {t("sidebar.settings")}
        </button>
      </div>

      {confirmDelete && (
        <div className="mt-2 mx-3 rounded-lg border border-err/40 bg-panel2 p-2 text-xs">
          <p>
            {t("sidebar.deleteSessionConfirm", {
              title: sessions.find((s) => s.id === confirmDelete)?.title ?? "",
            })}
          </p>
          <div className="mt-2 flex gap-2">
            <button
              onClick={() => setConfirmDelete(null)}
              className="rounded border border-edge px-2 py-0.5 text-dim hover:text-fg"
            >
              {t("interact.cancel")}
            </button>
            <button
              onClick={() => {
                void deleteSession(confirmDelete);
                setConfirmDelete(null);
              }}
              className="rounded bg-err px-2 py-0.5 text-white hover:opacity-90"
            >
              {t("sidebar.deleteSession")}
            </button>
          </div>
        </div>
      )}

      {sessionsOpen && (
        <div
          className="fixed inset-x-0 bottom-0 top-11 z-40 grid place-items-center bg-black/60 p-6"
          onClick={() => setSessionsOpen(false)}
        >
          <div
            className="w-[560px] max-h-[80vh] flex flex-col rounded-2xl border border-edge bg-panel shadow-2xl"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="flex items-center gap-2 px-4 py-3 border-b border-edge">
              <Search size={14} className="text-dim shrink-0" />
              <input
                autoFocus
                value={sessionQuery}
                onChange={(e) => setSessionQuery(e.target.value)}
                placeholder={t("sidebar.searchSessions")}
                className="flex-1 min-w-0 bg-transparent outline-none text-sm"
              />
              <button
                onClick={() => setSessionsOpen(false)}
                className="text-dim hover:text-fg shrink-0"
              >
                <X size={16} />
              </button>
            </div>
            <div className="flex-1 overflow-y-auto p-2">
              {filteredSessions.length === 0 ? (
                <p className="text-xs text-dim p-3">—</p>
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
          className="fixed inset-x-0 bottom-0 top-11 z-40 grid place-items-center bg-black/60 p-6"
          onClick={() => setWorkspacesOpen(false)}
        >
          <div
            className="w-[420px] max-h-[70vh] flex flex-col rounded-2xl border border-edge bg-panel shadow-2xl"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="flex items-center justify-between px-4 py-3 border-b border-edge">
              <h3 className="text-sm font-semibold">
                {t("sidebar.workspaces")}
              </h3>
              <button
                onClick={() => setWorkspacesOpen(false)}
                className="text-dim hover:text-fg"
              >
                <X size={16} />
              </button>
            </div>
            <div className="flex-1 overflow-y-auto p-2">
              {workspaces.length === 0 ? (
                <p className="text-xs text-dim p-3">—</p>
              ) : (
                <ul className="space-y-1">{workspaces.map(renderWorkspaceRow)}</ul>
              )}
            </div>
          </div>
        </div>
      )}
    </aside>
  );
}
