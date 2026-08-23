import {
  FolderOpen,
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
  const { t } = useTranslation();

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

      <div className="flex-1" />

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
