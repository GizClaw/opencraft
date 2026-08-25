import { memo, useEffect, useRef, useState } from 'react';
import {
  AlertTriangle,
  Archive,
  Bot,
  ChevronDown,
  ChevronRight,
  File,
  Flame,
  Folder,
  Lock,
  Loader2,
  RotateCcw,
  Send,
  ShieldCheck,
  Square,
} from 'lucide-react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { useTranslation } from 'react-i18next';
import { OnFileDrop, OnFileDropOff } from '../../wailsjs/runtime/runtime';
import { api } from '../lib/api';
import { COMPACT_SUMMARY_PREFIX } from '../lib/compact';
import { useStore } from '../lib/store';
import type { FileNode } from '../lib/types';
import type { MessageView } from '../lib/store';
import { InteractionCard } from './InteractionCard';
import { ToolCard } from './ToolCard';

function Reasoning({ text }: { text: string }) {
  const { t } = useTranslation();
  return (
    <details className="mb-1.5">
      <summary className="cursor-pointer text-xs text-dim select-none">
        {t('chat.reasonCollapse')}
      </summary>
      <div className="mt-1 rounded-lg bg-panel2 border border-edge p-3 text-xs text-dim whitespace-pre-wrap">
        {text}
      </div>
    </details>
  );
}

// AssistantText renders one assistant text block. While the message is
// still streaming, markdown parsing is deferred to plain text: parsing
// the full document on every token delta stalls the main thread and
// freezes the UI on long outputs. Completed messages render markdown
// once.
const AssistantText = memo(function AssistantText({
  text,
  streaming,
}: {
  text: string;
  streaming: boolean;
}) {
  if (streaming) {
    return <div className="prose-chat whitespace-pre-wrap text-sm">{text}</div>;
  }
  return (
    <div className="prose-chat text-sm">
      <ReactMarkdown remarkPlugins={[remarkGfm]}>{text}</ReactMarkdown>
    </div>
  );
});

// MessageRow renders one conversation message. Memoized so stream
// deltas only re-render the message that changed instead of reparsing
// every completed message on each token.
const MessageRow = memo(function MessageRow({
  msg,
  busy,
  streaming,
}: {
  msg: MessageView;
  busy: boolean;
  streaming: boolean;
}) {
  const { t } = useTranslation();
  if (msg.role === 'user') {
    if (msg.text.startsWith(COMPACT_SUMMARY_PREFIX)) {
      return <CompactCard text={msg.text} />;
    }
    return (
      <div className="flex justify-end">
        <div className="max-w-[80%] rounded-2xl rounded-br-sm border border-accent/30 bg-accent/15 px-4 py-2.5 text-sm whitespace-pre-wrap">
          {msg.text}
        </div>
      </div>
    );
  }
  return (
    <div className="flex flex-col gap-1">
      {msg.items.map((item) => {
        switch (item.kind) {
          case 'reasoning':
            return <Reasoning key={item.id} text={item.text} />;
          case 'tool_call':
            return <ToolCard key={item.id} tool={item.tool} />;
          case 'text':
            return (
              <AssistantText
                key={item.id}
                text={item.text}
                streaming={streaming}
              />
            );
        }
      })}
      {msg.items.length === 0 && busy && (
        <div className="flex items-center gap-2 py-1 text-sm text-dim">
          <Loader2 size={14} className="animate-spin" />
          {t('chat.thinking')}
        </div>
      )}
    </div>
  );
});

// CompactCard renders a compaction summary (a user message marked with
// COMPACT_SUMMARY_PREFIX) as a tool-style card instead of a chat
// bubble, so auto-compaction is visible in the transcript.
function CompactCard({ text }: { text: string }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(true);
  const body = text.slice(COMPACT_SUMMARY_PREFIX.length).trim();
  return (
    <div className="overflow-hidden rounded-lg border border-edge bg-panel2 my-1.5">
      <button
        onClick={() => setOpen(!open)}
        className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-panel2/70"
      >
        <Archive size={14} className="shrink-0 text-dim" />
        <span>{t('tool.compacted')}</span>
        <span className="flex-1" />
        <span className="text-xs text-dim">{t('tool.done')}</span>
        {open ? (
          <ChevronDown size={14} className="shrink-0 text-dim" />
        ) : (
          <ChevronRight size={14} className="shrink-0 text-dim" />
        )}
      </button>
      {open && body && (
        <div className="max-h-80 overflow-y-auto whitespace-pre-wrap border-t border-edge px-3 py-2 text-xs text-dim">
          {body}
        </div>
      )}
    </div>
  );
}

