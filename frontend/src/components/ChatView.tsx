import {
  Fragment,
  memo,
  useEffect,
  useMemo,
  useRef,
  useState,
  type MouseEvent,
} from 'react';
import {
  AlertTriangle,
  Archive,
  ArrowLeft,
  ArrowUp,
  Bot,
  Check,
  ChevronDown,
  ChevronRight,
  ChevronUp,
  Copy,
  File,
  FileArchive,
  FileCode,
  FileImage,
  FileSpreadsheet,
  FileText,
  Film,
  Flame,
  FolderOpen,
  Globe,
  Lock,
  Loader2,
  Music2,
  Package,
  Paperclip,
  Presentation,
  RotateCcw,
  ShieldCheck,
  Sparkles,
  Square,
  Terminal,
  Video,
  X,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { OnFileDrop, OnFileDropOff } from '../../wailsjs/runtime/runtime';
import { api } from '../lib/api';
import { COMPACT_SUMMARY_PREFIX } from '../lib/compact';
import { useStore } from '../lib/store';
import { useConversationState, useFocusState } from '../state/react';
import type { AttachmentView } from '../lib/types';
import type {
  AssistantItem,
  MessageView,
  TurnArtifacts,
  TurnDoc,
} from '../lib/store';
import { InteractionCard } from './InteractionCard';
import {
  MarkdownComposer,
  type MarkdownComposerHandle,
} from './MarkdownComposer';
import { Markdown } from './Markdown';
import { PlanPanel } from './PlanPanel';
import { ToolCard } from './ToolCard';
import { ProjectTrustBanner } from './ProjectTrustBanner';
import { StreamItemView } from './StreamItemView';
import { latestPlan } from '../lib/plan';
import { groupToolCalls, type ToolCallItem } from '../lib/stream';

const isCommandTool = (name: string) =>
  name === 'exec_command' || name === 'exec_session';

// RENDER_WINDOW bounds the number of transcript messages mounted in the
// DOM. Older messages remain in the store/archive; "load earlier"
// grows the window in steps instead of rendering a long session at
// once.
const RENDER_WINDOW = 200;
const RENDER_STEP = 100;

// ToolGroupView renders a burst of consecutive tool calls as one
// collapsible block ("Ran 4 commands"), defaulting to collapsed; the
// individual tool cards appear once expanded.
const ToolGroupView = memo(
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
            <Loader2
              size="1.0000rem"
              className="animate-spin shrink-0 text-accent"
            />
          ) : failed ? (
            <X size="1.0000rem" className="shrink-0 text-err" />
          ) : (
            <Check size="1.0000rem" className="shrink-0 text-ok" />
          )}
          {allCommands ? (
            <Terminal size="1.0000rem" className="shrink-0 text-accent" />
          ) : (
            <Bot size="1.0000rem" className="shrink-0 text-accent" />
          )}
          <span className="min-w-0 flex-1 truncate text-sm text-fg">
            {label}
          </span>
          {running && (
            <span className="shrink-0 text-xs text-dim tabular-nums">
              {done}/{tools.length}
            </span>
          )}
          {open ? (
            <ChevronDown size="1.0000rem" className="shrink-0 text-dim" />
          ) : (
            <ChevronRight size="1.0000rem" className="shrink-0 text-dim" />
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
  },
  (prev, next) => sameToolGroup(prev.tools, next.tools),
);

function sameToolGroup(a: ToolCallItem[], b: ToolCallItem[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    const x = a[i].tool;
    const y = b[i].tool;
    if (
      a[i].id !== b[i].id ||
      x.id !== y.id ||
      x.name !== y.name ||
      x.status !== y.status ||
      x.result !== y.result
    ) {
      return false;
    }
  }
  return true;
}

