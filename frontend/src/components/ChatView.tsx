import { useEffect, useRef, useState } from "react";
import { AlertTriangle, Flame, Loader2, Send, ShieldCheck, Square } from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { useStore } from "../lib/store";
import { InteractionCard } from "./InteractionCard";
import { ToolCard } from "./ToolCard";

function Reasoning({ text }: { text: string }) {
  return (
    <details className="mb-1.5">
      <summary className="cursor-pointer text-xs text-dim select-none">
        思考过程
      </summary>
      <div className="mt-1 rounded-lg bg-panel2 border border-edge p-3 text-xs text-dim whitespace-pre-wrap">
        {text}
      </div>
    </details>
  );
}

export function ChatView() {
  const messages = useStore((s) => s.messages);
  const busy = useStore((s) => s.busy);
  const configured = useStore((s) => s.configured);
  const pendingInteract = useStore((s) => s.pendingInteract);
  const workspace = useStore((s) => s.workspace);
  const send = useStore((s) => s.send);
  const cancelRun = useStore((s) => s.cancelRun);
  const mode = useStore((s) => s.mode);
  const setMode = useStore((s) => s.setMode);
  const [input, setInput] = useState("");
  const [confirmYolo, setConfirmYolo] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    scrollRef.current?.scrollTo({
      top: scrollRef.current.scrollHeight,
      behavior: "smooth",
    });
  }, [messages, pendingInteract]);

  const submit = () => {
    if (!input.trim() || busy) return;
    const text = input;
    setInput("");
    void send(text);
  };

  const workspaceName =
    workspace.split(/[\\/]/).filter(Boolean).pop() ?? workspace;
  const yolo = mode === "yolo";

  return (
    <main className="flex-1 min-w-0 flex flex-col min-h-0">
      <header className="h-11 shrink-0 border-b border-edge bg-panel flex items-center px-4 gap-2">
        <span className="text-sm font-medium truncate">{workspaceName}</span>
        {busy && (
          <span className="flex items-center gap-1 text-xs text-accent">
            <Loader2 size={12} className="animate-spin" /> 运行中
          </span>
        )}
        <span className="flex-1" />
      </header>

      <div ref={scrollRef} className="flex-1 overflow-y-auto px-6 py-4">
        {messages.length === 0 ? (
          <div className="h-full grid place-items-center text-dim text-sm">
            {configured
              ? "开始与 opencraft 对话 — 描述任务即可，助手会自主调用工具"
              : "请先完成推理配置"}
          </div>
        ) : (
          <div className="max-w-3xl mx-auto space-y-4">
            {messages.map((msg) =>
              msg.role === "user" ? (
                <div key={msg.id} className="flex justify-end">
                  <div className="max-w-[80%] rounded-2xl rounded-br-sm bg-accent/15 border border-accent/30 px-4 py-2.5 text-sm whitespace-pre-wrap">
                    {msg.text}
                  </div>
                </div>
              ) : (
                <div key={msg.id} className="flex flex-col gap-1">
                  {msg.reasoning && <Reasoning text={msg.reasoning} />}
                  {msg.tools.map((tool) => (
                    <ToolCard key={tool.id} tool={tool} />
                  ))}
                  {msg.text && (
                    <div className="prose-chat text-sm">
                      <ReactMarkdown remarkPlugins={[remarkGfm]}>
                        {msg.text}
                      </ReactMarkdown>
                    </div>
                  )}
                  {!msg.text && msg.tools.length === 0 && busy && (
                    <div className="flex items-center gap-2 text-dim text-sm py-1">
                      <Loader2 size={14} className="animate-spin" />
                      思考中…
                    </div>
                  )}
                </div>
              ),
            )}
            {pendingInteract && <InteractionCard spec={pendingInteract} />}
          </div>
        )}
      </div>

      <div className="shrink-0 px-6 pb-4">
        <div className="max-w-3xl mx-auto rounded-xl border border-edge bg-panel focus-within:border-accent/60 transition-colors">
          {yolo && (
            <div className="flex items-center gap-2 rounded-t-xl border-b border-err/40 bg-err/10 px-4 py-1.5 text-xs text-err">
              <AlertTriangle size={13} />
              YOLO 模式：沙箱已禁用，命令直接在宿主机执行，无批准提示
            </div>
          )}
          <textarea
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                e.preventDefault();
                submit();
              }
            }}
            rows={3}
            placeholder={configured ? "描述任务，Enter 发送，Shift+Enter 换行" : "完成推理配置后开始对话"}
            disabled={!configured}
            className="w-full resize-none bg-transparent px-4 pt-3 text-sm outline-none disabled:opacity-50"
          />
          <div className="flex items-center justify-between px-3 pb-2.5">
            <div className="flex items-center gap-3">
              <button
                onClick={() => (yolo ? void setMode("workspace") : setConfirmYolo(true))}
                className={`flex items-center gap-1.5 rounded-lg border px-2.5 py-1 text-xs transition-colors ${
                  yolo
                    ? "border-err/50 bg-err/10 text-err hover:bg-err/20"
                    : "border-edge text-dim hover:text-fg"
                }`}
                title={yolo ? "点击切回工作区模式" : "点击进入 YOLO 模式"}
              >
                {yolo ? <Flame size={12} /> : <ShieldCheck size={12} />}
                {yolo ? "YOLO" : "工作区模式"}
              </button>
              <span className="text-xs text-dim hidden sm:inline">
                {busy ? "正在运行，可点击右侧停止" : "Enter 发送 · Shift+Enter 换行"}
              </span>
            </div>
            {busy ? (
              <button
                onClick={() => void cancelRun()}
                className="flex items-center gap-1.5 rounded-lg border border-edge px-3 py-1.5 text-sm text-err hover:bg-panel2"
              >
                <Square size={13} /> 停止
              </button>
            ) : (
              <button
                onClick={submit}
                disabled={!input.trim()}
                className="flex items-center gap-1.5 rounded-lg bg-accent px-4 py-1.5 text-sm text-white hover:opacity-90 disabled:opacity-40"
              >
                <Send size={13} /> 发送
              </button>
            )}
          </div>
        </div>
        {confirmYolo && (
          <div className="fixed inset-0 z-40 grid place-items-center bg-black/60">
            <div className="w-[420px] rounded-2xl border border-err/40 bg-panel p-5 shadow-2xl">
              <div className="flex items-center gap-2 text-sm font-semibold text-err">
                <AlertTriangle size={16} />
                切换到 YOLO 模式？
              </div>
              <p className="mt-3 text-sm text-dim leading-relaxed">
                YOLO 会禁用当前会话的沙箱：命令以完整环境在宿主机执行、
                不再请求权限批准、文件工具可访问任意路径。仅对当前对话生效。
              </p>
              <div className="mt-4 flex justify-end gap-2">
                <button
                  onClick={() => setConfirmYolo(false)}
                  className="rounded-lg border border-edge px-4 py-1.5 text-sm text-dim hover:text-fg"
                >
                  取消
                </button>
                <button
                  onClick={() => {
                    setConfirmYolo(false);
                    void setMode("yolo");
                  }}
                  className="rounded-lg bg-err px-4 py-1.5 text-sm text-white hover:opacity-90"
                >
                  确认切换
                </button>
              </div>
            </div>
          </div>
        )}
      </div>
    </main>
  );
}
