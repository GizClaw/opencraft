import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Check, ChevronDown, ChevronRight, Loader2, Wrench, X } from "lucide-react";
import type { ToolView } from "../lib/store";

export function ToolCard({ tool }: { tool: ToolView }) {
  const [open, setOpen] = useState(false);
  const { t } = useTranslation();
  const Icon =
    tool.status === "running"
      ? Loader2
      : tool.status === "error"
        ? X
        : Check;
  const color =
    tool.status === "running"
      ? "text-accent"
      : tool.status === "error"
        ? "text-err"
        : "text-ok";

  return (
    <div className="rounded-lg border border-edge bg-panel2 overflow-hidden my-1.5">
      <button
        onClick={() => setOpen(!open)}
        className="w-full flex items-center gap-2 px-3 py-2 text-left text-sm hover:bg-panel2/70"
      >
        <Icon size={14} className={`${color} ${tool.status === "running" ? "animate-spin" : ""}`} />
        <Wrench size={13} className="text-dim" />
        <span className="font-mono text-xs">{tool.name}</span>
        <span className="flex-1" />
        <span className="text-xs text-dim">{t(`tool.${tool.status}`)}</span>
        {open ? <ChevronDown size={14} className="text-dim" /> : <ChevronRight size={14} className="text-dim" />}
      </button>
      {open && (
        <div className="border-t border-edge px-3 py-2 space-y-2">
          <pre className="text-xs overflow-x-auto whitespace-pre-wrap break-all font-mono text-dim max-h-48 overflow-y-auto">
            {tool.args}
          </pre>
          {tool.result !== undefined && (
            <pre className="text-xs overflow-x-auto whitespace-pre-wrap break-all font-mono max-h-64 overflow-y-auto">
              {tool.result}
            </pre>
          )}
        </div>
      )}
    </div>
  );
}
