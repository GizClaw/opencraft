import {
  FolderOpen,
  Kanban,
  MessageSquare,
  Plus,
  Settings,
} from "lucide-react";
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
  const currentSession = useStore((s) => s.currentSession);
  const resume = useStore((s) => s.resume);
  const openKanban = useStore((s) => s.openKanban);
  const { t } = useTranslation();

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
            <ul className="space-y-1">
              {sessions.map((s) => (
                <li key={s.id}>
                  <button
                    onClick={() => void resume(s.id)}
                    className={`w-full flex items-center gap-2 rounded-lg px-2 py-1.5 text-left text-sm ${
                      s.id === currentSession
                        ? "bg-accent/15 border border-accent/40"
                        : "border border-transparent hover:bg-panel2"
                    }`}
                    title={s.id}
                  >
                    <MessageSquare
                      size={13}
                      className="text-dim shrink-0"
                    />
                    <span className="flex-1 truncate">{s.title}</span>
                    <span className="text-xs text-dim shrink-0">
                      {fmtTime(s.updated_at)}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
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
