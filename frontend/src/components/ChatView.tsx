import { memo, useEffect, useRef, useState } from 'react';
import {
  AlertTriangle,
  Archive,
  Bot,
  Check,
  ChevronDown,
  ChevronRight,
  ChevronUp,
  Copy,
  File,
  Flame,
  Folder,
  Lock,
  Loader2,
  Music2,
  Paperclip,
  Redo2,
  RotateCcw,
  Send,
  ShieldCheck,
  Sparkles,
  Square,
  Terminal,
  Undo2,
  Video,
  X,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { OnFileDrop, OnFileDropOff } from '../../wailsjs/runtime/runtime';
import { api } from '../lib/api';
import { COMPACT_SUMMARY_PREFIX } from '../lib/compact';
import { useStore } from '../lib/store';
import type { AttachmentView, SkillDTO, UndoState } from '../lib/types';
import type { AssistantItem, MessageView } from '../lib/store';
import { InteractionCard } from './InteractionCard';
import { ApplyPatchView, ToolCard, WriteView } from './ToolCard';
import { LiveMarkdown, looksLikeMarkdown, Markdown } from './Markdown';
import { ProjectTrustBanner } from './ProjectTrustBanner';

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

type ToolCallItem = Extract<AssistantItem, { kind: 'tool_call' }>;

// Tools rendered as always-visible full blocks are never folded into a
// consecutive-run group: their content matters in place (patch diffs,
// written file contents), not hidden behind a "Ran N tools" summary.
const nonGroupedTools = new Set(['apply_patch', 'write_file']);

// groupToolCalls merges consecutive tool calls into groups so a burst
// of tool executions renders as one collapsible block instead of a
// stack of cards. Non-tool items are passed through unchanged.
function groupToolCalls(
  items: AssistantItem[],
): (AssistantItem | ToolCallItem[])[] {
  const out: (AssistantItem | ToolCallItem[])[] = [];
  let cur: ToolCallItem[] | null = null;
  for (const item of items) {
    if (item.kind === 'tool_call' && !nonGroupedTools.has(item.tool.name)) {
      if (!cur) cur = [];
      cur.push(item);
    } else {
      if (cur) {
        out.push(cur);
        cur = null;
      }
      out.push(item);
    }
  }
  if (cur) out.push(cur);
  return out;
}

const isCommandTool = (name: string) =>
  name === 'exec_command' || name === 'exec_session';

// ToolGroupView renders a burst of consecutive tool calls as one
// collapsible block ("Ran 4 commands"), defaulting to collapsed; the
// individual tool cards appear once expanded.
function ToolGroupView({ tools }: { tools: ToolCallItem[] }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const allCommands = tools.every((ti) => isCommandTool(ti.tool.name));
  const running = tools.some((ti) => ti.tool.status === 'running');
  const failed = tools.some((ti) => ti.tool.status === 'error');
  const done = tools.filter((ti) => ti.tool.status === 'done').length;
  const label = allCommands
    ? t('chat.ranCommands', { count: tools.length })
    : t('chat.ranTools', { count: tools.length });

  return (
    <div className="my-1.5">
      <button
        onClick={() => setOpen(!open)}
        className={`flex w-full items-center gap-2 rounded-lg border px-3 py-2 text-left text-sm transition-colors ${
          failed
            ? 'border-err/40 bg-err/5'
            : running
              ? 'border-accent/40 bg-panel2'
              : 'border-edge bg-panel2'
        } hover:bg-panel2/70`}
      >
        {running ? (
          <Loader2 size={14} className="animate-spin shrink-0 text-accent" />
        ) : failed ? (
          <X size={14} className="shrink-0 text-err" />
        ) : (
          <Check size={14} className="shrink-0 text-ok" />
        )}
        {allCommands ? (
          <Terminal size={14} className="shrink-0 text-accent" />
        ) : (
          <Bot size={14} className="shrink-0 text-accent" />
        )}
        <span className="min-w-0 flex-1 truncate text-sm text-fg">{label}</span>
        {running && (
          <span className="shrink-0 text-xs text-dim tabular-nums">
            {done}/{tools.length}
          </span>
        )}
        {open ? (
          <ChevronDown size={14} className="shrink-0 text-dim" />
        ) : (
          <ChevronRight size={14} className="shrink-0 text-dim" />
        )}
      </button>
      {open && (
        <div className="mt-1 space-y-1.5">
          {tools.map((item) => (
            <ToolCard key={item.id} tool={item.tool} />
          ))}
        </div>
      )}
    </div>
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
    if (looksLikeMarkdown(text)) {
      return <LiveMarkdown text={text} />;
    }
    return <div className="prose-chat whitespace-pre-wrap text-sm">{text}</div>;
  }
  return (
    <div className="prose-chat text-sm">
      <Markdown text={text} />
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
  isTurnLast,
}: {
  msg: MessageView;
  busy: boolean;
  streaming: boolean;
  isTurnLast: boolean;
}) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  if (msg.role === 'user') {
    if (msg.text.startsWith(COMPACT_SUMMARY_PREFIX)) {
      return <CompactCard text={msg.text} />;
    }
    const attachments = msg.attachments ?? [];
    const images = attachments.filter((a) => a.kind === 'image');
    const files = attachments.filter((a) => a.kind !== 'image');
    return (
      <div className="flex justify-end">
        <div className="max-w-[80%] rounded-2xl rounded-br-sm border border-accent/30 bg-accent/15 px-4 py-2.5 text-sm">
          {images.length > 0 && (
            <div className="mb-2 flex flex-wrap justify-end gap-2">
              {images.map((a) => (
                <AttachmentImage key={a.id} att={a} />
              ))}
            </div>
          )}
          {msg.text && <div className="whitespace-pre-wrap">{msg.text}</div>}
          {files.length > 0 && <AttachmentFiles attachments={files} />}
        </div>
      </div>
    );
  }
  const finalText = msg.items
    .filter(
      (it): it is Extract<AssistantItem, { kind: 'text' }> =>
        it.kind === 'text',
    )
    .map((it) => it.text)
    .join('\n')
    .trim();
  const copyable = isTurnLast && !streaming && finalText.length > 0;
  const copyFinal = async () => {
    if (!finalText) return;
    try {
      await navigator.clipboard.writeText(finalText);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard unavailable
    }
  };
  return (
    <div className="flex flex-col gap-1">
      {groupToolCalls(msg.items).map((group, gi) => {
        if (Array.isArray(group)) {
          return group.length === 1 ? (
            group[0].tool.name === 'apply_patch' ? (
              <ApplyPatchView key={group[0].id} tool={group[0].tool} />
            ) : group[0].tool.name === 'write_file' ? (
              <WriteView key={group[0].id} tool={group[0].tool} />
            ) : (
              <ToolCard key={group[0].id} tool={group[0].tool} />
            )
          ) : (
            <ToolGroupView key={`group-${gi}`} tools={group} />
          );
        }
        switch (group.kind) {
          case 'reasoning':
            return <Reasoning key={group.id} text={group.text} />;
          case 'tool_call':
            // Non-grouped tools (apply_patch / write_file) arrive here
            // as standalone items.
            return group.tool.name === 'apply_patch' ? (
              <ApplyPatchView key={group.id} tool={group.tool} />
            ) : group.tool.name === 'write_file' ? (
              <WriteView key={group.id} tool={group.tool} />
            ) : (
              <ToolCard key={group.id} tool={group.tool} />
            );
          case 'text':
            return (
              <AssistantText
                key={group.id}
                text={group.text}
                streaming={streaming}
              />
            );
        }
      })}
      {copyable && (
        <div className="flex justify-end">
          <button
            onClick={() => void copyFinal()}
            className="flex items-center gap-1 rounded border border-edge px-1.5 py-0.5 text-[10px] text-dim hover:text-fg"
            aria-label={t('chat.copyOutput')}
          >
            {copied ? <Check size={11} /> : <Copy size={11} />}
            {copied ? t('chat.copied') : t('chat.copyOutput')}
          </button>
        </div>
      )}
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

// formatSize renders a byte count as a short human-readable size.
function formatSize(n: number) {
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`;
  if (n >= 1024) return `${Math.round(n / 1024)} KB`;
  return `${n} B`;
}

// AttachmentImage renders one image attachment. Live sends carry the
// data URL already; resumed history has only the stored path, so the
// component fetches the preview from the backend on mount (WKWebView
// cannot load file:// directly).
function AttachmentImage({ att }: { att: AttachmentView }) {
  const [url, setUrl] = useState(att.data_url ?? '');
  useEffect(() => {
    if (url) return;
    let live = true;
    void api
      .readAttachment(att.path)
      .then((dto) => {
        if (live) setUrl(dto.data_url ?? 'missing');
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [att.path, url]);
  if (url && url !== 'missing') {
    return (
      <img
        src={url}
        alt={att.name}
        className="max-h-44 max-w-64 rounded-lg border border-edge object-contain"
      />
    );
  }
  if (url === 'missing') {
    return (
      <div className="flex h-24 w-36 items-center justify-center rounded-lg border border-edge bg-panel2 px-2 text-center text-[10px] text-dim">
        <span className="truncate">{att.name}</span>
      </div>
    );
  }
  return (
    <div className="flex h-24 w-36 items-center justify-center rounded-lg border border-edge bg-panel2">
      <Loader2 size={14} className="animate-spin text-dim" />
    </div>
  );
}

// AttachmentFiles renders non-image attachments as a collapsed list
// below the message text: one toggle reveals the file chips.
function AttachmentFiles({ attachments }: { attachments: AttachmentView[] }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const FileIcon = (a: AttachmentView) =>
    a.kind === 'audio' ? Music2 : a.kind === 'video' ? Video : File;
  return (
    <div className="mt-2">
      <button
        onClick={() => setOpen(!open)}
        className="flex items-center gap-1.5 rounded-md border border-edge bg-panel2/70 px-2 py-1 text-xs text-dim hover:text-fg"
      >
        <Paperclip size={12} />
        {t('chat.files', { count: attachments.length })}
        {open ? <ChevronUp size={12} /> : <ChevronDown size={12} />}
      </button>
      {open && (
        <div className="mt-1.5 space-y-1">
          {attachments.map((a) => {
            const Icon = FileIcon(a);
            return (
              <div
                key={a.id}
                className="flex items-center gap-1.5 rounded-md border border-edge bg-panel2 px-2 py-1 text-xs"
              >
                <Icon size={12} className="shrink-0 text-dim" />
                <span className="min-w-0 truncate">{a.name}</span>
                {a.size != null && (
                  <span className="shrink-0 text-dim tabular-nums">
                    {formatSize(a.size)}
                  </span>
                )}
              </div>
            );
          })}
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
  const status = useStore((s) => s.status);
  const pendingInteracts = conv?.pendingInteracts ?? [];
  const send = useStore((s) => s.send);
  const cancelRun = useStore((s) => s.cancelRun);
  const clearLastFailed = useStore((s) => s.clearLastFailed);
  const subagentCards = useStore((s) => s.subagentCards);
  const subagentPanelOpen = useStore((s) => s.subagentPanelOpen);
  const toggleSubagentPanel = useStore((s) => s.toggleSubagentPanel);
  const retryLast = useStore((s) => s.retryLast);
  const mode = conv?.mode ?? 'workspace';
  const setMode = useStore((s) => s.setMode);
  const think = conv?.think ?? 'medium';
  const stage = conv?.stage ?? '';
  const setThink = useStore((s) => s.setThink);
  const model = conv?.model ?? '';
  const setModel = useStore((s) => s.setModel);
  const modelOptions = useStore((s) => s.modelOptions);
  // The think picker is only meaningful when the effective model
  // (explicit per-conversation hint, or the default router target when
  // empty) declares a reasoning capability.
  const thinkSupported = model
    ? (modelOptions.find((o) => o.id === model)?.reasoning ?? false)
    : (status?.default_reasoning ?? false);
  const lastFailed = conv?.lastFailed ?? false;
  const openConfig = useStore((s) => s.openConfig);
  const [input, setInput] = useState('');
  const [attachments, setAttachments] = useState<AttachmentView[]>([]);
  const [confirmYolo, setConfirmYolo] = useState(false);
  const [modeMenuOpen, setModeMenuOpen] = useState(false);
  const [undoAvail, setUndoAvail] = useState<UndoState>({
    can_undo: false,
    can_redo: false,
  });
  const [undoNotice, setUndoNotice] = useState('');
  const scrollRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const highlightRef = useRef<HTMLDivElement>(null);
  // Inline mention picker: "@query" searches workspace files,
  // "$query" completes skill names. Arrow keys move, Enter inserts,
  // Escape closes.
  type MentionKind = 'file' | 'skill';
  interface MentionItem {
    key: string;
    label: string;
    sub: string;
    isDir?: boolean;
    trigger: '@' | '$';
    insert: string;
  }
  const [mention, setMention] = useState<{
    kind: MentionKind;
    query: string;
    items: MentionItem[];
    active: number | null;
  } | null>(null);
  const fileSearchSeq = useRef(0);
  const fileSearchTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const skillsCache = useRef<SkillDTO[] | null>(null);
  // Stick-to-bottom: while the agent streams, the view follows the
  // latest output with an instant snap. Smooth-scrolling on every
  // delta races when the window is occluded (switching screens) and
  // the viewport visibly scrambles until the stream stops, so the
  // animation is avoided entirely. Scrolling up unpins; scrolling
  // back to the bottom re-pins.
  const [stick, setStick] = useState(true);
  // The ref is the source of truth for the pin decision inside the
  // scroll effect. Stream deltas and wheel events can land in the same
  // commit, and the effect must never snap with a stale "pinned" value
  // captured from an earlier render — that race is what made the view
  // jump back to the bottom right after the user scrolled away.
  const stickRef = useRef(true);
  // Tracks IME composition so pressing Enter to confirm a candidate
  // never sends the message (isComposing alone is unreliable in
  // WKWebView for the Enter key).
  const composingRef = useRef(false);
  const { t } = useTranslation();

  const addAttachmentPaths = async (paths: string[]) => {
    if (paths.length === 0) return;
    const next: AttachmentView[] = [];
    for (const [i, p] of paths.slice(0, 8).entries()) {
      try {
        const dto = await api.readAttachment(p);
        if (!dto.path) continue;
        const mt = dto.media_type ?? '';
        const kind: AttachmentView['kind'] = mt.startsWith('image/')
          ? 'image'
          : mt.startsWith('audio/')
            ? 'audio'
            : mt.startsWith('video/')
              ? 'video'
              : 'file';
        next.push({
          id: `att-${Date.now()}-${i}-${Math.random().toString(36).slice(2, 7)}`,
          kind,
          path: dto.path,
          name: dto.name,
          media_type: dto.media_type,
          size: dto.size,
          data_url: dto.data_url,
        });
      } catch {
        // Unreadable / too large files are skipped; the backend rejects
        // them again at send time.
      }
    }
    if (next.length > 0) {
      setAttachments((prev) => [...prev, ...next].slice(0, 8));
    }
  };

  const removeAttachment = (id: string) => {
    setAttachments((prev) => prev.filter((a) => a.id !== id));
  };

  const pickAttachment = async () => {
    try {
      const picked = await api.pickFile(t('chat.attach'), '');
      if (picked) void addAttachmentPaths([picked]);
    } catch {
      // cancelled
    }
  };
  const stageLabel =
    stage === 'reasoning'
      ? t('chat.stageReasoning')
      : stage.startsWith('tool:')
        ? t('chat.stageTool', { tool: stage.slice(5) })
        : t('chat.running');

  useEffect(() => {
    OnFileDrop((_x, _y, paths) => {
      if (!paths || paths.length === 0) return;
      void addAttachmentPaths(paths);
    }, true);
    return () => {
      OnFileDropOff();
    };
  }, []);

  useEffect(() => {
    if (!stickRef.current) return;
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [messages, pendingInteracts, stick]);

  // Refresh undo/redo availability when a turn finishes or the
  // conversation switches (captures happen on turn end).
  useEffect(() => {
    if (busy) return;
    void api
      .undoState()
      .then(setUndoAvail)
      .catch(() => {});
  }, [busy, current]);

  const runUndo = async (redo: boolean) => {
    try {
      const files = redo ? await api.redoChange() : await api.undoChange();
      setUndoNotice(
        redo
          ? t('chat.redoDone', { count: files.length })
          : t('chat.undoDone', { count: files.length }),
      );
      void api
        .undoState()
        .then(setUndoAvail)
        .catch(() => {});
    } catch {
      setUndoNotice(redo ? t('chat.redoEmpty') : t('chat.undoEmpty'));
    }
    window.setTimeout(() => setUndoNotice(''), 3000);
  };

  useEffect(() => {
    const onVisibility = () => {
      if (document.visibilityState === 'visible' && stickRef.current) {
        const el = scrollRef.current;
        if (el) el.scrollTop = el.scrollHeight;
      }
    };
    document.addEventListener('visibilitychange', onVisibility);
    return () => document.removeEventListener('visibilitychange', onVisibility);
  }, []);

  const submit = () => {
    if ((!input.trim() && attachments.length === 0) || busy) return;
    stickRef.current = true;
    setStick(true);
    const text = input;
    setInput('');
    const staged = attachments;
    setAttachments([]);
    void send(text, staged);
  };

  const retry = () => {
    setInput('');
    void retryLast();
  };

  const onInputChange = (value: string) => {
    setInput(value);
    const fileMatch = value.match(/(?:^|\s)@([\w./-]*)$/);
    const skillMatch = value.match(/(?:^|\s)\$([a-z0-9-]*)$/i);
    if (fileMatch) {
      const query = fileMatch[1];
      setMention((m) =>
        m && m.kind === 'file'
          ? { ...m, query }
          : { kind: 'file', query, items: [], active: null },
      );
      if (fileSearchTimer.current) clearTimeout(fileSearchTimer.current);
      fileSearchTimer.current = setTimeout(() => {
        const seq = ++fileSearchSeq.current;
        void api
          .searchFiles(query)
          .then((hits) => {
            if (seq !== fileSearchSeq.current) return;
            const list = Array.isArray(hits) ? hits : [];
            setMention((m) =>
              m && m.kind === 'file'
                ? {
                    ...m,
                    items: list.map((h) => ({
                      key: h.path,
                      label: h.path,
                      sub: '',
                      isDir: h.is_dir,
                      trigger: '@' as const,
                      insert: '@' + h.path,
                    })),
                  }
                : m,
            );
          })
          .catch(() => {
            if (seq === fileSearchSeq.current) {
              setMention((m) =>
                m && m.kind === 'file' ? { ...m, items: [] } : m,
              );
            }
          });
      }, 120);
    } else if (skillMatch) {
      const query = skillMatch[1];
      setMention((m) =>
        m && m.kind === 'skill'
          ? { ...m, query }
          : { kind: 'skill', query, items: [], active: null },
      );
      const apply = (skills: SkillDTO[]) => {
        const q = query.toLowerCase();
        setMention((m) =>
          m && m.kind === 'skill'
            ? {
                ...m,
                items: skills
                  .filter((s) => s.name.toLowerCase().includes(q))
                  .map((s) => ({
                    key: s.name,
                    label: s.name,
                    sub: s.description,
                    trigger: '$' as const,
                    insert: '$' + s.name,
                  })),
              }
            : m,
        );
      };
      if (skillsCache.current) {
        apply(skillsCache.current);
      } else {
        void api
          .skills()
          .then((skills) => {
            skillsCache.current = skills;
            apply(skills);
          })
          .catch(() => {
            skillsCache.current = [];
            setMention((m) =>
              m && m.kind === 'skill' ? { ...m, items: [] } : m,
            );
          });
      }
    } else {
      setMention(null);
    }
  };

  // Grow the composer with the content up to a max height; shrink back
  // when the text is cleared.
  useEffect(() => {
    const el = inputRef.current;
    if (!el) return;
    el.style.height = 'auto';
    el.style.height = `${Math.min(el.scrollHeight, 208)}px`;
  }, [input]);

  const insertMention = (item: MentionItem) => {
    const re =
      item.trigger === '@'
        ? /(?:^|\s)@([\w./-]*)$/
        : /(?:^|\s)\$([a-z0-9-]*)$/i;
    const match = input.match(re);
    let next: string;
    if (match) {
      const at = match.index! + match[0].indexOf(item.trigger);
      next = input.slice(0, at) + item.insert;
    } else {
      next = input + item.insert;
    }
    setInput(next + ' ');
    setMention(null);
    inputRef.current?.focus();
  };

  // renderHighlightedInput colors @/$ mention tokens in the composer
  // mirror layer; the textarea itself renders transparent text so the
  // highlight shows through while selection stays native.
  const renderHighlightedInput = (text: string) => {
    const nodes: React.ReactNode[] = [];
    const re = /@[\w./-]+|\$[a-z0-9-]+/gi;
    let last = 0;
    let m: RegExpExecArray | null;
    let key = 0;
    while ((m = re.exec(text)) !== null) {
      if (m.index > last) nodes.push(text.slice(last, m.index));
      nodes.push(
        <span key={key++} className="text-accent">
          {m[0]}
        </span>,
      );
      last = m.index + m[0].length;
    }
    if (last < text.length) nodes.push(text.slice(last));
    return nodes;
  };

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
            <Loader2 size={12} className="animate-spin" /> {stageLabel}
          </span>
        )}
        <span className="flex-1" />
        {undoNotice && (
          <span className="mr-1 text-xs text-dim">{undoNotice}</span>
        )}
        <button
          onClick={() => void runUndo(false)}
          disabled={!undoAvail.can_undo}
          title={t('chat.undo')}
          aria-label={t('chat.undo')}
          className="rounded-lg border border-edge p-1.5 text-dim transition-colors hover:text-fg disabled:opacity-40"
        >
          <Undo2 size={13} />
        </button>
        <button
          onClick={() => void runUndo(true)}
          disabled={!undoAvail.can_redo}
          title={t('chat.redo')}
          aria-label={t('chat.redo')}
          className="ml-1 rounded-lg border border-edge p-1.5 text-dim transition-colors hover:text-fg disabled:opacity-40"
        >
          <Redo2 size={13} />
        </button>
        {subagentCards.length > 0 && (
          <button
            onClick={toggleSubagentPanel}
            className={`flex items-center gap-1.5 rounded-lg border px-2 py-1 text-xs transition-colors ${
              subagentPanelOpen
                ? 'border-subagent/40 bg-subagent/10 text-subagent'
                : 'border-edge text-dim hover:text-fg'
            }`}
            title={t('subagent.toggle')}
            aria-label={t('subagent.toggle')}
          >
            <Bot size={13} />
            {subagentCards.length}
          </button>
        )}
      </header>

      <ProjectTrustBanner />

      <div
        ref={scrollRef}
        onScroll={() => {
          const el = scrollRef.current;
          if (!el) return;
          const pinned = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
          stickRef.current = pinned;
          setStick(pinned);
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
          <div className="max-w-4xl mx-auto space-y-4">
            {messages.map((msg, i) => (
              <MessageRow
                key={msg.id}
                msg={msg}
                busy={busy}
                isTurnLast={
                  msg.role === 'assistant' &&
                  (i === messages.length - 1 ||
                    messages[i + 1]?.role === 'user')
                }
                streaming={
                  busy && msg.role === 'assistant' && i === messages.length - 1
                }
              />
            ))}
            {pendingInteracts.map((spec) => (
              <InteractionCard key={spec.id} spec={spec} />
            ))}
            {lastFailed && !busy && (
              <div className="flex items-center justify-between gap-3 rounded-xl border border-err/40 bg-err/10 px-4 py-3 text-sm">
                <span className="text-dim">{t('chat.lastFailed')}</span>
                <div className="flex items-center gap-2">
                  <button
                    onClick={retry}
                    className="flex items-center gap-1.5 rounded-lg border border-edge px-3 py-1.5 text-dim hover:text-accent"
                  >
                    <RotateCcw size={13} /> {t('chat.retry')}
                  </button>
                  <button
                    onClick={clearLastFailed}
                    className="rounded-lg px-3 py-1.5 text-dim hover:text-fg"
                  >
                    {t('chat.dismiss')}
                  </button>
                </div>
              </div>
            )}
          </div>
        )}
      </div>

      <div className="shrink-0 px-6 pb-4">
        <div className="max-w-4xl mx-auto rounded-xl border border-edge bg-panel focus-within:border-accent/60 transition-colors">
          <div
            className={`flex items-center gap-2 rounded-t-xl border-b px-3 py-1.5 text-xs transition-colors ${
              yolo ? 'border-yolo/40 bg-yolo/10' : 'border-transparent'
            }`}
          >
            <div className="relative">
              <button
                onClick={() => setModeMenuOpen((v) => !v)}
                className={`flex items-center gap-1.5 rounded-md border px-2 py-0.5 transition-colors ${
                  yolo
                    ? 'border-yolo/50 bg-yolo/15 text-yolo hover:bg-yolo/25'
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
                          ? 'bg-yolo/15 text-yolo'
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
              <span className="flex items-center gap-1 text-yolo">
                <AlertTriangle size={12} />
                {t('chat.yoloBanner')}
              </span>
            )}
          </div>
          {attachments.length > 0 && (
            <div className="flex flex-wrap gap-2 px-3 pt-2">
              {attachments.map((a) => (
                <div key={a.id} className="group relative">
                  {a.kind === 'image' && a.data_url ? (
                    <img
                      src={a.data_url}
                      alt={a.name}
                      className="h-16 w-16 rounded-lg border border-edge object-cover"
                    />
                  ) : (
                    <div className="flex h-16 w-16 flex-col items-center justify-center gap-1 rounded-lg border border-edge bg-panel2 p-1 text-[10px] text-dim">
                      <File size={16} className="shrink-0" />
                      <span className="w-full truncate text-center">
                        {a.name}
                      </span>
                    </div>
                  )}
                  <button
                    onClick={() => removeAttachment(a.id)}
                    aria-label={t('chat.removeAttachment')}
                    className="absolute -right-1.5 -top-1.5 rounded-full bg-err p-0.5 text-white opacity-80 hover:opacity-100"
                  >
                    <X size={10} />
                  </button>
                </div>
              ))}
            </div>
          )}
          <div className="relative">
            <div
              ref={highlightRef}
              aria-hidden="true"
              className="pointer-events-none absolute inset-0 overflow-hidden whitespace-pre-wrap break-words px-4 pt-3 text-sm text-fg"
            >
              {renderHighlightedInput(input)}
            </div>
            <textarea
              ref={inputRef}
              value={input}
              onChange={(e) => onInputChange(e.target.value)}
              onCompositionStart={() => (composingRef.current = true)}
              onCompositionEnd={() => (composingRef.current = false)}
              onScroll={() => {
                if (highlightRef.current) {
                  highlightRef.current.scrollTop =
                    inputRef.current?.scrollTop ?? 0;
                }
              }}
              onKeyDown={(e) => {
                if (mention && e.key === 'Escape') {
                  setMention(null);
                  return;
                }
                if (
                  mention &&
                  mention.items.length > 0 &&
                  (e.key === 'ArrowDown' || e.key === 'ArrowUp')
                ) {
                  e.preventDefault();
                  const delta = e.key === 'ArrowDown' ? 1 : -1;
                  setMention((m) =>
                    m
                      ? {
                          ...m,
                          active:
                            m.active == null
                              ? delta === 1
                                ? 0
                                : m.items.length - 1
                              : (m.active + delta + m.items.length) %
                                m.items.length,
                        }
                      : m,
                  );
                  return;
                }
                if (mention && e.key === 'Enter' && !e.shiftKey) {
                  const item = mention.items[mention.active ?? 0];
                  if (item) {
                    e.preventDefault();
                    insertMention(item);
                    return;
                  }
                }
                if (mention && e.key === 'Enter') {
                  e.preventDefault();
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
              className="relative max-h-52 w-full resize-none overflow-y-auto bg-transparent px-4 pt-3 text-sm text-transparent caret-fg outline-none disabled:opacity-50"
            />
          </div>
          {mention && (
            <div className="mx-3 mb-2 max-h-48 overflow-y-auto rounded-lg border border-edge bg-panel2 shadow-xl">
              {mention.items.length === 0 ? (
                <div className="px-3 py-2 text-xs text-dim">
                  {mention.query
                    ? t('chat.mentionNoMatch')
                    : mention.kind === 'file'
                      ? t('chat.mentionSearchHint')
                      : t('chat.mentionSkillHint')}
                </div>
              ) : (
                mention.items.slice(0, 12).map((n, i) => (
                  <button
                    key={n.key}
                    onClick={() => insertMention(n)}
                    onMouseEnter={() =>
                      setMention((m) => (m ? { ...m, active: i } : m))
                    }
                    className={`w-full flex items-center gap-2 px-3 py-1.5 text-left text-sm ${
                      i === mention.active
                        ? 'bg-accent/15 text-accent'
                        : 'hover:bg-panel'
                    }`}
                  >
                    {n.trigger === '@' ? (
                      n.isDir ? (
                        <Folder size={13} className="text-accent shrink-0" />
                      ) : (
                        <File size={13} className="text-dim shrink-0" />
                      )
                    ) : (
                      <Sparkles size={13} className="text-accent shrink-0" />
                    )}
                    <span className="shrink-0">{n.label}</span>
                    {n.sub && (
                      <span className="truncate text-xs text-dim">{n.sub}</span>
                    )}
                  </button>
                ))
              )}
            </div>
          )}
          <div className="flex items-center justify-between px-3 pb-2.5">
            <div className="flex items-center gap-3">
              <button
                onClick={() => void pickAttachment()}
                disabled={!configured || busy}
                title={t('chat.attach')}
                aria-label={t('chat.attach')}
                className="flex items-center gap-1 rounded-lg border border-edge px-2.5 py-1 text-xs text-dim hover:text-fg disabled:opacity-50"
              >
                <Paperclip size={13} />
              </button>
              {thinkSupported && (
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
              )}
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
            <div className="flex items-center gap-2">
              <span className="text-dim hidden sm:inline text-xs">
                {busy ? t('chat.runningHint') : t('chat.enterHint')}
              </span>
              {busy ? (
                <button
                  onClick={() => void cancelRun()}
                  className="flex items-center gap-1.5 rounded-lg border border-edge px-3 py-1.5 text-sm text-err hover:bg-panel2"
                >
                  <Square size={13} /> {t('chat.stop')}
                </button>
              ) : (
                <button
                  onClick={submit}
                  disabled={!input.trim() && attachments.length === 0}
                  className="flex items-center gap-1.5 rounded-lg bg-accent px-4 py-1.5 text-sm text-white hover:opacity-90 disabled:opacity-40"
                >
                  <Send size={13} /> {t('chat.send')}
                </button>
              )}
            </div>
          </div>
        </div>
        {confirmYolo && (
          <div className="fixed inset-x-0 bottom-0 top-11 z-40 grid place-items-center bg-black/60">
            <div className="w-[420px] rounded-2xl border border-yolo/40 bg-panel p-5 shadow-2xl">
              <div className="flex items-center gap-2 text-sm font-semibold text-yolo">
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