// MessageRow renders one conversation message. Memoized so stream
// deltas only re-render the message that changed instead of reparsing
// every completed message on each token.
const MessageRow = memo(function MessageRow({
  msg,
  busy,
  streaming,
  isTurnLast,
  requestedAt,
  startedAt,
}: {
  msg: MessageView;
  busy: boolean;
  streaming: boolean;
  isTurnLast: boolean;
  requestedAt?: string;
  startedAt?: string;
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
        <div className="group flex w-fit max-w-[80%] flex-col items-end gap-1">
          <div className="rounded-2xl rounded-br-sm border border-accent/30 bg-accent/15 px-4 py-2.5 text-sm">
            {images.length > 0 && (
              <div className="mb-2 flex flex-wrap justify-end gap-2">
                {images.map((a) => (
                  <AttachmentImage key={a.id} att={a} />
                ))}
              </div>
            )}
            {msg.text && (
              <div className="prose-chat user-bubble-md text-sm">
                <Markdown text={msg.text} />
              </div>
            )}
            {files.length > 0 && <AttachmentFiles attachments={files} />}
          </div>
          <div className="pointer-events-none invisible flex items-center gap-1.5 pr-1 group-hover:pointer-events-auto group-hover:visible">
            {requestedAt && (
              <span className="text-xs text-dim tabular-nums">
                {formatChatTime(requestedAt)}
              </span>
            )}
            {msg.text && (
              <button
                onClick={() => {
                  void (async () => {
                    try {
                      await navigator.clipboard.writeText(msg.text);
                      setCopied(true);
                      setTimeout(() => setCopied(false), 1500);
                    } catch {
                      // clipboard unavailable
                    }
                  })();
                }}
                className="flex items-center rounded border border-edge p-1 text-dim hover:text-fg"
                aria-label={copied ? t('chat.copied') : t('chat.copyMessage')}
                tabIndex={-1}
              >
                {copied ? (
                  <Check size="0.7857rem" />
                ) : (
                  <Copy size="0.7857rem" />
                )}
              </button>
            )}
          </div>
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
  // update_plan calls render once in the top-left plan panel, so a
  // message holding only those calls collapses to nothing — skip it
  // instead of leaving an empty row in the transcript.
  const groups = groupToolCalls(msg.items);
  if (groups.length === 0) return null;
  return (
    <div className="group flex flex-col gap-1">
      {groups.map((group, gi) => {
        if (Array.isArray(group)) {
          return group.length === 1 ? (
            <StreamItemView
              key={group[0].id}
              item={group[0]}
              variant="chat"
              streaming={streaming}
            />
          ) : (
            <ToolGroupView key={`group-${gi}`} tools={group} />
          );
        }
        return (
          <StreamItemView
            key={group.id}
            item={group}
            variant="chat"
            streaming={streaming}
          />
        );
      })}
      {copyable && (
        <div className="pointer-events-none invisible flex items-center justify-start gap-1.5 pl-1 group-hover:pointer-events-auto group-hover:visible">
          <button
            onClick={() => void copyFinal()}
            className="flex items-center rounded border border-edge p-1 text-dim hover:text-fg"
            aria-label={copied ? t('chat.copied') : t('chat.copyOutput')}
            tabIndex={-1}
          >
            {copied ? <Check size="0.7857rem" /> : <Copy size="0.7857rem" />}
          </button>
          {startedAt && (
            <span className="text-xs text-dim tabular-nums">
              {formatChatTime(startedAt)}
            </span>
          )}
        </div>
      )}
      {msg.items.length === 0 && busy && (
        <div className="flex items-center gap-2 py-1 text-sm text-dim">
          <Loader2 size="1.0000rem" className="animate-spin" />
          {t('chat.thinking')}
        </div>
      )}
    </div>
  );
});

// formatChatTime renders a message timestamp as a compact local clock
// time. Older-than-today messages include the date so a resumed
// session's timeline stays unambiguous.
function formatChatTime(iso?: string) {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const sameDay = d.toDateString() === new Date().toDateString();
  return d.toLocaleString(undefined, {
    month: sameDay ? undefined : 'numeric',
    day: sameDay ? undefined : 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

// turnForIndex maps a transcript index to the turn entry that owns it.
// Entries are ordered by first-message index, so the closest preceding
// start is the owning turn.
function turnForIndex(
  turnArtifacts: TurnArtifacts[],
  index: number,
): TurnArtifacts | undefined {
  for (let ti = turnArtifacts.length - 1; ti >= 0; ti--) {
    if (turnArtifacts[ti].start <= index) return turnArtifacts[ti];
  }
  return undefined;
}

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
        <Archive size="1.0000rem" className="shrink-0 text-dim" />
        <span>{t('tool.compacted')}</span>
        <span className="flex-1" />
        <span className="text-xs text-dim">{t('tool.done')}</span>
        {open ? (
          <ChevronDown size="1.0000rem" className="shrink-0 text-dim" />
        ) : (
          <ChevronRight size="1.0000rem" className="shrink-0 text-dim" />
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

// docIcon maps a file extension to a themed icon for the artifact
// strip.
function docIcon(path: string) {
  const ext = path.split('.').pop()?.toLowerCase() ?? '';
  if (['md', 'markdown', 'txt', 'rst', 'doc', 'docx', 'pdf'].includes(ext)) {
    return <FileText size="0.9286rem" className="text-accent" />;
  }
  if (['ppt', 'pptx', 'key'].includes(ext)) {
    return <Presentation size="0.9286rem" className="text-warn" />;
  }
  if (['xls', 'xlsx', 'csv'].includes(ext)) {
    return <FileSpreadsheet size="0.9286rem" className="text-ok" />;
  }
  if (['png', 'jpg', 'jpeg', 'webp', 'gif', 'svg', 'bmp'].includes(ext)) {
    return <FileImage size="0.9286rem" className="text-accent" />;
  }
  if (['mp4', 'mov', 'webm', 'mkv', 'avi'].includes(ext)) {
    return <Film size="0.9286rem" className="text-warn" />;
  }
  if (['zip', 'gz', 'tar', '7z', 'rar'].includes(ext)) {
    return <FileArchive size="0.9286rem" className="text-dim" />;
  }
  if (
    [
      'go',
      'ts',
      'tsx',
      'js',
      'jsx',
      'py',
      'rs',
      'java',
      'c',
      'cpp',
      'h',
      'html',
      'css',
      'json',
      'yaml',
      'yml',
      'sh',
      'sql',
    ].includes(ext)
  ) {
    return <FileCode size="0.9286rem" className="text-dim" />;
  }
  return <File size="0.9286rem" className="text-dim" />;
}

// absoluteArtifactPath joins workspace-relative artifact paths with the
// current workspace so "Copy Path" always puts a usable absolute path
// on the clipboard. Already-absolute paths are left untouched.
function absoluteArtifactPath(path: string, workspace: string | undefined) {
  if (!workspace) return path;
  const absolute =
    path.startsWith('/') ||
    path.startsWith('\\') ||
    /^[A-Za-z]:[\\/]/.test(path);
  if (absolute) return path;
  const sep = workspace.includes('\\') ? '\\' : '/';
  const root = workspace.replace(/[\\/]+$/, '');
  return (
    root +
    sep +
    path
      .split(/[\\/]+/)
      .filter(Boolean)
      .join(sep)
  );
}

// workedForLabel renders the turn's execution time as "1h 2m 3s",
// preferring the backend-computed duration when available.
function workedForLabel(
  startedAt?: string,
  finishedAt?: string,
  durationMs?: number,
) {
  let ms = durationMs;
  if (ms === undefined && startedAt && finishedAt) {
    const start = new Date(startedAt).getTime();
    const end = new Date(finishedAt).getTime();
    if (Number.isFinite(start) && Number.isFinite(end)) {
      ms = end - start;
    }
  }
  if (ms === undefined || !Number.isFinite(ms) || ms < 0) return '';
  const total = Math.floor(ms / 1000);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const parts = [];
  if (h > 0) parts.push(`${h}h`);
  if (m > 0 || h > 0) parts.push(`${m}m`);
  parts.push(`${s}s`);
  return parts.join(' ');
}

// WorkedForLine renders the execution-time label below the turn
// artifact card, centered between two filling divider lines.
function WorkedForLine({
  startedAt,
  finishedAt,
  durationMs,
}: {
  startedAt?: string;
  finishedAt?: string;
  durationMs?: number;
}) {
  const { t } = useTranslation();
  const worked = workedForLabel(startedAt, finishedAt, durationMs);
  if (!worked) return null;
  return (
    <div className="flex items-center gap-3 text-xs text-dim">
      <div className="h-px flex-1 bg-edge" />
      <span className="shrink-0 tabular-nums">
        {t('chat.workedFor', { duration: worked })}
      </span>
      <div className="h-px flex-1 bg-edge" />
    </div>
  );
}

// ArtifactStrip renders the current turn's produced files as a
// left-to-right horizontal scroll strip: one chip per file, clickable
// to open with the system default app and with a right-click menu for
// save-as, copying the path, revealing the file, and open-with. It
// fills in live while the turn runs and stays after the turn ends.
const ArtifactStrip = memo(function ArtifactStrip({
  docs,
}: {
  docs: TurnDoc[];
}) {
  const { t } = useTranslation();
  const workspace = useStore((s) => s.workspace);
  const scrollRef = useRef<HTMLDivElement>(null);
  const [menu, setMenu] = useState<{
    doc: TurnDoc;
    x: number;
    y: number;
  } | null>(null);
  useEffect(() => {
    // Keep the newest artifact visible: the strip fills left-to-right
    // and auto-scrolls to the right edge on new files.
    const el = scrollRef.current;
    if (el) el.scrollLeft = el.scrollWidth;
  }, [docs]);
  const closeMenu = () => setMenu(null);
  const openMenu = (event: MouseEvent<HTMLButtonElement>, doc: TurnDoc) => {
    event.preventDefault();
    const menuWidth = 220;
    const menuHeight = 160;
    setMenu({
      doc,
      x: Math.min(event.clientX, window.innerWidth - menuWidth - 8),
      y: Math.min(event.clientY, window.innerHeight - menuHeight - 8),
    });
  };
  const runMenuAction = async (action: () => Promise<unknown>) => {
    try {
      await action();
    } catch (err) {
      console.error('opencraft artifact action failed:', err);
    } finally {
      closeMenu();
    }
  };
  const revealLabel = /Macintosh|Mac OS X/i.test(navigator.userAgent)
    ? t('chat.artifactRevealMac')
    : /Windows/i.test(navigator.userAgent)
      ? t('chat.artifactRevealWindows')
      : t('chat.artifactRevealLinux');
  return (
    <div className="rounded-xl border border-edge bg-panel2 p-3 my-3">
      <div className="mb-2 flex items-center gap-2 text-xs text-dim">
        <Package size="0.9286rem" className="text-accent" />
        <span className="font-medium text-fg">{t('chat.turnArtifacts')}</span>
        <span className="rounded bg-panel px-1.5 py-0.5 tabular-nums">
          {docs.length}
        </span>
      </div>
      <div
        ref={scrollRef}
        className="flex gap-1.5 overflow-x-auto snap-x pb-0.5"
      >
        {docs.map((doc) => {
          const name = doc.path.split('/').pop() || doc.path;
          return (
            <button
              key={doc.path}
              onClick={() => void api.openPath(doc.path)}
              onContextMenu={(e) => openMenu(e, doc)}
              title={t('chat.openArtifact', { path: doc.path })}
              aria-haspopup="menu"
              className="flex max-w-56 shrink-0 snap-start items-center gap-1.5 rounded-lg border border-edge bg-panel px-2.5 py-1.5 text-xs text-fg transition-colors hover:border-accent/50 hover:bg-panel2"
            >
              {docIcon(doc.path)}
              <span className="truncate">{name}</span>
            </button>
          );
        })}
      </div>
      {menu && (
        <>
          <div
            className="fixed inset-0 z-50"
            onMouseDown={closeMenu}
            onContextMenu={(e) => {
              e.preventDefault();
              closeMenu();
            }}
          />
          <div
            role="menu"
            className="fixed z-[60] min-w-52 rounded-lg border border-edge bg-panel p-1 shadow-xl"
            style={{ left: menu.x, top: menu.y }}
            onContextMenu={(e) => e.preventDefault()}
          >
            <button
              role="menuitem"
              className="flex w-full items-center rounded-md px-2.5 py-1.5 text-left text-xs text-fg hover:bg-panel2"
              onClick={() =>
                void runMenuAction(() => api.openArtifactWith(menu.doc.path))
              }
            >
              {t('chat.artifactOpenWith')}
            </button>
            <div role="separator" className="my-1 border-t border-edge" />
            <button
              role="menuitem"
              className="flex w-full items-center rounded-md px-2.5 py-1.5 text-left text-xs text-fg hover:bg-panel2"
              onClick={() =>
                void runMenuAction(() => api.saveArtifactAs(menu.doc.path))
              }
            >
              {t('chat.artifactSaveAs')}
            </button>
            <button
              role="menuitem"
              className="flex w-full items-center rounded-md px-2.5 py-1.5 text-left text-xs text-fg hover:bg-panel2"
              onClick={() =>
                void runMenuAction(async () => {
                  await navigator.clipboard.writeText(
                    absoluteArtifactPath(menu.doc.path, workspace),
                  );
                })
              }
            >
              {t('chat.artifactCopyPath')}
            </button>
            <button
              role="menuitem"
              className="flex w-full items-center rounded-md px-2.5 py-1.5 text-left text-xs text-fg hover:bg-panel2"
              onClick={() =>
                void runMenuAction(() => api.revealArtifact(menu.doc.path))
              }
            >
              {revealLabel}
            </button>
          </div>
        </>
      )}
    </div>
  );
});

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
      <div className="flex h-24 w-36 items-center justify-center rounded-lg border border-edge bg-panel2 px-2 text-center text-[0.7143rem] text-dim">
        <span className="truncate">{att.name}</span>
      </div>
    );
  }
  return (
    <div className="flex h-24 w-36 items-center justify-center rounded-lg border border-edge bg-panel2">
      <Loader2 size="1.0000rem" className="animate-spin text-dim" />
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
        <Paperclip size="0.8571rem" />
        {t('chat.files', { count: attachments.length })}
        {open ? (
          <ChevronUp size="0.8571rem" />
        ) : (
          <ChevronDown size="0.8571rem" />
        )}
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
                <Icon size="0.8571rem" className="shrink-0 text-dim" />
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
  const focus = useFocusState();
  const current = focus.name === 'active' ? focus.sessionID : '';
  const conv = useStore((s) => {
    return current ? s.conversations[current] : undefined;
  });
  const conversationState = useConversationState(current);
  const sessions = useStore((s) => s.sessions);
  const messages = conv?.messages ?? [];
  const turnState = conversationState?.turn;
  const busy =
    turnState?.name === 'starting' || turnState?.name === 'running';
  const turnArtifacts = conv?.turnArtifacts ?? [];
  const [visibleCount, setVisibleCount] = useState(RENDER_WINDOW);
  const [loadingEarlier, setLoadingEarlier] = useState(false);
  // A conversation switch resets the window to the tail; a resumed
  // session starts with the newest messages visible.
  useEffect(() => {
    setVisibleCount(RENDER_WINDOW);
    setLoadingEarlier(false);
  }, [current]);
  const truncated = messages.length > visibleCount;
  const start = truncated ? messages.length - visibleCount : 0;
  const visibleMessages = truncated ? messages.slice(start) : messages;
  const planState = useMemo(() => latestPlan(messages), [messages]);
  const [planDismissed, setPlanDismissed] = useState(false);
  const planItemsKey = planState
    ? planState.plan.items.map((s) => `${s.status}|${s.step}`).join('\n')
    : '';
  // A dismissed plan panel reappears as soon as fresh live progress
  // arrives, so collapsing it never hides a running plan.
  useEffect(() => {
    if (planState?.live) setPlanDismissed(false);
    // planItemsKey is the stable identity of the plan content; the
    // live flag is checked separately because a new running update
    // must also resurface the panel.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [planItemsKey, planState?.live]);
  const configured = useStore((s) => s.configured);
  const status = useStore((s) => s.status);
  const pendingInteracts = conv?.pendingInteracts ?? [];
  const send = useStore((s) => s.send);
  const newChat = useStore((s) => s.newChat);
  const resume = useStore((s) => s.resume);
  const retryTranscript = useStore((s) => s.retryTranscript);
  const backFromFailure = useStore((s) => s.backFromFailure);
  const cancelRun = useStore((s) => s.cancelRun);
  const clearLastFailed = useStore((s) => s.clearLastFailed);
  const subagentCards = useStore((s) => s.subagentCards);
  const subagentPanelOpen = useStore((s) => s.subagentPanelOpen);
  const toggleSubagentPanel = useStore((s) => s.toggleSubagentPanel);
  const retryLast = useStore((s) => s.retryLast);
  const mode = conv?.mode ?? 'workspace';
  const setMode = useStore((s) => s.setMode);
  const think = conv?.think ?? 'medium';
  const stage = turnState?.name === 'running' ? turnState.stage : '';
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
  const lastFailed = turnState?.name === 'failed';
  const openConfig = useStore((s) => s.openConfig);
  const [input, setInput] = useState('');
  const [attachments, setAttachments] = useState<AttachmentView[]>([]);
  const [confirmYolo, setConfirmYolo] = useState(false);
  const [modeMenuOpen, setModeMenuOpen] = useState(false);
  const [modelMenuOpen, setModelMenuOpen] = useState(false);
  const composerDraft = useStore((s) => s.composerDraft);
  const clearComposerDraft = useStore((s) => s.clearComposerDraft);
  const composerRef = useRef<MarkdownComposerHandle>(null);
  useEffect(() => {
    if (!composerDraft) return;
    composerRef.current?.setMarkdown(composerDraft);
    setInput(composerDraft);
    clearComposerDraft();
  }, [composerDraft, clearComposerDraft]);
  const scrollRef = useRef<HTMLDivElement>(null);
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
  const loadEarlier = () => {
    if (!truncated || loadingEarlier) return;
    const el = scrollRef.current;
    const prevScrollHeight = el?.scrollHeight ?? 0;
    const prevScrollTop = el?.scrollTop ?? 0;
    setLoadingEarlier(true);
    setVisibleCount((v) => v + RENDER_STEP);
    // The transcript is chronological, so loading earlier history
    // prepends rows above the current window. Anchor the viewport to
    // the content that was already visible; the newly inserted history
    // then sits above it and can be read by continuing to scroll up.
    stickRef.current = false;
    setStick(false);
    requestAnimationFrame(() => {
      const current = scrollRef.current;
      if (!current) return;
      const addedAbove = current.scrollHeight - prevScrollHeight;
      current.scrollTop = prevScrollTop + addedAbove;
    });
    window.setTimeout(() => setLoadingEarlier(false), 250);
  };
  const { t } = useTranslation();
  const thinkLevels = [
    { value: 'minimal', label: t('chat.thinkMinimal') },
    { value: 'low', label: t('chat.thinkLow') },
    { value: 'medium', label: t('chat.thinkMedium') },
    { value: 'high', label: t('chat.thinkHigh') },
    { value: 'xhigh', label: t('chat.thinkXHigh') },
  ];
  const thinkIndex = Math.max(
    0,
    thinkLevels.findIndex((l) => l.value === think),
  );
  const thinkLabel = thinkLevels[thinkIndex].label;
  const modelLabel = model
    ? (modelOptions.find((o) => o.id === model)?.label ?? model)
    : t('chat.modelAuto');

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
    const frame = requestAnimationFrame(() => {
      const el = scrollRef.current;
      if (el && stickRef.current) el.scrollTop = el.scrollHeight;
    });
    return () => cancelAnimationFrame(frame);
  }, [messages, pendingInteracts, stick]);

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
    const text = composerRef.current?.getMarkdown() ?? input;
    if ((!text.trim() && attachments.length === 0) || busy) return;
    stickRef.current = true;
    setStick(true);
    setInput('');
    composerRef.current?.clear();
    const staged = attachments;
    setAttachments([]);
    void send(text, staged);
  };

  const retry = () => {
    setInput('');
    composerRef.current?.clear();
    void retryLast();
  };

  const sessionTitle = sessions.find((s) => s.id === current)?.title;
  const headerTitle =
    sessionTitle && sessionTitle !== '(empty)'
      ? sessionTitle
      : t('chat.newSession');
  const yolo = mode === 'yolo';
  const readOnly = mode === 'read-only';
  const centerComposer = messages.length === 0 && configured;

  const retryFocusSwitch = () => {
    if (focus.name !== 'failed') return;
    if (focus.to.kind === 'existing') {
      void resume(focus.to.id);
    } else {
      void newChat();
    }
  };

  if (focus.name === 'no-session') {
    return (
      <main className="relative flex-1 min-w-0 grid place-items-center">
        <div className="text-center space-y-3 px-6">
          <p className="text-sm text-dim">{t('chat.noSession')}</p>
          <button
            onClick={() => void newChat()}
            className="rounded-lg border border-edge px-4 py-1.5 text-sm text-fg hover:border-accent/50 transition-colors"
          >
            {t('chat.startNew')}
          </button>
        </div>
      </main>
    );
  }

  if (focus.name === 'opening') {
    return (
      <main className="relative flex-1 min-w-0 grid place-items-center">
        <div className="flex items-center gap-2 text-sm text-dim">
          <Loader2 size="0.9286rem" className="animate-spin text-accent" />
          {t('chat.opening')}
        </div>
      </main>
    );
  }

  if (focus.name === 'failed') {
    return (
      <main className="relative flex-1 min-w-0 grid place-items-center">
        <div className="max-w-md w-full space-y-3 rounded-xl border border-err/40 bg-err/10 px-5 py-4 text-sm">
          <div className="flex items-center gap-2 font-medium text-fg">
            <AlertTriangle size="0.9286rem" className="text-err" />
            {t('chat.switchFailed')}
          </div>
          <p className="whitespace-pre-wrap break-all text-dim">{focus.error}</p>
          <div className="flex gap-2">
            {focus.from.kind === 'session' && (
              <button
                onClick={backFromFailure}
                className="flex items-center gap-1.5 rounded-lg border border-edge px-3 py-1.5 text-dim hover:text-fg"
              >
                <ArrowLeft size="0.8571rem" />
                {t('chat.backToSession')}
              </button>
            )}
            <button
              onClick={retryFocusSwitch}
              className="flex items-center gap-1.5 rounded-lg border border-edge px-3 py-1.5 text-dim hover:text-accent"
            >
              <RotateCcw size="0.8571rem" />
              {t('chat.retrySwitch')}
            </button>
          </div>
        </div>
      </main>
    );
  }

  const historyBlocked =
    focus.name === 'active' &&
    conversationState?.transcript.name !== 'ready' &&
    !busy;
  const historyFailed =
    conversationState?.transcript.name === 'failed' && !busy;
  const historyWarning =
    focus.name === 'active' &&
    conversationState?.transcript.name !== 'ready' &&
    busy;

  if (focus.name === 'active' && historyBlocked) {
    return (
      <main className="relative flex-1 min-w-0 grid place-items-center">
        <div className="text-center space-y-3 px-6">
          {historyFailed ? (
            <>
              <AlertTriangle
                size="1.25rem"
                className="mx-auto text-err"
              />
              <p className="text-sm text-dim">{t('chat.historyFailed')}</p>
              <p className="max-w-md text-xs text-dim">
                {conversationState?.transcript.name === 'failed' &&
                  'error' in conversationState.transcript &&
                  conversationState.transcript.error}
              </p>
              <button
                onClick={() => current && void retryTranscript(current)}
                className="flex items-center gap-1.5 rounded-lg border border-edge px-3 py-1.5 text-sm text-dim hover:text-accent"
              >
                <RotateCcw size="0.8571rem" />
                {t('chat.retryHistory')}
              </button>
            </>
          ) : (
            <div className="flex items-center gap-2 text-sm text-dim">
              <Loader2 size="0.9286rem" className="animate-spin text-accent" />
              {t('chat.loadingHistory')}
            </div>
          )}
        </div>
      </main>
    );
  }

  return (
    <main className="relative flex-1 min-w-0 flex flex-col min-h-0">
      <header
        className="h-11 shrink-0 border-b border-edge bg-panel flex items-center px-4 gap-2"
        style={{ ['--wails-draggable' as string]: 'drag' }}
      >
        <span className="text-sm font-medium truncate">{headerTitle}</span>
        {busy && (
          <span className="flex items-center gap-1 text-xs text-accent">
            <Loader2 size="0.8571rem" className="animate-spin" /> {stageLabel}
          </span>
        )}
        <span className="flex-1" />
        <div
          className="flex items-center"
          style={{ ['--wails-draggable' as string]: 'no-drag' }}
        >
          {subagentCards.length > 0 && (
            <button
              onClick={toggleSubagentPanel}
              className={`ml-1.5 flex items-center gap-1.5 rounded-lg border px-2 py-1 text-xs transition-colors ${
                subagentPanelOpen
                  ? 'border-subagent/40 bg-subagent/10 text-subagent'
                  : 'border-edge text-dim hover:text-fg'
              }`}
              title={t('subagent.toggle')}
              aria-label={t('subagent.toggle')}
            >
              <Bot size="0.9286rem" />
              {subagentCards.length}
            </button>
          )}
        </div>
      </header>

      <ProjectTrustBanner />

      <div className="relative flex min-h-0 flex-1 flex-col">
        <div
          ref={scrollRef}
          onScroll={() => {
            const el = scrollRef.current;
            if (!el) return;
            const pinned =
              el.scrollHeight - el.scrollTop - el.clientHeight < 80;
            stickRef.current = pinned;
            setStick(pinned);
            if (!pinned && el.scrollTop <= 8 && truncated && !loadingEarlier) {
              loadEarlier();
            }
          }}
          data-testid="chat-scroll"
          className="flex-1 overflow-y-auto [overflow-anchor:none] px-6 py-4"
        >
          {loadingEarlier && (
            <div className="pointer-events-none fixed left-1/2 top-14 z-20 -translate-x-1/2 rounded-full border border-edge bg-panel p-2 shadow-xl">
              <Loader2 size="1.0000rem" className="animate-spin text-dim" />
            </div>
          )}
          {historyWarning && (
            <div className="mb-3 flex items-center gap-2 rounded-lg border border-edge bg-panel2/70 px-3 py-2 text-xs text-dim">
              <AlertTriangle size="0.8571rem" className="text-accent" />
              {t('chat.liveWithoutHistory')}
            </div>
          )}
          {messages.length === 0 ? (
            <div className="h-full grid place-items-center">
              {!configured && (
                <div className="text-center space-y-3">
                  <div className="text-dim text-sm">
                    {t('chat.emptyUnconfigured')}
                  </div>
                  <button
                    onClick={() => openConfig()}
                    className="rounded-lg border border-edge px-3 py-1.5 text-sm text-fg hover:border-accent/50 transition-colors"
                  >
                    {t('chat.openSettings')}
                  </button>
                </div>
              )}
            </div>
          ) : (
            <div className="max-w-4xl mx-auto space-y-4">
              {visibleMessages.map((msg, localI) => {
                const i = start + localI;
                // Find the archived/live turn owning this message so
                // user bubbles and final assistant rows can show the
                // turn's request/start timestamps.
                const turn = turnForIndex(turnArtifacts, i);
                // The turn's artifact card and worked-for line render
                // after its last message: before the next user bubble
                // (or at the transcript end).
                const turnIdx = turn ? turnArtifacts.indexOf(turn) : -1;
                const isTurnEnd =
                  turnIdx >= 0 &&
                  i ===
                    (turnIdx + 1 < turnArtifacts.length
                      ? turnArtifacts[turnIdx + 1].start - 1
                      : messages.length - 1);
                const showArtifacts = isTurnEnd && (turn?.docs.length ?? 0) > 0;
                const showWorked =
                  isTurnEnd && msg.role === 'assistant' && !!turn;
                return (
                  <Fragment key={msg.id}>
                    <MessageRow
                      msg={msg}
                      busy={busy}
                      isTurnLast={
                        msg.role === 'assistant' &&
                        (i === messages.length - 1 ||
                          messages[i + 1]?.role === 'user')
                      }
                      requestedAt={turn?.requestedAt}
                      startedAt={turn?.startedAt}
                      streaming={
                        busy &&
                        msg.role === 'assistant' &&
                        i === messages.length - 1
                      }
                    />
                    {showArtifacts && turn && (
                      <>
                        <ArtifactStrip docs={turn.docs} />
                        <WorkedForLine
                          startedAt={turn.startedAt}
                          finishedAt={turn.finishedAt}
                          durationMs={turn.durationMs}
                        />
                      </>
                    )}
                    {showWorked && !showArtifacts && (
                      <WorkedForLine
                        startedAt={turn?.startedAt}
                        finishedAt={turn?.finishedAt}
                        durationMs={turn?.durationMs}
                      />
                    )}
                  </Fragment>
                );
              })}
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
                      <RotateCcw size="0.9286rem" /> {t('chat.retry')}
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
        {planState && !planDismissed && (
          <PlanPanel
            plan={planState.plan}
            live={planState.live}
            onClose={() => setPlanDismissed(true)}
          />
        )}
      </div>

      <div
        className={
          centerComposer
            ? 'absolute inset-x-0 top-[calc(50%+1.375rem)] z-10 mx-auto w-full max-w-4xl -translate-y-1/2 px-6'
            : 'shrink-0 px-6 pb-4'
        }
      >
        <div className="max-w-4xl mx-auto rounded-xl border border-edge bg-panel focus-within:border-accent/60 transition-colors">
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
                    <div className="flex h-16 w-16 flex-col items-center justify-center gap-1 rounded-lg border border-edge bg-panel2 p-1 text-[0.7143rem] text-dim">
                      <File size="1.1429rem" className="shrink-0" />
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
                    <X size="0.7143rem" />
                  </button>
                </div>
              ))}
            </div>
          )}
          {/* The top gap lives outside the scroll container (pt-3 here)
              so it stays visible even when the editor is scrolled to
              the bottom; padding inside the editor would scroll away
              with the content. */}
          <div className="pt-3">
            <MarkdownComposer
              ref={composerRef}
              initialMarkdown={input}
              placeholder={
                configured
                  ? t('chat.placeholder')
                  : t('chat.placeholderUnconfigured')
              }
              disabled={!configured}
              onValueChange={setInput}
              onSubmit={submit}
            />
          </div>
          <div className="mt-3 flex items-center justify-between px-3 pb-2.5">
            <div className="flex items-center gap-3">
              <button
                onClick={() => void pickAttachment()}
                disabled={!configured || busy}
                title={t('chat.attach')}
                aria-label={t('chat.attach')}
                className="flex items-center gap-1 rounded-lg border border-edge px-2.5 py-1 text-xs text-dim hover:text-fg disabled:opacity-50"
              >
                <Paperclip size="0.9286rem" />
              </button>
              <div className="relative">
                <button
                  onClick={() => setModeMenuOpen((v) => !v)}
                  className={`flex items-center gap-1.5 rounded-lg border px-2.5 py-1 text-xs transition-colors ${
                    yolo
                      ? 'border-yolo/50 bg-yolo/15 text-yolo hover:bg-yolo/25'
                      : readOnly
                        ? 'border-accent/40 bg-accent/10 text-accent hover:bg-accent/20'
                        : 'border-edge text-dim hover:text-fg'
                  }`}
                  title={t('chat.sandboxMode')}
                >
                  {yolo ? (
                    <Flame size="0.9286rem" />
                  ) : readOnly ? (
                    <Lock size="0.9286rem" />
                  ) : (
                    <ShieldCheck size="0.9286rem" />
                  )}
                  {yolo
                    ? t('chat.yoloMode')
                    : readOnly
                      ? t('chat.readOnlyMode')
                      : t('chat.workspaceMode')}
                  <ChevronUp size="0.7857rem" />
                </button>
                {modeMenuOpen && (
                  <>
                    <div
                      className="fixed inset-0 z-30"
                      onClick={() => setModeMenuOpen(false)}
                    />
                    <div className="absolute bottom-full left-0 z-40 mb-1.5 w-80 rounded-lg border border-edge bg-panel p-1 shadow-xl">
                      <button
                        onClick={() => {
                          setModeMenuOpen(false);
                          void setMode('read-only');
                        }}
                        className={`w-full rounded-md px-2 py-1.5 text-left text-xs ${
                          readOnly
                            ? 'bg-accent/10 text-accent'
                            : 'text-dim hover:bg-panel2 hover:text-fg'
                        }`}
                      >
                        <span className="flex items-center gap-2">
                          <Lock size="0.8571rem" /> {t('chat.readOnlyMode')}
                        </span>
                        <span className="mt-0.5 block pl-5 text-[0.7143rem] leading-snug text-dim">
                          {t('chat.readOnlyBanner')}
                        </span>
                      </button>
                      <button
                        onClick={() => {
                          setModeMenuOpen(false);
                          void setMode('workspace');
                        }}
                        className={`w-full rounded-md px-2 py-1.5 text-left text-xs ${
                          !readOnly && !yolo
                            ? 'bg-accent/10 text-accent'
                            : 'text-dim hover:bg-panel2 hover:text-fg'
                        }`}
                      >
                        <span className="flex items-center gap-2">
                          <ShieldCheck size="0.8571rem" />{' '}
                          {t('chat.workspaceMode')}
                        </span>
                        <span className="mt-0.5 block pl-5 text-[0.7143rem] leading-snug text-dim">
                          {t('chat.workspaceBanner')}
                        </span>
                      </button>
                      <button
                        onClick={() => {
                          setModeMenuOpen(false);
                          setConfirmYolo(true);
                        }}
                        className={`w-full rounded-md px-2 py-1.5 text-left text-xs ${
                          yolo
                            ? 'bg-yolo/15 text-yolo'
                            : 'text-yolo hover:bg-yolo/10 hover:text-yolo'
                        }`}
                      >
                        <span className="flex items-center gap-2">
                          <Flame size="0.8571rem" /> {t('chat.yoloMode')}
                        </span>
                        <span className="mt-0.5 block pl-5 text-[0.7143rem] leading-snug text-yolo/80">
                          {t('chat.yoloBanner')}
                        </span>
                      </button>
                    </div>
                  </>
                )}
              </div>
            </div>
            <div className="flex items-center gap-2">
              {(thinkSupported || modelOptions.length > 0) && (
                <div className="relative">
                  <button
                    onClick={() => setModelMenuOpen((v) => !v)}
                    title={t('chat.modelLabel')}
                    className="flex items-center gap-1.5 rounded-lg border border-edge px-2.5 py-1 text-xs text-dim hover:text-fg"
                  >
                    <Sparkles size="0.8571rem" className="text-accent" />
                    <span className="max-w-32 truncate">{modelLabel}</span>
                    {thinkSupported && (
                      <>
                        <span className="text-edge">·</span>
                        <span>{thinkLabel}</span>
                      </>
                    )}
                    <ChevronUp size="0.7857rem" />
                  </button>
                  {modelMenuOpen && (
                    <>
                      <div
                        className="fixed inset-0 z-30"
                        onClick={() => setModelMenuOpen(false)}
                      />
                      <div className="absolute bottom-full right-0 z-40 mb-1.5 w-64 rounded-xl border border-edge bg-panel p-1.5 shadow-xl">
                        <div className="px-2 pb-1 pt-1.5 text-[0.7143rem] uppercase tracking-wider text-dim">
                          {t('chat.modelLabel')}
                        </div>
                        <div className="max-h-52 overflow-y-auto">
                          <button
                            onClick={() => {
                              setModelMenuOpen(false);
                              void setModel('');
                            }}
                            className={`flex w-full items-center justify-between rounded-md px-2 py-1.5 text-left text-xs ${
                              !model
                                ? 'bg-accent/10 text-accent'
                                : 'text-dim hover:bg-panel2 hover:text-fg'
                            }`}
                          >
                            <span>{t('chat.modelAuto')}</span>
                            {!model && <Check size="0.8571rem" />}
                          </button>
                          {modelOptions.map((m) => (
                            <button
                              key={m.id}
                              onClick={() => {
                                setModelMenuOpen(false);
                                void setModel(m.id);
                              }}
                              className={`flex w-full items-center justify-between rounded-md px-2 py-1.5 text-left text-xs ${
                                model === m.id
                                  ? 'bg-accent/10 text-accent'
                                  : 'text-dim hover:bg-panel2 hover:text-fg'
                              }`}
                            >
                              <span className="truncate">{m.label}</span>
                              {model === m.id && <Check size="0.8571rem" />}
                            </button>
                          ))}
                        </div>
                        {thinkSupported && (
                          <>
                            <div className="my-1 border-t border-edge" />
                            <div className="flex items-center justify-between px-2 pt-1.5 text-xs">
                              <span className="text-dim">
                                {t('chat.thinkLabel')}
                              </span>
                              <span className="text-fg">{thinkLabel}</span>
                            </div>
                            <div className="px-2 pt-1.5">
                              <input
                                type="range"
                                min={0}
                                max={thinkLevels.length - 1}
                                step={1}
                                value={thinkIndex}
                                onChange={(e) => {
                                  const v = Number(e.target.value);
                                  void setThink(
                                    thinkLevels[v]?.value ?? 'medium',
                                  );
                                }}
                                className="w-full accent-accent"
                              />
                            </div>
                            <div className="flex justify-between px-2 pb-1.5 text-[0.7143rem] text-dim">
                              {thinkLevels.map((l) => (
                                <span key={l.value}>{l.label}</span>
                              ))}
                            </div>
                          </>
                        )}
                      </div>
                    </>
                  )}
                </div>
              )}
              {busy ? (
                <button
                  onClick={() => void cancelRun()}
                  aria-label={t('chat.stop')}
                  title={t('chat.stop')}
                  className="grid h-8 w-8 place-items-center rounded-lg border border-edge text-err hover:bg-panel2"
                >
                  <Square size="0.9286rem" />
                </button>
              ) : (
                <button
                  onClick={submit}
                  disabled={!input.trim() && attachments.length === 0}
                  aria-label={t('chat.send')}
                  title={t('chat.send')}
                  className="grid h-8 w-8 place-items-center rounded-lg bg-accent text-white hover:opacity-90 disabled:opacity-40"
                >
                  <ArrowUp size="1.1429rem" />
                </button>
              )}
            </div>
          </div>
        </div>
      </div>
      {confirmYolo && (
        <div className="fixed bottom-0 top-11 left-0 right-0 z-40 grid place-items-center bg-black/60 p-6">
          <div
            role="alertdialog"
            aria-modal="true"
            className="w-[34rem] max-w-[calc(100vw-3rem)] max-h-[calc(100vh-6rem)] overflow-y-auto rounded-2xl border border-yolo/50 bg-panel p-5 shadow-2xl"
          >
            <div className="flex items-start gap-3">
              <div className="grid h-9 w-9 shrink-0 place-items-center rounded-xl border border-yolo/40 bg-yolo/15 text-yolo">
                <AlertTriangle size="1.1429rem" />
              </div>
              <div className="min-w-0">
                <h2 className="text-sm font-semibold leading-snug text-fg">
                  {t('chat.yoloConfirmTitle')}
                </h2>
                <p className="mt-1 text-xs leading-relaxed text-dim">
                  {t('chat.yoloConfirmIntro')}
                </p>
              </div>
            </div>
            <div className="mt-4 space-y-2">
              {[
                {
                  icon: FolderOpen,
                  title: t('chat.yoloConfirmFiles'),
                  body: t('chat.yoloConfirmFilesBody'),
                },
                {
                  icon: Terminal,
                  title: t('chat.yoloConfirmTerminal'),
                  body: t('chat.yoloConfirmTerminalBody'),
                },
                {
                  icon: Globe,
                  title: t('chat.yoloConfirmNetwork'),
                  body: t('chat.yoloConfirmNetworkBody'),
                },
              ].map((risk) => {
                const Icon = risk.icon;
                return (
                  <div
                    key={risk.title}
                    className="flex items-start gap-2.5 rounded-lg border border-edge/70 bg-panel2/60 px-3 py-2.5"
                  >
                    <Icon
                      size="0.9286rem"
                      className="mt-0.5 shrink-0 text-yolo"
                    />
                    <div className="min-w-0">
                      <div className="text-xs font-medium text-fg">
                        {risk.title}
                      </div>
                      <p className="mt-0.5 text-[0.7857rem] leading-snug text-dim">
                        {risk.body}
                      </p>
                    </div>
                  </div>
                );
              })}
            </div>
            <p className="mt-4 rounded-lg border border-yolo/25 bg-yolo/5 px-3 py-2 text-[0.7857rem] leading-snug text-dim">
              {t('chat.yoloConfirmScope')}
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
                className="rounded-lg bg-yolo px-4 py-1.5 text-sm font-medium text-white hover:opacity-90"
              >
                {t('chat.confirmSwitch')}
              </button>
            </div>
          </div>
        </div>
      )}
    </main>
  );
}
