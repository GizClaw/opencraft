import {
  FolderOpen,
  Kanban,
  MessageSquare,
  Plus,
  Settings,
  Trash2,
  Loader2,
} from "lucide-react";
import { useState } from "react";
import { api } from "../lib/api";
import { useTranslation } from "react-i18next";
import { useStore } from "../lib/store";

function basename(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).pop() ?? path;
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
  const { t } = useTranslation();
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [sessionQuery, setSessionQuery] = useState("");

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

  const fmtTokens = (n: number) =>
    n >= 1000 ? `${(n / 1000).toFixed(1)}k` : n > 0 ? String(n) : "";

  return (
    <aside className="w-60 shrink-0 border-r border-edge bg-panel flex flex-col min-h-0">
      <div className="px-4 py-3 border-b border-edge">
        <div className="font-semibold tracking-wide">opencraft</div>
      </div>

      <div className="px-3 pt-3">
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
          {sessions.length === 0 ? (
            <p className="text-xs text-dim">—</p>
          ) : (
            <>
              <input
                value={sessionQuery}
                onChange={(e) => setSessionQuery(e.target.value)}
                placeholder={t("sidebar.searchSessions")}
                className="mb-2 w-full rounded-lg border border-edge bg-panel2 px-2 py-1 text-xs outline-none focus:border-accent"
              />
              <ul className="space-y-1">
                {sessions
                  .filter((s) =>
                    s.title.toLowerCase().includes(sessionQuery.toLowerCase()),
                  )
                  .map((s) => (
                    <li key={s.id}>
                    <div
                      className={`group flex items-center gap-1 rounded-lg px-1.5 py-1 text-left text-sm ${
                        s.id === currentSession
                          ? "bg-accent/15 border border-accent/40"
                          : "border border-transparent hover:bg-panel2"
                      }`}
                      title={s.id}
                    >
                      <button
                        onClick={() => void resume(s.id)}
                        className="flex flex-1 min-w-0 items-center gap-2"
                      >
                        <MessageSquare
                          size={13}
                          className="text-dim shrink-0"
                        />
                        <span className="flex-1 truncate">{s.title}</span>
                        {conversations[s.id]?.busy && (
                          <Loader2
                            size={12}
                            className="text-accent animate-spin shrink-0"
                          />
                        )}
                        <span className="text-xs text-dim shrink-0">
                          {fmtTokens(s.total_tokens)}
                        </span>
                        <span className="text-xs text-dim shrink-0">
                          {fmtTime(s.updated_at)}
                        </span>
                      </button>
                      <button
                        onClick={() => setConfirmDelete(s.id)}
                        className="text-dim opacity-0 group-hover:opacity-100 hover:text-err shrink-0"
                        title={t("sidebar.deleteSession")}
                      >
                        <Trash2 size={12} />
                      </button>
                    </div>
                    </li>
                  ))}
              </ul>
              {confirmDelete && (
                <div className="mt-2 rounded-lg border border-err/40 bg-panel2 p-2 text-xs">
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
            </>
          )}
        </section>
      </div>

      <div className="border-t border-edge p-3 space-y-2">
        <div
          className="flex items-center gap-1.5 text-xs text-dim truncate"
          title={workspace}
        >
          <FolderOpen size={13} className="shrink-0" />
          <span className="truncate">{basename(workspace)}</span>
        </div>
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
    </aside>
  );
}