export function ChatView() {
  const current = useStore((s) => s.current);
  const conv = useStore((s) => s.conversations[s.current]);
  const sessions = useStore((s) => s.sessions);
  const messages = conv?.messages ?? [];
  const busy = conv?.busy ?? false;
  const configured = useStore((s) => s.configured);
  const pendingInteracts = conv?.pendingInteracts ?? [];
  const workspace = useStore((s) => s.workspace);
  const send = useStore((s) => s.send);
  const cancelRun = useStore((s) => s.cancelRun);
  const subagentCards = useStore((s) => s.subagentCards);
  const subagentPanelOpen = useStore((s) => s.subagentPanelOpen);
  const toggleSubagentPanel = useStore((s) => s.toggleSubagentPanel);
  const retryLast = useStore((s) => s.retryLast);
  const mode = conv?.mode ?? 'workspace';
  const setMode = useStore((s) => s.setMode);
  const think = conv?.think ?? 'medium';
  const setThink = useStore((s) => s.setThink);
  const model = conv?.model ?? '';
  const setModel = useStore((s) => s.setModel);
  const modelOptions = useStore((s) => s.modelOptions);
  const lastFailed = conv?.lastFailed ?? false;
  const openConfig = useStore((s) => s.openConfig);
  const [input, setInput] = useState('');
  const [confirmYolo, setConfirmYolo] = useState(false);
  const [modeMenuOpen, setModeMenuOpen] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const [mention, setMention] = useState<{ open: boolean; query: string }>({
    open: false,
    query: '',
  });
  const [mentionItems, setMentionItems] = useState<FileNode[]>([]);
  const mentionLoaded = useRef(false);
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
    OnFileDrop((_x, _y, paths) => {
      if (!paths || paths.length === 0) return;
      setInput((prev) => prev + (prev ? '\n' : '') + paths.join('\n'));
    }, true);
    return () => {
      OnFileDropOff();
    };
  }, []);

  useEffect(() => {
    if (!stick) return;
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [messages, pendingInteracts, stick]);

  useEffect(() => {
    const onVisibility = () => {
      if (document.visibilityState === 'visible' && stick) {
        const el = scrollRef.current;
        if (el) el.scrollTop = el.scrollHeight;
      }
    };
    document.addEventListener('visibilitychange', onVisibility);
    return () => document.removeEventListener('visibilitychange', onVisibility);
  }, [stick]);

  const submit = () => {
    if (!input.trim() || busy) return;
    setStick(true);
    const text = input;
    setInput('');
    void send(text);
  };

  const retry = () => {
    setInput('');
    void retryLast();
  };

  const onInputChange = (value: string) => {
    setInput(value);
    const match = value.match(/(?:^|\s)@([\w./-]*)$/);
    if (match) {
      setMention({ open: true, query: match[1] });
      if (!mentionLoaded.current) {
        mentionLoaded.current = true;
        void api
          .listDir(workspace)
          .then(setMentionItems)
          .catch(() => setMentionItems([]));
      }
    } else {
      setMention((m) => (m.open ? { open: false, query: '' } : m));
    }
  };

  const insertMention = (node: FileNode) => {
    const match = input.match(/(?:^|\s)@([\w./-]*)$/);
    let next: string;
    if (match) {
      const at = match.index! + match[0].indexOf('@');
      next = input.slice(0, at) + node.path;
    } else {
      next = input + node.path;
    }
    setInput(next + ' ');
    setMention({ open: false, query: '' });
    inputRef.current?.focus();
  };

  const filteredMentions = mentionItems.filter((n) =>
    n.name.toLowerCase().includes(mention.query.toLowerCase()),
  );

  const sessionTitle = sessions.find((s) => s.id === current)?.title;
  const headerTitle =
    sessionTitle && sessionTitle !== '(empty)'
      ? sessionTitle
      : t('chat.newSession');
  const yolo = mode === 'yolo';
  const readOnly = mode === 'read-only';

  return (
    <main className="flex-1 min-w-0 flex flex-col min-h-0">
      <header className="h-11 shrink-0 border-b border-edge bg-panel flex items-center px-4 gap-2">
        <span className="text-sm font-medium truncate">{headerTitle}</span>
        {busy && (
          <span className="flex items-center gap-1 text-xs text-accent">
            <Loader2 size={12} className="animate-spin" /> {t('chat.running')}
          </span>
        )}
        <span className="flex-1" />
        {subagentCards.length > 0 && (
          <button
            onClick={toggleSubagentPanel}
            className={`flex items-center gap-1.5 rounded-lg border px-2 py-1 text-xs transition-colors ${
              subagentPanelOpen
                ? 'border-violet-400/40 bg-violet-400/10 text-violet-400'
                : 'border-edge text-dim hover:text-fg'
            }`}
            title={t('subagent.toggle')}
          >
            <Bot size={13} />
            {subagentCards.length}
          </button>
        )}
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
          <div className="h-full grid place-items-center">
            <div className="text-center space-y-3">
              <div className="text-dim text-sm">
                {configured ? t('chat.empty') : t('chat.emptyUnconfigured')}
              </div>
              {!configured && (
                <button
                  onClick={openConfig}
                  className="rounded-lg border border-edge px-3 py-1.5 text-sm text-fg hover:border-accent/50 transition-colors"
                >
                  {t('chat.openSettings')}
                </button>
              )}
            </div>
          </div>
        ) : (
          <div className="max-w-3xl mx-auto space-y-4">
            {messages.map((msg, i) => (
              <MessageRow
                key={msg.id}
                msg={msg}
                busy={busy}
                streaming={
                  busy && msg.role === 'assistant' && i === messages.length - 1
                }
              />
            ))}
            {pendingInteracts.map((spec) => (
              <InteractionCard key={spec.id} spec={spec} />
            ))}
          </div>
        )}
      </div>

      <div className="shrink-0 px-6 pb-4">
        <div className="max-w-3xl mx-auto rounded-xl border border-edge bg-panel focus-within:border-accent/60 transition-colors">
          <div
            className={`flex items-center gap-2 rounded-t-xl border-b px-3 py-1.5 text-xs transition-colors ${
              yolo
                ? 'border-[#d9a83c]/40 bg-[#d9a83c]/10'
                : 'border-transparent'
            }`}
          >
            <div className="relative">
              <button
                onClick={() => setModeMenuOpen((v) => !v)}
                className={`flex items-center gap-1.5 rounded-md border px-2 py-0.5 transition-colors ${
                  yolo
                    ? 'border-[#d9a83c]/50 bg-[#d9a83c]/15 text-[#e2b341] hover:bg-[#d9a83c]/25'
                    : readOnly
                      ? 'border-accent/40 bg-accent/10 text-accent hover:bg-accent/20'
                      : 'border-edge text-dim hover:text-fg'
                }`}
                title={t('chat.sandboxMode')}
              >
                {yolo ? (
                  <Flame size={11} />
                ) : readOnly ? (
                  <Lock size={11} />
                ) : (
                  <ShieldCheck size={11} />
                )}
                {yolo
                  ? t('chat.yoloMode')
                  : readOnly
                    ? t('chat.readOnlyMode')
                    : t('chat.workspaceMode')}
                <ChevronDown size={11} />
              </button>
              {modeMenuOpen && (
                <>
                  <div
                    className="fixed inset-0 z-30"
                    onClick={() => setModeMenuOpen(false)}
                  />
                  <div className="absolute left-0 top-full z-40 mt-1 w-44 rounded-lg border border-edge bg-panel p-1 shadow-xl">
                    <button
                      onClick={() => {
                        setModeMenuOpen(false);
                        void setMode('read-only');
                      }}
                      className={`flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs ${
                        readOnly
                          ? 'bg-accent/10 text-accent'
                          : 'text-dim hover:bg-panel2 hover:text-fg'
                      }`}
                    >
                      <Lock size={12} /> {t('chat.readOnlyMode')}
                    </button>
                    <button
                      onClick={() => {
                        setModeMenuOpen(false);
                        void setMode('workspace');
                      }}
                      className={`flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs ${
                        !readOnly && !yolo
                          ? 'bg-accent/10 text-accent'
                          : 'text-dim hover:bg-panel2 hover:text-fg'
                      }`}
                    >
                      <ShieldCheck size={12} /> {t('chat.workspaceMode')}
                    </button>
                    <button
                      onClick={() => {
                        setModeMenuOpen(false);
                        setConfirmYolo(true);
                      }}
                      className={`flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs ${
                        yolo
                          ? 'bg-[#d9a83c]/15 text-[#e2b341]'
                          : 'text-dim hover:bg-panel2 hover:text-fg'
                      }`}
                    >
                      <Flame size={12} /> {t('chat.yoloMode')}
                    </button>
                  </div>
                </>
              )}
            </div>
            {readOnly && (
              <span className="flex items-center gap-1 text-accent">
                <Lock size={12} />
                {t('chat.readOnlyBanner')}
              </span>
            )}
            {yolo && (
              <span className="flex items-center gap-1 text-[#d9a83c]">
                <AlertTriangle size={12} />
                {t('chat.yoloBanner')}
              </span>
            )}
            <span className="flex-1" />
            <span className="text-dim hidden sm:inline">
              {busy ? t('chat.runningHint') : t('chat.enterHint')}
            </span>
          </div>
          <textarea
            ref={inputRef}
            value={input}
            onChange={(e) => onInputChange(e.target.value)}
            onCompositionStart={() => (composingRef.current = true)}
            onCompositionEnd={() => (composingRef.current = false)}
            onKeyDown={(e) => {
              if (e.key === 'Escape' && mention.open) {
                setMention({ open: false, query: '' });
                return;
              }
              if (
                e.key === 'Enter' &&
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
              configured
                ? t('chat.placeholder')
                : t('chat.placeholderUnconfigured')
            }
            disabled={!configured}
            className="w-full resize-none bg-transparent px-4 pt-3 text-sm outline-none disabled:opacity-50"
          />
          {mention.open && (
            <div className="mx-3 mb-2 max-h-48 overflow-y-auto rounded-lg border border-edge bg-panel2 shadow-xl">
              {filteredMentions.length === 0 ? (
                <div className="px-3 py-2 text-xs text-dim">—</div>
              ) : (
                filteredMentions.slice(0, 12).map((n) => (
                  <button
                    key={n.path}
                    onClick={() => insertMention(n)}
                    className="w-full flex items-center gap-2 px-3 py-1.5 text-left text-sm hover:bg-panel"
                  >
                    {n.is_dir ? (
                      <Folder size={13} className="text-accent shrink-0" />
                    ) : (
                      <File size={13} className="text-dim shrink-0" />
                    )}
                    <span className="truncate">{n.name}</span>
                  </button>
                ))
              )}
            </div>
          )}
          <div className="flex items-center justify-between px-3 pb-2.5">
            <div className="flex items-center gap-3">
              <div className="flex items-center gap-1.5 rounded-lg border border-edge px-2.5 py-1 text-xs text-dim">
                {t('chat.thinkLabel')}
                <select
                  value={think}
                  onChange={(e) => void setThink(e.target.value)}
                  className="bg-transparent outline-none text-fg"
                >
                  <option value="low">{t('chat.thinkLow')}</option>
                  <option value="medium">{t('chat.thinkMedium')}</option>
                  <option value="high">{t('chat.thinkHigh')}</option>
                </select>
              </div>
              {modelOptions.length > 0 && (
                <div className="flex items-center gap-1.5 rounded-lg border border-edge px-2.5 py-1 text-xs text-dim">
                  {t('chat.modelLabel')}
                  <select
                    value={model}
                    onChange={(e) => void setModel(e.target.value)}
                    className="bg-transparent outline-none text-fg max-w-40"
                    title={model}
                  >
                    <option value="">{t('chat.modelAuto')}</option>
                    {modelOptions.map((m) => (
                      <option key={m.id} value={m.id}>
                        {m.label}
                      </option>
                    ))}
                  </select>
                </div>
              )}
            </div>
            {busy ? (
              <button
                onClick={() => void cancelRun()}
                className="flex items-center gap-1.5 rounded-lg border border-edge px-3 py-1.5 text-sm text-err hover:bg-panel2"
              >
                <Square size={13} /> {t('chat.stop')}
              </button>
            ) : (
              <div className="flex items-center gap-2">
                {lastFailed && (
                  <button
                    onClick={retry}
                    className="flex items-center gap-1.5 rounded-lg border border-edge px-3 py-1.5 text-sm text-dim hover:text-accent"
                  >
                    <RotateCcw size={13} /> {t('chat.retry')}
                  </button>
                )}
                <button
                  onClick={submit}
                  disabled={!input.trim()}
                  className="flex items-center gap-1.5 rounded-lg bg-accent px-4 py-1.5 text-sm text-white hover:opacity-90 disabled:opacity-40"
                >
                  <Send size={13} /> {t('chat.send')}
                </button>
              </div>
            )}
          </div>
        </div>
        {confirmYolo && (
          <div className="fixed inset-x-0 bottom-0 top-11 z-40 grid place-items-center bg-black/60">
            <div className="w-[420px] rounded-2xl border border-[#d9a83c]/40 bg-panel p-5 shadow-2xl">
              <div className="flex items-center gap-2 text-sm font-semibold text-[#d9a83c]">
                <AlertTriangle size={16} />
                {t('chat.yoloConfirmTitle')}
              </div>
              <p className="mt-3 text-sm text-dim leading-relaxed">
                {t('chat.yoloConfirmBody')}
              </p>
              <div className="mt-4 flex justify-end gap-2">
                <button
                  onClick={() => setConfirmYolo(false)}
                  className="rounded-lg border border-edge px-4 py-1.5 text-sm text-dim hover:text-fg"
                >
                  {t('interact.cancel')}
                </button>
                <button
                  onClick={() => {
                    setConfirmYolo(false);
                    void setMode('yolo');
                  }}
                  className="rounded-lg bg-err px-4 py-1.5 text-sm text-white hover:opacity-90"
                >
                  {t('chat.confirmSwitch')}
                </button>
              </div>
            </div>
          </div>
        )}
      </div>
    </main>
  );
}
