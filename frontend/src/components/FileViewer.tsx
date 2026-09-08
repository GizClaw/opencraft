import { Component, lazy, Suspense, useEffect, useState } from 'react';
import type { ErrorInfo, ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import {
  ChevronDown,
  ChevronRight,
  Copy,
  ExternalLink,
  File as FileGlyph,
  FileCode,
  FileText,
  Folder,
  FolderOpen,
  Loader2,
  Plus,
  Search,
  X,
} from 'lucide-react';
import i18n from '../i18n';
import { api } from '../lib/api';
import { useStore } from '../lib/store';
import type { FileNode, FilePreview, FileTab } from '../lib/types';
import { Markdown } from './Markdown';

const CodePane = lazy(() =>
  import('./viewer/CodePane').then((m) => ({ default: m.CodePane })),
);
const PdfPane = lazy(() =>
  import('./viewer/PdfPane').then((m) => ({ default: m.PdfPane })),
);

// LazyBoundary contains one lazy preview chunk. A chunk that fails to
// load (e.g. a stale Vite optimizer serving an old module list) shows
// a retry card inside the viewer instead of crashing the whole app.
class LazyBoundary extends Component<
  { children: ReactNode },
  { error: Error | null }
> {
  state: { error: Error | null } = { error: null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('opencraft file viewer chunk failed:', error, info);
  }

  render() {
    if (this.state.error) {
      return (
        <div className="flex h-full flex-col items-center justify-center gap-3 p-6 text-center text-xs text-dim">
          <p className="text-err">{i18n.t('files.viewerChunkFailed')}</p>
          <code className="max-w-full truncate rounded border border-edge bg-panel2 px-2 py-1">
            {String(this.state.error.message)}
          </code>
          <button
            onClick={() => this.setState({ error: null })}
            className="rounded-md border border-edge bg-panel2 px-3 py-1.5 text-fg hover:border-accent/50"
          >
            {i18n.t('app.tryAgain')}
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}

function isMarkdown(name: string): boolean {
  const ext = name.split('.').pop()?.toLowerCase();
  return ext === 'md' || ext === 'markdown';
}

function dirOf(rel: string): string {
  const i = Math.max(rel.lastIndexOf('/'), rel.lastIndexOf('\\'));
  return i < 0 ? '.' : rel.slice(0, i);
}

function pathParts(rel: string): string[] {
  if (!rel || rel === '.') return [];
  return rel.split('/');
}

// FileViewer is the session-scoped right-hand file panel. It lives
// inside the chat page: visibility, open tabs and tree position are
// all stored per conversation and restored when that chat is focused.
export function FileViewer({
  sessionID,
  embedded = false,
}: {
  sessionID: string;
  // embedded makes the panel fill a parent right-rail shell (Files/Git
  // segmented mode); standalone keeps its own width and left border.
  embedded?: boolean;
}) {
  const { t } = useTranslation();
  const viewer = useStore((s) => s.viewers[sessionID]);
  const tabs = viewer?.fileTabs ?? [];
  const activeKey = viewer?.fileActive ?? null;
  const activate = useStore((s) => s.activateFileTab);
  const closeTab = useStore((s) => s.closeFileTab);
  const showDir = useStore((s) => s.showFileDir);
  const newEmptyTab = useStore((s) => s.newEmptyTab);
  const [treeOpen, setTreeOpen] = useState(false);
  const active = tabs.find((tab) => tab.key === activeKey) ?? null;

  // The tree visibility is a per-panel transient: switching sessions
  // closes it so a chat never inherits the previous one's overlay.
  useEffect(() => {
    setTreeOpen(false);
  }, [sessionID]);

  return (
    <div
      className={
        embedded
          ? 'relative flex h-full min-h-0 min-w-0 flex-1 flex-col bg-panel'
          : 'relative flex h-full shrink-0 flex-col border-l border-edge bg-panel'
      }
      style={
        embedded ? undefined : { width: 'min(49.6vw, 896px)', minWidth: 448 }
      }
    >
      <div className="flex h-9 shrink-0 items-end gap-0 overflow-x-auto border-b border-edge bg-panel2/30 px-2 pt-1 text-xs">
        {tabs.map((tab) => (
          <TabChip
            key={tab.key}
            tab={tab}
            active={tab.key === activeKey}
            onActivate={() => activate(tab.key)}
            onClose={() => closeTab(tab.key)}
          />
        ))}
        <button
          onClick={() => {
            newEmptyTab();
            setTreeOpen(true);
          }}
          className="mb-1 ml-1 grid h-6 w-7 shrink-0 place-items-center rounded-md border border-transparent text-dim transition-colors hover:border-edge hover:bg-panel2 hover:text-fg"
          title={t('files.newTab')}
          aria-label={t('files.newTab')}
        >
          <Plus size="0.8571rem" />
        </button>
      </div>

      <div className="relative flex min-h-0 flex-1 flex-col">
        {active && active.path ? (
          <ToolbarRow
            tab={active}
            treeOpen={treeOpen}
            onToggleTree={() => setTreeOpen((v) => !v)}
            onDir={(rel) => {
              // Directory navigation is only visible inside the tree:
              // open it when a breadcrumb folder is clicked while the
              // overlay is closed, so the click has an effect.
              if (!treeOpen) setTreeOpen(true);
              showDir(rel);
            }}
          />
        ) : null}
        <div className="relative min-h-0 flex-1">
          {active && active.path ? (
            <div className="absolute inset-0 overflow-auto">
              <TabContent tab={active} key={active.key} />
            </div>
          ) : (
            <div className="absolute inset-0 flex flex-col items-center justify-center gap-2 p-6 text-center text-xs text-dim">
              <FileGlyph size="1.7143rem" className="opacity-60" />
              <p>{active ? t('files.selectFromTree') : t('files.emptyHint')}</p>
              <button
                onClick={() => setTreeOpen(true)}
                className="rounded-lg border border-edge bg-panel2 px-3 py-1.5 text-fg hover:border-accent/50"
              >
                {t('files.browseWorkspace')}
              </button>
            </div>
          )}
          {treeOpen && (
            <FileTreePanel
              sessionID={sessionID}
              onClose={() => setTreeOpen(false)}
            />
          )}
        </div>
      </div>
    </div>
  );
}

function TabChip({
  tab,
  active,
  onActivate,
  onClose,
}: {
  tab: FileTab;
  active: boolean;
  onActivate: () => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const placeholder = !tab.path;
  return (
    <div
      className={`group relative flex min-w-0 shrink-0 cursor-pointer items-center gap-1.5 rounded-t-lg border border-b-0 px-3 py-1.5 transition-colors ${
        active
          ? '-mb-px border-edge bg-panel text-fg'
          : 'border-transparent text-dim hover:bg-panel hover:text-fg'
      }`}
      onClick={onActivate}
      title={placeholder ? t('files.newFile') : tab.rel || tab.path}
    >
      {placeholder ? (
        <FileGlyph size="0.8571rem" className="shrink-0 text-dim" />
      ) : isMarkdown(tab.name) ? (
        <FileText size="0.8571rem" className="shrink-0 text-accent/80" />
      ) : (
        <FileCode size="0.8571rem" className="shrink-0 text-dim" />
      )}
      {active && (
        <span className="absolute inset-x-3 top-0 h-[2px] rounded-b bg-accent" />
      )}
      <span className="max-w-40 truncate">
        {placeholder ? t('files.newFile') : tab.name}
      </span>
      <button
        onClick={(e) => {
          e.stopPropagation();
          onClose();
        }}
        className="ml-1 grid h-4 w-4 shrink-0 place-items-center rounded text-dim opacity-0 transition-opacity hover:bg-edge hover:text-fg group-hover:opacity-100"
        aria-label="Close tab"
      >
        <X size="0.7143rem" />
      </button>
    </div>
  );
}

// ToolbarRow merges the breadcrumb, the file-tree toggle and the
// actions pill into one navigation row under the tabs.
function ToolbarRow({
  tab,
  treeOpen,
  onToggleTree,
  onDir,
}: {
  tab: FileTab;
  treeOpen: boolean;
  onToggleTree: () => void;
  onDir: (rel: string) => void;
}) {
  const { t } = useTranslation();
  const workspacePath = useStore((s) => s.workspace);
  const workspaceMeta = useStore((s) =>
    s.workspaces.find((w) => w.path === workspacePath),
  );
  const workspaceLabel =
    workspaceMeta?.title ||
    (workspacePath.split(/[\\/]/).filter(Boolean).pop() ?? workspacePath);
  const parts = pathParts(tab.rel);
  const outsideWorkspace = tab.root !== 'workspace';
  return (
    <div className="relative flex h-9 shrink-0 items-center gap-1 border-b border-edge px-2">
      {outsideWorkspace ? (
        <span className="min-w-0 flex-1 truncate px-1 text-xs text-dim">
          <span className="text-accent/80">{tab.root}</span>
          <span className="ml-1 truncate">{tab.path}</span>
        </span>
      ) : (
        <div className="flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto text-xs text-dim">
          <button
            onClick={() => onDir('.')}
            className="shrink-0 hover:text-fg"
            title={workspacePath}
          >
            {workspaceLabel}
          </button>
          {parts.slice(0, -1).map((part, i) => {
            const rel = parts.slice(0, i + 1).join('/');
            return (
              <span key={rel} className="flex shrink-0 items-center gap-0.5">
                <ChevronRight size="0.7143rem" />
                <button onClick={() => onDir(rel)} className="hover:text-fg">
                  {part}
                </button>
              </span>
            );
          })}
          {parts.length > 0 && (
            <span className="flex shrink-0 items-center gap-0.5 text-fg">
              <ChevronRight size="0.7143rem" />
              <span className="max-w-64 truncate">
                {parts[parts.length - 1]}
              </span>
            </span>
          )}
        </div>
      )}
      <button
        onClick={onToggleTree}
        title={treeOpen ? t('files.hideTree') : t('files.toggleTree')}
        aria-pressed={treeOpen}
        className={`grid h-7 w-7 shrink-0 place-items-center rounded-lg ${
          treeOpen
            ? 'bg-accent/15 text-accent'
            : 'text-dim hover:bg-panel2 hover:text-fg'
        }`}
      >
        {treeOpen ? (
          <FolderOpen size="0.9286rem" />
        ) : (
          <Folder size="0.9286rem" />
        )}
      </button>
      <ViewerActions tab={tab} />
    </div>
  );
}

// ViewerActions is the actions capsule in the breadcrumb row: a pill
// like the sandbox-mode selector that expands into the file actions.
function ViewerActions({ tab }: { tab: FileTab }) {
  const { t } = useTranslation();
  const flash = useStore((s) => s.flash);
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState<'path' | 'content' | null>(null);

  const run = (fn: () => Promise<unknown>) => {
    setOpen(false);
    void fn().catch((err) => flash(String(err)));
  };

  const copy = async (what: 'path' | 'content') => {
    try {
      // Copying the path always yields the absolute path; the
      // workspace-relative form is only for on-screen breadcrumbs.
      let text = tab.path;
      if (what === 'content') {
        const p = await api.readPreview(tab.path);
        text = p.text ?? '';
      }
      await navigator.clipboard.writeText(text);
      setCopied(what);
      window.setTimeout(() => setCopied(null), 1200);
    } catch (err) {
      flash(String(err));
    } finally {
      setOpen(false);
    }
  };

  return (
    <div className="relative shrink-0">
      <button
        onClick={() => setOpen((v) => !v)}
        aria-haspopup="menu"
        aria-expanded={open}
        className="flex items-center gap-1.5 rounded-lg border border-edge bg-panel2 px-2.5 py-1 text-xs text-fg transition-colors hover:border-accent/50"
      >
        <ExternalLink size="0.7857rem" className="text-accent" />
        <span>{t('files.openSystem')}</span>
        <ChevronDown
          size="0.7857rem"
          className={`transition-transform ${open ? 'rotate-180' : ''}`}
        />
      </button>
      {open && (
        <>
          <div
            className="fixed inset-0 z-30"
            onMouseDown={() => setOpen(false)}
          />
          <div
            role="menu"
            className="absolute right-0 top-full z-40 mt-1 min-w-52 rounded-lg border border-edge bg-panel p-1 shadow-xl"
          >
            <button
              role="menuitem"
              className="flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-xs text-fg hover:bg-panel2"
              onClick={() => run(() => api.openPath(tab.path))}
            >
              <ExternalLink size="0.8571rem" className="text-dim" />
              {t('files.openSystem')}
            </button>
            <button
              role="menuitem"
              className="flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-xs text-fg hover:bg-panel2"
              onClick={() => run(() => api.openArtifactWith(tab.path))}
            >
              <ExternalLink size="0.8571rem" className="text-dim" />
              {t('files.openWith')}
            </button>
            <button
              role="menuitem"
              className="flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-xs text-fg hover:bg-panel2"
              onClick={() => run(() => api.revealArtifact(tab.path))}
            >
              <FolderOpen size="0.8571rem" className="text-dim" />
              {t('files.reveal')}
            </button>
            <div role="separator" className="my-1 border-t border-edge" />
            <button
              role="menuitem"
              className="flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-xs text-fg hover:bg-panel2"
              onClick={() => void copy('path')}
            >
              <Copy size="0.8571rem" className="text-dim" />
              {copied === 'path' ? t('files.copied') : t('files.copyPath')}
            </button>
            <button
              role="menuitem"
              className="flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-xs text-fg hover:bg-panel2"
              onClick={() => void copy('content')}
            >
              <Copy size="0.8571rem" className="text-dim" />
              {copied === 'content'
                ? t('files.copied')
                : t('files.copyContent')}
            </button>
          </div>
        </>
      )}
    </div>
  );
}

// FileTreePanel floats over the right side of the file content. It
// combines workspace quick-open (File.Search) and the lazy directory
// tree in one overlay so the top-level plus/search row is unnecessary.
function FileTreePanel({
  sessionID,
  onClose,
}: {
  sessionID: string;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const openFileTarget = useStore((s) => s.openFileTarget);
  const showDir = useStore((s) => s.showFileDir);
  const treeDir = useStore((s) => s.viewers[sessionID]?.fileTreeDir ?? '.');
  const [query, setQuery] = useState('');
  const [hits, setHits] = useState<{ path: string; is_dir: boolean }[]>([]);
  const [entries, setEntries] = useState<Record<string, FileNode[]>>({});
  const [expanded, setExpanded] = useState<Record<string, boolean>>({
    '.': true,
  });

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation();
        onClose();
      }
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, [onClose]);

  useEffect(() => {
    if (!expanded['.']) return;
    void api
      .listDir('.')
      .then((nodes) => setEntries((e) => ({ ...e, '.': nodes })))
      .catch(() => undefined);
    // The tree follows directory navigation from breadcrumbs/links.
    const parts = treeDir.split('/').filter(Boolean);
    const expandedNext: Record<string, boolean> = { ...expanded, '.': true };
    let acc = '';
    for (const part of parts) {
      acc = acc ? `${acc}/${part}` : part;
      expandedNext[acc] = true;
    }
    setExpanded(expandedNext);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [treeDir]);

  useEffect(() => {
    const q = query.trim();
    if (!q) {
      setHits([]);
      return;
    }
    const timer = window.setTimeout(() => {
      void api
        .searchFiles(q, 50)
        .then((h) => setHits(h))
        .catch(() => setHits([]));
    }, 150);
    return () => window.clearTimeout(timer);
  }, [query]);

  const toggle = (rel: string) => {
    const next = { ...expanded, [rel]: !expanded[rel] };
    setExpanded(next);
    if (next[rel] && !entries[rel]) {
      void api
        .listDir(rel)
        .then((nodes) => setEntries((e) => ({ ...e, [rel]: nodes })))
        .catch(() => undefined);
    }
  };

  const renderDir = (rel: string, depth: number): React.ReactNode => {
    const nodes = entries[rel] ?? [];
    return (
      <div key={rel}>
        {nodes
          .filter((n) => n.is_dir)
          .map((n) => {
            const childRel = n.path;
            const childOpen = !!expanded[childRel];
            return (
              <div key={childRel}>
                <button
                  onClick={() => toggle(childRel)}
                  style={{ paddingLeft: 8 + depth * 12 }}
                  className={`flex w-full items-center gap-1 truncate rounded px-1 py-0.5 text-left text-xs hover:bg-panel2 ${
                    treeDir === childRel ? 'text-fg' : 'text-dim hover:text-fg'
                  }`}
                >
                  {childOpen ? (
                    <ChevronDown size="0.7857rem" className="shrink-0" />
                  ) : (
                    <ChevronRight size="0.7857rem" className="shrink-0" />
                  )}
                  {childOpen ? (
                    <FolderOpen
                      size="0.9286rem"
                      className="shrink-0 text-accent/70"
                    />
                  ) : (
                    <Folder
                      size="0.9286rem"
                      className="shrink-0 text-accent/70"
                    />
                  )}
                  <span className="truncate">{n.name}</span>
                </button>
                {childOpen && renderDir(childRel, depth + 1)}
              </div>
            );
          })}
        {nodes
          .filter((n) => !n.is_dir)
          .map((n) => (
            <button
              key={n.path}
              onClick={() => void openFileTarget(n.path)}
              style={{ paddingLeft: 8 + (depth + 1) * 12 }}
              className="flex w-full items-center gap-1 rounded px-1 py-0.5 text-left text-xs text-dim hover:bg-panel2 hover:text-fg"
              title={n.path}
            >
              <FileCode size="0.8571rem" className="shrink-0" />
              <span className="truncate">{n.name}</span>
            </button>
          ))}
      </div>
    );
  };

  return (
    <div className="absolute inset-y-0 right-0 z-20 flex w-80 max-w-[85%] flex-col border-l border-edge bg-panel shadow-xl">
      <div className="flex h-9 shrink-0 items-center gap-1.5 border-b border-edge px-2">
        <Search size="0.8571rem" className="shrink-0 text-dim" />
        <input
          autoFocus
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t('files.quickOpenHint')}
          className="min-w-0 flex-1 bg-transparent text-xs outline-none"
        />
        <button
          onClick={onClose}
          className="grid h-6 w-6 shrink-0 place-items-center rounded text-dim hover:bg-panel2 hover:text-fg"
          aria-label={t('files.hideTree')}
        >
          <X size="0.8571rem" />
        </button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-1.5">
        {query.trim() ? (
          hits.length === 0 ? (
            <div className="px-2 py-3 text-center text-[0.7143rem] text-dim">
              {t('files.noMatches')}
            </div>
          ) : (
            hits.map((hit) => (
              <button
                key={hit.path}
                onClick={() => {
                  if (hit.is_dir) showDir(hit.path);
                  else void openFileTarget(hit.path);
                }}
                className="flex w-full items-center gap-1.5 truncate rounded px-2 py-1 text-left text-xs text-dim hover:bg-panel2 hover:text-fg"
              >
                {hit.is_dir ? (
                  <Folder
                    size="0.8571rem"
                    className="shrink-0 text-accent/70"
                  />
                ) : (
                  <FileCode size="0.8571rem" className="shrink-0" />
                )}
                <span className="truncate">{hit.path}</span>
              </button>
            ))
          )
        ) : (
          <>
            <div className="flex items-center gap-1 px-1 pb-1 text-[0.6429rem] font-semibold uppercase tracking-wider text-dim">
              {t('files.workspace')}
            </div>
            <button
              onClick={() => toggle('.')}
              className="flex w-full items-center gap-1 rounded px-1 py-0.5 text-xs text-dim hover:bg-panel2 hover:text-fg"
            >
              {expanded['.'] ? (
                <ChevronDown size="0.7857rem" />
              ) : (
                <ChevronRight size="0.7857rem" />
              )}
              {expanded['.'] ? (
                <FolderOpen size="0.9286rem" className="text-accent/70" />
              ) : (
                <Folder size="0.9286rem" className="text-accent/70" />
              )}
              <span className="truncate">{t('files.workspace')}</span>
            </button>
            {expanded['.'] && renderDir('.', 1)}
          </>
        )}
      </div>
    </div>
  );
}

function TabContent({ tab }: { tab: FileTab }) {
  const { t } = useTranslation();
  const openFileTarget = useStore((s) => s.openFileTarget);
  const [preview, setPreview] = useState<FilePreview | null>(null);
  const [error, setError] = useState('');
  const [showMd, setShowMd] = useState(isMarkdown(tab.name));

  useEffect(() => {
    let live = true;
    setPreview(null);
    setError('');
    void api
      .readPreview(tab.path)
      .then((p) => {
        if (live) setPreview(p);
      })
      .catch((err) => {
        if (live) setError(String(err));
      });
    return () => {
      live = false;
    };
  }, [tab.path]);

  if (error) {
    return <MetaPane tab={tab} message={error} showMetaActions />;
  }
  if (!preview) {
    return (
      <div className="flex h-full items-center justify-center text-dim">
        <Loader2 size="1.1429rem" className="animate-spin" />
      </div>
    );
  }

  if (preview.kind === 'text' && isMarkdown(tab.name) && showMd) {
    // Relative references resolve against the file's own directory.
    // Workspace tabs have a relative path; data/skill tabs fall back to
    // the absolute path so dirOf stays correct for both.
    const base = tab.root === 'workspace' ? dirOf(tab.rel) : dirOf(tab.path);
    return (
      <div className="p-4">
        <button
          onClick={() => setShowMd(false)}
          className="mb-2 rounded border border-edge bg-panel2 px-2 py-1 text-xs text-dim hover:text-fg"
        >
          {t('files.viewSource')}
        </button>
        <div className="prose-chat text-sm">
          <Markdown
            text={preview.text ?? ''}
            basePath={base}
            onOpen={(href, b) => void openFileTarget(href, b ?? '')}
          />
        </div>
      </div>
    );
  }

  if (preview.kind === 'text') {
    return (
      <LazyBoundary>
        <Suspense
          fallback={
            <div className="flex h-full items-center justify-center text-dim">
              <Loader2 size="1.1429rem" className="animate-spin" />
            </div>
          }
        >
          <CodePane
            text={preview.text ?? ''}
            name={tab.name}
            sourceView={isMarkdown(tab.name)}
            onSource={() => setShowMd(true)}
          />
        </Suspense>
      </LazyBoundary>
    );
  }

  if (preview.kind === 'image' && preview.data_url) {
    return (
      <div className="flex h-full items-start justify-center overflow-auto p-4">
        <img
          src={preview.data_url}
          alt={tab.name}
          className="max-w-full rounded-lg border border-edge object-contain"
        />
      </div>
    );
  }

  if (preview.kind === 'pdf' && preview.data_url) {
    return (
      <LazyBoundary>
        <Suspense
          fallback={
            <div className="flex h-full items-center justify-center text-dim">
              <Loader2 size="1.1429rem" className="animate-spin" />
            </div>
          }
        >
          <PdfPane dataUrl={preview.data_url} name={tab.name} />
        </Suspense>
      </LazyBoundary>
    );
  }

  return (
    <MetaPane
      tab={tab}
      message={preview.too_large ? t('files.tooLarge') : undefined}
      showMetaActions
    />
  );
}

function MetaPane({
  tab,
  message,
  showMetaActions = true,
}: {
  tab: FileTab;
  message?: string;
  showMetaActions?: boolean;
}) {
  const { t } = useTranslation();
  const flash = useStore((s) => s.flash);
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 p-6 text-center text-xs text-dim">
      <FileGlyph size="2.2857rem" className="opacity-50" />
      <div className="break-all text-fg">{tab.name}</div>
      <div>
        {message ?? `${t('files.noInlinePreview')} ${tab.media_type || ''}`}
      </div>
      {showMetaActions && (
        <div className="flex gap-2">
          <button
            onClick={() =>
              void api.openPath(tab.path).catch((err) => flash(String(err)))
            }
            className="rounded-md border border-edge bg-panel2 px-3 py-1.5 hover:border-accent/50"
          >
            {t('files.openSystem')}
          </button>
          <button
            onClick={() =>
              void api
                .revealArtifact(tab.path)
                .catch((err) => flash(String(err)))
            }
            className="rounded-md border border-edge bg-panel2 px-3 py-1.5 hover:border-accent/50"
          >
            {t('files.reveal')}
          </button>
        </div>
      )}
    </div>
  );
}
