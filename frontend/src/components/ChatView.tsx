import { useEffect, useRef, useState } from "react";
import { AlertTriangle, Flame, Loader2, Send, ShieldCheck, Square } from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { useTranslation } from "react-i18next";
import { useStore } from "../lib/store";
import { InteractionCard } from "./InteractionCard";
import { ToolCard } from "./ToolCard";

function Reasoning({ text }: { text: string }) {
  const { t } = useTranslation();
  return (
    <details className="mb-1.5">
      <summary className="cursor-pointer text-xs text-dim select-none">
        {t("chat.reasonCollapse")}
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
  const pendingInteracts = useStore((s) => s.pendingInteracts);
  const workspace = useStore((s) => s.workspace);
  const send = useStore((s) => s.send);
  const cancelRun = useStore((s) => s.cancelRun);
  const mode = useStore((s) => s.mode);
  const setMode = useStore((s) => s.setMode);
  const [input, setInput] = useState("");
  const [confirmYolo, setConfirmYolo] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);
  // Stick-to-bottom: while the agent streams, the view follows the
  // latest output with an instant snap. Smooth-scrolling on every
  // delta races when the window is occluded (switching screens) and
  // the viewport visibly scrambles until the stream stops, so the
  // animation is avoided entirely. Scrolling up unpins; scrolling
  // back to the bottom re-pins.
  const [stick, setStick] = useState(true);
  // Tracks IME composition so pressing Enter to confirm a candidate
  // never sends the message (isComposing alone is unreliable in
  // WKWebView for the Enter key).
  const composingRef = useRef(false);
  const { t } = useTranslation();

  useEffect(() => {
    if (!stick) return;
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [messages, pendingInteracts, stick]);

  useEffect(() => {
    const onVisibility = () => {
      if (document.visibilityState === "visible" && stick) {
        const el = scrollRef.current;
        if (el) el.scrollTop = el.scrollHeight;
      }
    };
    document.addEventListener("visibilitychange", onVisibility);
    return () => document.removeEventListener("visibilitychange", onVisibility);
  }, [stick]);

  const submit = () => {
    if (!input.trim() || busy) return;
    setStick(true);
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
            <Loader2 size={12} className="animate-spin" /> {t("chat.running")}
          </span>
        )}
        <span className="flex-1" />
      </header>

      <div
        ref={scrollRef}
        onScroll={() => {
          const el = scrollRef.current;
          if (!el) return;
          setStick(el.scrollHeight - el.scrollTop - el.clientHeight < 80);
        }}
        className="flex-1 overflow-y-auto px-6 py-4"
      >
        {messages.length === 0 ? (
          <div className="h-full grid place-items-center text-dim text-sm">
            {configured
              ? t("chat.empty")
              : t("chat.emptyUnconfigured")}
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
                  {msg.items.map((item) => {
                    switch (item.kind) {
                      case "reasoning":
                        return <Reasoning key={item.id} text={item.text} />;
                      case "tool_call":
                        return <ToolCard key={item.id} tool={item.tool} />;
                      case "text":
                        return (
                          <div key={item.id} className="prose-chat text-sm">
                            <ReactMarkdown remarkPlugins={[remarkGfm]}>
                              {item.text}
                            </ReactMarkdown>
                          </div>
                        );
                    }
                  })}
                  {msg.items.length === 0 && busy && (
                    <div className="flex items-center gap-2 text-dim text-sm py-1">
                      <Loader2 size={14} className="animate-spin" />
                      {t("chat.thinking")}
                    </div>
                  )}
                </div>
              ),
            )}
            {pendingInteracts.map((spec) => (
              <InteractionCard key={spec.id} spec={spec} />
            ))}
          </div>
        )}
      </div>

      <div className="shrink-0 px-6 pb-4">
        <div className="max-w-3xl mx-auto rounded-xl border border-edge bg-panel focus-within:border-accent/60 transition-colors">
          {yolo && (
            <div className="flex items-center gap-2 rounded-t-xl border-b border-err/40 bg-err/10 px-4 py-1.5 text-xs text-err">
              <AlertTriangle size={13} />
              {t("chat.yoloBanner")}
            </div>
          )}
          <textarea
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onCompositionStart={() => (composingRef.current = true)}
            onCompositionEnd={() => (composingRef.current = false)}
            onKeyDown={(e) => {
              if (
                e.key === "Enter" &&
                !e.shiftKey &&
                !e.nativeEvent.isComposing &&
                !composingRef.current &&
                e.keyCode !== 229
              ) {
                e.preventDefault();
                submit();
              }
            }}
            rows={3}
            placeholder={
              configured ? t("chat.placeholder") : t("chat.placeholderUnconfigured")
            }
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
                title={yolo ? t("chat.workspaceMode") : t("chat.yoloMode")}
              >
                {yolo ? <Flame size={12} /> : <ShieldCheck size={12} />}
                {yolo ? t("chat.yoloMode") : t("chat.workspaceMode")}
              </button>
              <span className="text-xs text-dim hidden sm:inline">
                {busy ? t("chat.runningHint") : t("chat.enterHint")}
              </span>
            </div>
            {busy ? (
              <button
                onClick={() => void cancelRun()}
                className="flex items-center gap-1.5 rounded-lg border border-edge px-3 py-1.5 text-sm text-err hover:bg-panel2"
              >
                <Square size={13} /> {t("chat.stop")}
              </button>
            ) : (
              <button
                onClick={submit}
                disabled={!input.trim()}
                className="flex items-center gap-1.5 rounded-lg bg-accent px-4 py-1.5 text-sm text-white hover:opacity-90 disabled:opacity-40"
              >
                <Send size={13} /> {t("chat.send")}
              </button>
            )}
          </div>
        </div>
        {confirmYolo && (
          <div className="fixed inset-0 z-40 grid place-items-center bg-black/60">
            <div className="w-[420px] rounded-2xl border border-err/40 bg-panel p-5 shadow-2xl">
              <div className="flex items-center gap-2 text-sm font-semibold text-err">
                <AlertTriangle size={16} />
                {t("chat.yoloConfirmTitle")}
              </div>
              <p className="mt-3 text-sm text-dim leading-relaxed">
                {t("chat.yoloConfirmBody")}
              </p>
              <div className="mt-4 flex justify-end gap-2">
                <button
                  onClick={() => setConfirmYolo(false)}
                  className="rounded-lg border border-edge px-4 py-1.5 text-sm text-dim hover:text-fg"
                >
                  {t("interact.cancel")}
                </button>
                <button
                  onClick={() => {
                    setConfirmYolo(false);
                    void setMode("yolo");
                  }}
                  className="rounded-lg bg-err px-4 py-1.5 text-sm text-white hover:opacity-90"
                >
                  {t("chat.confirmSwitch")}
                </button>
              </div>
            </div>
          </div>
        )}
      </div>
    </main>
  );
}
