import {
  Bot,
  FolderOpen,
  Plus,
  RefreshCw,
  Settings,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { useStore } from "../lib/store";

function basename(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).pop() ?? path;
}

export function Sidebar() {
  const agents = useStore((s) => s.agents);
  const workspace = useStore((s) => s.workspace);
  const newChat = useStore((s) => s.newChat);
  const openOnboarding = useStore((s) => s.openOnboarding);
  const refreshAgents = useStore((s) => s.refreshAgents);
  const { t, i18n } = useTranslation();
  const lang = i18n.resolvedLanguage?.startsWith("zh") ? "zh" : "en";

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

      <div className="flex-1 overflow-y-auto px-3 py-4 space-y-5">
        <section>
          <div className="flex items-center justify-between mb-2">
            <h3 className="text-xs uppercase tracking-wider text-dim">
              {t("sidebar.subagents")}
            </h3>
            <button
              onClick={() => void refreshAgents()}
              className="text-dim hover:text-fg"
              title={t("sidebar.refresh")}
            >
              <RefreshCw size={12} />
            </button>
          </div>
          {agents.length === 0 ? (
            <p className="text-xs text-dim">
              {t("sidebar.noAgents")}
              <br />
              {t("sidebar.noAgentsHint")}
            </p>
          ) : (
            <ul className="space-y-2">
              {agents.map((a) => (
                <li
                  key={a.name}
                  className="rounded-lg border border-edge bg-panel2 p-2"
                  title={a.description}
                >
                  <div className="flex items-center gap-1.5 text-sm">
                    <Bot size={14} className="text-accent shrink-0" />
                    <span className="truncate">{a.name}</span>
                  </div>
                  <p className="text-xs text-dim mt-1 line-clamp-2">
                    {a.description}
                  </p>
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
          onClick={openOnboarding}
          className="flex items-center gap-1.5 text-xs text-dim hover:text-fg"
        >
          <Settings size={13} />
          {t("sidebar.inferenceConfig")}
        </button>
        <div className="flex items-center gap-1 pt-1">
          <div className="flex rounded-lg border border-edge overflow-hidden text-xs">
            <button
              onClick={() => void i18n.changeLanguage("zh")}
              className={`px-2 py-0.5 ${
                lang === "zh"
                  ? "bg-accent text-white"
                  : "text-dim hover:text-fg"
              }`}
            >
              中文
            </button>
            <button
              onClick={() => void i18n.changeLanguage("en")}
              className={`px-2 py-0.5 ${
                lang === "en"
                  ? "bg-accent text-white"
                  : "text-dim hover:text-fg"
              }`}
            >
              EN
            </button>
          </div>
          <span className="flex-1" />
        </div>
      </div>
    </aside>
  );
}
