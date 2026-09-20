import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  ChevronDown,
  ChevronRight,
  Copy,
  Eye,
  EyeOff,
  ExternalLink,
  File as FileGlyph,
  FileCode,
  FileText,
  Folder,
  FolderOpen,
  Plus,
  Search,
  X,
} from 'lucide-react';
import { api } from '../lib/api';
import { useStore } from '../lib/store';
import type { FileNode, FileTab } from '../lib/types';
import { Button } from './ui/Button';
import { useOverlayLayer } from '../lib/overlay';
import { Popover } from './ui/Popover';
import { ICON } from './ui/icon';
import { FilePreviewPane, isMarkdownFile } from './viewer/FilePreviewPane';

function pathParts(rel: string): string[] {
  if (!rel || rel === '.') return [];
  return rel.split('/');
}

// The hidden scrollbar is replaced by an edge fade: a mask, so the
// gradient is expressed in the strip's own pixels instead of a
// hardcoded background color that would break in the light theme.
function stripMask(left: boolean, right: boolean): string | undefined {
  const w = '1.25rem';
  if (left && right) {
    return `linear-gradient(to right, transparent 0, black ${w}, black calc(100% - ${w}), transparent 100%)`;
  }
  if (left) {
    return `linear-gradient(to right, transparent 0, black ${w}, black 100%)`;
  }
  if (right) {
    return `linear-gradient(to right, black 0, black calc(100% - ${w}), transparent 100%)`;
  }
  return undefined;
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
  const openFileTarget = useStore((s) => s.openFileTarget);
  const [treeOpen, setTreeOpen] = useState(false);
  const stripRef = useRef<HTMLDivElement | null>(null);
  const [stripFade, setStripFade] = useState({ left: false, right: false });
  const active = tabs.find((tab) => tab.key === activeKey) ?? null;
  const mask = stripMask(stripFade.left, stripFade.right);

  // The tree visibility is a per-panel transient: switching sessions
  // closes it so a chat never inherits the previous one's overlay.
  useEffect(() => {
    setTreeOpen(false);
  }, [sessionID]);

  const syncStripFade = useCallback(() => {
    const strip = stripRef.current;
    if (!strip) return;
    const max = Math.max(0, strip.scrollWidth - strip.clientWidth);
    const left = strip.scrollLeft > 1;
    const right = strip.scrollLeft < max - 1;
    setStripFade((prev) =>
      prev.left === left && prev.right === right ? prev : { left, right },
    );
  }, []);

  // The strip hides its scrollbar, because a bar inside the h-9 row
  // would eat into the tab chips and break the active tab's seam with
  // the border below. Keeping the active tab in view replaces it:
  // opening a file from the tree or a chat link, and restoring a
  // session, must always land on a tab that is actually visible.
  useEffect(() => {
    stripRef.current
      ?.querySelector<HTMLElement>('[data-tab-active="true"]')
      ?.scrollIntoView({ inline: 'nearest', block: 'nearest' });
    syncStripFade();
  }, [activeKey, sessionID, syncStripFade]);

  // The edge fades track the real scroll box, so they light up only on
  // the side that actually hides tabs.
  useEffect(() => {
    const strip = stripRef.current;
    if (!strip) return;
    syncStripFade();
    strip.addEventListener('scroll', syncStripFade, { passive: true });
    const observer = new ResizeObserver(syncStripFade);
    observer.observe(strip);
    return () => {
      strip.removeEventListener('scroll', syncStripFade);
      observer.disconnect();
    };
  }, [syncStripFade, tabs.length]);

  // A hidden scrollbar costs the mouse its only handle: desktop engines
  // don't turn a plain vertical wheel into horizontal scrolling, so map
  // the wheel here, the way editor tab strips do. Trackpad horizontal
  // gestures keep their native path, and the listener is non-passive so
  // the wheel doesn't also scroll whatever sits behind the rail.
  useEffect(() => {
    const strip = stripRef.current;
    if (!strip) return;
    const onWheel = (event: WheelEvent) => {
      if (Math.abs(event.deltaY) <= Math.abs(event.deltaX)) return;
      if (strip.scrollWidth <= strip.clientWidth) return;
      event.preventDefault();
      // deltaMode 1 counts lines (classic wheels in some engines).
      strip.scrollLeft += event.deltaY * (event.deltaMode === 1 ? 16 : 1);
    };
    strip.addEventListener('wheel', onWheel, { passive: false });
    return () => strip.removeEventListener('wheel', onWheel);
  }, []);

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
      <div className="flex h-9 shrink-0 items-end border-b border-edge bg-panel2 px-2 pt-1 text-xs">
        <div
          ref={stripRef}
          className="no-scrollbar overscroll-none flex min-w-0 flex-1 items-end gap-0 overflow-x-auto"
          style={{ maskImage: mask, WebkitMaskImage: mask }}
        >
          {tabs.map((tab) => (
            <TabChip
              key={tab.key}
              tab={tab}
              active={tab.key === activeKey}
              onActivate={() => activate(tab.key)}
              onClose={() => closeTab(tab.key)}
            />
          ))}
        </div>
        {/* The new-tab button stays outside the scroller so a long tab
            list can never push it out of reach. */}
        <button
          onClick={() => {
            newEmptyTab();
            setTreeOpen(true);
          }}
          className="mb-1 ml-1 grid h-6 w-7 shrink-0 place-items-center rounded-control border border-transparent text-dim transition-colors hover:border-edge hover:bg-panel2 hover:text-fg"
          data-tip={t('files.newTab')}
          aria-label={t('files.newTab')}
        >
          <Plus size={ICON.xs} />
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
              <FilePreviewPane
                tab={active}
                key={active.key}
                onOpen={(href, base) => void openFileTarget(href, base)}
              />
            </div>
          ) : (
            // The tree panel is an overlay: stop the empty state short
            // of it so its copy is never clipped mid-word.
            <div
              className={`absolute inset-y-0 left-0 flex flex-col items-center justify-center gap-2 p-6 text-center text-xs text-dim ${
                treeOpen ? 'right-80' : 'right-0'
              }`}
            >
              <FileGlyph size={ICON.xl} className="opacity-60" />
              <p>{active ? t('files.selectFromTree') : t('files.emptyHint')}</p>
              <Button variant="secondary" onClick={() => setTreeOpen(true)}>
                {t('files.browseWorkspace')}
              </Button>
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
      className={`group relative flex min-w-0 shrink-0 cursor-pointer items-center gap-1.5 rounded-t-control border border-b-0 px-3 py-1.5 transition-colors ${
        active
          ? '-mb-px border-edge bg-panel text-fg'
          : 'border-transparent text-dim hover:bg-panel hover:text-fg'
      }`}
      data-tab-active={active ? 'true' : undefined}
      onClick={onActivate}
      data-tip={placeholder ? t('files.newFile') : tab.rel || tab.path}
    >
      {placeholder ? (
        <FileGlyph size={ICON.xs} className="shrink-0 text-dim" />
      ) : isMarkdownFile(tab.name) ? (
        <FileText size={ICON.xs} className="shrink-0 text-accent" />
      ) : (
        <FileCode size={ICON.xs} className="shrink-0 text-dim" />
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
        className="ml-1 grid h-4 w-4 shrink-0 place-items-center rounded-tight text-dim opacity-0 transition-opacity hover:bg-edge hover:text-fg group-hover:opacity-100"
        aria-label="Close tab"
      >
        <X size={ICON.xs} />
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
          <span className="text-accent">{tab.root}</span>
          <span className="ml-1 truncate">{tab.path}</span>
        </span>
      ) : (
        <div className="flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto text-xs text-dim">
          <button
            onClick={() => onDir('.')}
            className="shrink-0 hover:text-fg"
            data-tip={workspacePath}
          >
            {workspaceLabel}
          </button>
          {parts.slice(0, -1).map((part, i) => {
            const rel = parts.slice(0, i + 1).join('/');
            return (
              <span key={rel} className="flex shrink-0 items-center gap-0.5">
                <ChevronRight size={ICON.xs} />
                <button onClick={() => onDir(rel)} className="hover:text-fg">
                  {part}
                </button>
              </span>
            );
          })}
          {parts.length > 0 && (
            <span className="flex shrink-0 items-center gap-0.5 text-fg">
              <ChevronRight size={ICON.xs} />
              <span className="max-w-64 truncate">
                {parts[parts.length - 1]}
              </span>
            </span>
          )}
        </div>
      )}
      <button
        onClick={onToggleTree}
        data-tip={treeOpen ? t('files.hideTree') : t('files.toggleTree')}
        aria-label={treeOpen ? t('files.hideTree') : t('files.toggleTree')}
        aria-pressed={treeOpen}
        className={`grid h-7 w-7 shrink-0 place-items-center rounded-control ${
          treeOpen
            ? 'bg-accent/15 text-accent'
            : 'text-dim hover:bg-panel2 hover:text-fg'
        }`}
      >
        {treeOpen ? <FolderOpen size={ICON.sm} /> : <Folder size={ICON.sm} />}
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
  const triggerRef = useRef<HTMLButtonElement | null>(null);
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
        ref={triggerRef}
        onClick={() => setOpen((v) => !v)}
        aria-haspopup="menu"
        aria-expanded={open}
        className="flex items-center gap-1.5 rounded-control border border-edge bg-panel2 px-2.5 py-1 text-xs text-fg transition-colors hover:border-accent/50"
      >
        <ExternalLink size={ICON.xs} className="text-accent" />
        <span>{t('files.openSystem')}</span>
        <ChevronDown
          size={ICON.xs}
          className={`transition-transform ${open ? 'rotate-180' : ''}`}
        />
      </button>
      <Popover
        open={open}
        onClose={() => setOpen(false)}
        anchor={triggerRef.current}
        role="menu"
        align="end"
        keyboard
        panelClassName="min-w-52 rounded-control border border-edge bg-panel p-1 shadow-popover"
      >
        <button
          role="menuitem"
          className="flex w-full items-center gap-2 rounded-control px-2.5 py-1.5 text-left text-xs text-fg hover:bg-panel2"
          onClick={() => run(() => api.openPath(tab.path))}
        >
          <ExternalLink size={ICON.xs} className="text-dim" />
          {t('files.openSystem')}
        </button>
        <button
          role="menuitem"
          className="flex w-full items-center gap-2 rounded-control px-2.5 py-1.5 text-left text-xs text-fg hover:bg-panel2"
          onClick={() => run(() => api.openArtifactWith(tab.path))}
        >
          <ExternalLink size={ICON.xs} className="text-dim" />
          {t('files.openWith')}
        </button>
        <button
          role="menuitem"
          className="flex w-full items-center gap-2 rounded-control px-2.5 py-1.5 text-left text-xs text-fg hover:bg-panel2"
          onClick={() => run(() => api.revealArtifact(tab.path))}
        >
          <FolderOpen size={ICON.xs} className="text-dim" />
          {t('files.reveal')}
        </button>
        <div role="separator" className="my-1 border-t border-edge" />
        <button
          role="menuitem"
          className="flex w-full items-center gap-2 rounded-control px-2.5 py-1.5 text-left text-xs text-fg hover:bg-panel2"
          onClick={() => void copy('path')}
        >
          <Copy size={ICON.xs} className="text-dim" />
          {copied === 'path' ? t('files.copied') : t('files.copyPath')}
        </button>
        <button
          role="menuitem"
          className="flex w-full items-center gap-2 rounded-control px-2.5 py-1.5 text-left text-xs text-fg hover:bg-panel2"
          onClick={() => void copy('content')}
        >
          <Copy size={ICON.xs} className="text-dim" />
          {copied === 'content' ? t('files.copied') : t('files.copyContent')}
        </button>
      </Popover>
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
  const uiSettings = useStore((s) => s.uiSettings);
  const setUISettings = useStore((s) => s.setUISettings);
  const showHidden = uiSettings.showHiddenFiles;
  const treeDir = useStore((s) => s.viewers[sessionID]?.fileTreeDir ?? '.');
  const [query, setQuery] = useState('');
  const [hits, setHits] = useState<{ path: string; is_dir: boolean }[]>([]);
  const [entries, setEntries] = useState<Record<string, FileNode[]>>({});
  const [expanded, setExpanded] = useState<Record<string, boolean>>({
    '.': true,
  });

  // The switch is a durable preference (desktop.json), so the panel
  // writes it the way Settings > Interface does and rolls the applied
  // value back when the document rejects the save.
  const toggleHidden = () => {
    const next = { ...uiSettings, showHiddenFiles: !showHidden };
    setUISettings(next);
    void api.setUISettings(next).catch(() => setUISettings(uiSettings));
  };

  // The panel is a floating surface, so Escape goes through the shared
  // layer stack: opening a menu or a preview on top of it hands the key
  // to that surface instead of closing the panel underneath.
  const panelRef = useRef<HTMLDivElement | null>(null);
  useOverlayLayer({
    active: true,
    containerRef: panelRef,
    onDismiss: onClose,
    trap: false,
    lock: false,
    restoreFocus: false,
  });

  // Flipping the switch changes what every directory holds; stale
  // children would keep rendering the previous answer, so collapse back
  // to the root and let the listing effect below refill it.
  useEffect(() => {
    setEntries({});
    setExpanded({ '.': true });
  }, [showHidden]);

  useEffect(() => {
    if (!expanded['.']) return;
    void api
      .listDir('.', showHidden)
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
  }, [treeDir, showHidden]);

  useEffect(() => {
    const q = query.trim();
    if (!q) {
      setHits([]);
      return;
    }
    const timer = window.setTimeout(() => {
      void api
        .searchFiles(q, 50, showHidden)
        .then((h) => setHits(h))
        .catch(() => setHits([]));
    }, 150);
    return () => window.clearTimeout(timer);
  }, [query, showHidden]);

  const toggle = (rel: string) => {
    const next = { ...expanded, [rel]: !expanded[rel] };
    setExpanded(next);
    if (next[rel] && !entries[rel]) {
      void api
        .listDir(rel, showHidden)
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
                  className={`flex w-full items-center gap-1 truncate rounded-tight px-1 py-0.5 text-left text-xs hover:bg-panel2 ${
                    treeDir === childRel ? 'text-fg' : 'text-dim hover:text-fg'
                  }`}
                >
                  {childOpen ? (
                    <ChevronDown size={ICON.xs} className="shrink-0" />
                  ) : (
                    <ChevronRight size={ICON.xs} className="shrink-0" />
                  )}
                  {childOpen ? (
                    <FolderOpen
                      size={ICON.sm}
                      className="shrink-0 text-accent"
                    />
                  ) : (
                    <Folder size={ICON.sm} className="shrink-0 text-accent" />
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
              className="flex w-full items-center gap-1 rounded-tight px-1 py-0.5 text-left text-xs text-dim hover:bg-panel2 hover:text-fg"
              data-tip={n.path}
            >
              <FileCode size={ICON.xs} className="shrink-0" />
              <span className="truncate">{n.name}</span>
            </button>
          ))}
      </div>
    );
  };

  return (
    <div
      ref={panelRef}
      className="absolute inset-y-0 right-0 z-[var(--oc-z-raised)] flex w-80 max-w-[85%] flex-col border-l border-edge bg-panel shadow-popover"
    >
      <div className="flex h-9 shrink-0 items-center gap-1.5 border-b border-edge px-2">
        <Search size={ICON.xs} className="shrink-0 text-dim" />
        <input
          autoFocus
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t('files.quickOpenHint')}
          className="min-w-0 flex-1 bg-transparent text-xs outline-none"
        />
        <button
          onClick={toggleHidden}
          aria-pressed={showHidden}
          aria-label={
            showHidden ? t('files.hideHidden') : t('files.showHidden')
          }
          data-tip={showHidden ? t('files.hideHidden') : t('files.showHidden')}
          className={`grid h-6 w-6 shrink-0 place-items-center rounded-tight hover:bg-panel2 ${
            showHidden ? 'text-accent' : 'text-dim hover:text-fg'
          }`}
        >
          {showHidden ? <Eye size={ICON.xs} /> : <EyeOff size={ICON.xs} />}
        </button>
        <button
          onClick={onClose}
          className="grid h-6 w-6 shrink-0 place-items-center rounded-tight text-dim hover:bg-panel2 hover:text-fg"
          aria-label={t('files.hideTree')}
        >
          <X size={ICON.xs} />
        </button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-1.5">
        {query.trim() ? (
          hits.length === 0 ? (
            <div className="px-2 py-3 text-center text-micro text-dim">
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
                className="flex w-full items-center gap-1.5 truncate rounded-tight px-2 py-1 text-left text-xs text-dim hover:bg-panel2 hover:text-fg"
              >
                {hit.is_dir ? (
                  <Folder size={ICON.xs} className="shrink-0 text-accent" />
                ) : (
                  <FileCode size={ICON.xs} className="shrink-0" />
                )}
                <span className="truncate">{hit.path}</span>
              </button>
            ))
          )
        ) : (
          <>
            <div className="flex items-center gap-1 px-1 pb-1 text-micro font-semibold uppercase tracking-wider text-dim">
              {t('files.workspace')}
            </div>
            <button
              onClick={() => toggle('.')}
              className="flex w-full items-center gap-1 rounded-tight px-1 py-0.5 text-xs text-dim hover:bg-panel2 hover:text-fg"
            >
              {expanded['.'] ? (
                <ChevronDown size={ICON.xs} />
              ) : (
                <ChevronRight size={ICON.xs} />
              )}
              {expanded['.'] ? (
                <FolderOpen size={ICON.sm} className="text-accent" />
              ) : (
                <Folder size={ICON.sm} className="text-accent" />
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
