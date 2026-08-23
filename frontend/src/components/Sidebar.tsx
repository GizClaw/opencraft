import {
  Bot,
  FolderOpen,
  Plus,
  RefreshCw,
  Settings,
} from "lucide-react";
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
          新会话
        </button>
      </div>

      <div className="flex-1 overflow-y-auto px-3 py-4 space-y-5">
        <section>
          <div className="flex items-center justify-between mb-2">
            <h3 className="text-xs uppercase tracking-wider text-dim">
              子代理
            </h3>
            <button
              onClick={() => void refreshAgents()}
              className="text-dim hover:text-fg"
              title="刷新"
            >
              <RefreshCw size={12} />
            </button>
          </div>
          {agents.length === 0 ? (
            <p className="text-xs text-dim">
              暂无子代理
              <br />
              （助手可通过 create_agent 创建）
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
          推理配置
        </button>
      </div>
    </aside>
  );
}
