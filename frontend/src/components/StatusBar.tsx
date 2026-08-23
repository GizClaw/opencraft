import { Loader2 } from "lucide-react";
import { useStore } from "../lib/store";

export function StatusBar() {
  const busy = useStore((s) => s.busy);
  const statusText = useStore((s) => s.statusText);
  const lastUsage = useStore((s) => s.lastUsage);
  const model = useStore((s) => s.status?.default_model) ?? "";

  return (
    <footer className="h-8 shrink-0 border-t border-edge bg-panel flex items-center gap-3 px-4 text-xs text-dim">
      <span className="flex items-center gap-1.5 min-w-0">
        {busy && <Loader2 size={13} className="animate-spin text-accent" />}
        <span className="truncate">{statusText || "就绪"}</span>
      </span>
      <span className="flex-1" />
      {lastUsage && (
        <span className="tabular-nums whitespace-nowrap">
          ↑{lastUsage.input_tokens} ↓{lastUsage.output_tokens}
          {lastUsage.reasoning_tokens > 0 && (
            <> 思考 {lastUsage.reasoning_tokens}</>
          )}
          {" "}· {lastUsage.latency_ms}ms
        </span>
      )}
      {model && (
        <span className="rounded bg-panel2 border border-edge px-2 py-0.5 whitespace-nowrap">
          {model}
        </span>
      )}
    </footer>
  );
}
