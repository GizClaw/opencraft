// GitPanel is the Git view of the right rail. It shows the change
// snapshot of the whole repository containing the active workspace
// (not just the workspace subtree), an inline diff preview per path,
// commit history and branch switching, and executes git write
// operations through the thin Git binding. Writes are disabled while
// the conversation turn runs; the backend also refuses writes while
// any run is active on the current workspace's Host. The
// in_workspace marker is informational: entries outside the workspace
// subtree are shown and remain fully operable.
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { useTranslation } from 'react-i18next';
import {
  AlertTriangle,
  ArrowDown,
  ArrowUp,
  Check,
  ChevronDown,
  ChevronRight,
  CircleCheck,
  CircleDot,
  Folder,
  FolderOpen,
  GitBranch as GitBranchIcon,
  GitBranchPlus,
  GitCommitHorizontal,
  GitPullRequest,
  History,
  List,
  ListTree,
  Loader2,
  PlusCircle,
  RefreshCw,
  Trash2,
  Undo2,
  UploadCloud,
  X,
} from 'lucide-react';
import { api } from '../lib/api';
import { parseUnifiedDiff } from '../lib/diff';
import { useStore } from '../lib/store';
import { useConversationState } from '../state/react';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import { dateLabel } from '../lib/format';
import type {
  GitBranch,
  GitChange,
  GitChangeKind,
  GitDiff,
  GitLogEntry,
  GitRepo,
  GitStatus,
} from '../lib/types';
import { PRView } from './PRView';
import { CommitDetailModal } from './CommitDetailModal';
import { GitDiffView } from './viewer/DiffView';

const KIND_MARK: Record<GitChangeKind, string> = {
  added: 'A',
  modified: 'M',
  deleted: 'D',
  renamed: 'R',
  copied: 'C',
  typechange: 'T',
  untracked: '?',
  unmerged: 'U',
};

function kindClass(kind: GitChangeKind): string {
  switch (kind) {
    case 'added':
      return 'bg-ok/15 text-ok';
    case 'deleted':
      return 'bg-err/15 text-err';
    case 'unmerged':
      return 'bg-warn/15 text-warn';
    case 'untracked':
      return 'bg-dim/10 text-dim';
    default:
      return 'bg-accent/15 text-accent';
  }
}

function groupEntries(entries: GitChange[]): {
  staged: GitChange[];
  unstaged: GitChange[];
  untracked: GitChange[];
  unmerged: GitChange[];
} {
  const groups = { staged: [], unstaged: [], untracked: [], unmerged: [] } as {
    staged: GitChange[];
    unstaged: GitChange[];
    untracked: GitChange[];
    unmerged: GitChange[];
  };
  for (const e of entries) {
    if (e.unmerged) groups.unmerged.push(e);
    else if (e.untracked) groups.untracked.push(e);
    else if (e.staged) groups.staged.push(e);
    else groups.unstaged.push(e);
  }
  return groups;
}

interface ConfirmSpec {
  title: string;
  body: string;
  confirmLabel: string;
  danger: boolean;
  action: () => Promise<void>;
}

// DiffSide picks which half of a dual-state file (both staged and
// unstaged changes) the diff modal previews.
type DiffSide = 'staged' | 'worktree';

export function GitPanel({ sessionID }: { sessionID: string }) {
  const { t } = useTranslation();
  const workspace = useStore((s) => s.workspace);
  const flash = useStore((s) => s.flash);
  const conversationState = useConversationState(sessionID);
  const turnState = conversationState?.turn;
  const busy = turnState?.name === 'starting' || turnState?.name === 'running';
  const [repo, setRepo] = useState<GitRepo | null>(null);
  const [status, setStatus] = useState<GitStatus | null>(null);
  const [logs, setLogs] = useState<GitLogEntry[]>([]);
  const [branches, setBranches] = useState<GitBranch[]>([]);
  const [view, setView] = useState<'changes' | 'history' | 'pr'>('changes');
  const [prAvailable, setPrAvailable] = useState(false);
  const [prNonce, setPrNonce] = useState(0);
  const [listMode, setListMode] = useState<'list' | 'tree'>('list');
  // pollMs is the status auto-refresh interval in milliseconds; 0
  // disables the background poll (event-driven refreshes stay on).
  const [pollMs, setPollMs] = useState(5000);
  const [selected, setSelected] = useState<GitChange | null>(null);
  const [commitEntry, setCommitEntry] = useState<GitLogEntry | null>(null);
  const [detail, setDetail] = useState<{
    diff: GitDiff | null;
    label: string;
    message: string;
    error?: string;
  } | null>(null);
  // diffSide remembers the user's staged/worktree choice while they
  // navigate between changed files; single-state files ignore it.
  const [diffSide, setDiffSide] = useState<DiffSide>('staged');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [branchOpen, setBranchOpen] = useState(false);
  const [commitMsg, setCommitMsg] = useState('');
  const [op, setOp] = useState<string | null>(null);
  const [confirm, setConfirm] = useState<ConfirmSpec | null>(null);
  const [forceOpen, setForceOpen] = useState(false);
  const [forceBranch, setForceBranch] = useState('');
  const [newBranchOpen, setNewBranchOpen] = useState(false);
  const [newBranchName, setNewBranchName] = useState('');
  const [spinning, setSpinning] = useState(false);
  const spinCount = useRef(0);
  const spinStartedAt = useRef(0);
  const spinEndTimer = useRef<number | null>(null);
  const prevBusy = useRef(busy);

  const beginSpin = useCallback(() => {
    if (spinEndTimer.current !== null) {
      window.clearTimeout(spinEndTimer.current);
      spinEndTimer.current = null;
    }
    if (spinCount.current === 0) spinStartedAt.current = Date.now();
    spinCount.current += 1;
    setSpinning(true);
  }, []);

  const endSpin = useCallback(() => {
    spinCount.current = Math.max(0, spinCount.current - 1);
    if (spinCount.current > 0) return;
    // Keep the icon visibly rotating even for very fast refreshes.
    const elapsed = Date.now() - spinStartedAt.current;
    const remaining = Math.max(0, 260 - elapsed);
    spinEndTimer.current = window.setTimeout(() => {
      spinEndTimer.current = null;
      setSpinning(false);
    }, remaining);
  }, []);

  useEffect(
    () => () => {
      if (spinEndTimer.current !== null) {
        window.clearTimeout(spinEndTimer.current);
      }
    },
    [],
  );

  const refresh = useCallback(
    async (silent = false) => {
      beginSpin();
      if (!silent) setLoading(true);
      setError('');
      try {
        const [repoInfo, statusInfo, history, branchList, prProbe] =
          await Promise.all([
            api.gitRepo(),
            api.gitStatus(),
            api.gitLog(50),
            api.gitBranches(),
            api.gitHubAvailable(),
          ]);
        setRepo(repoInfo);
        setStatus(statusInfo);
        setLogs(history);
        setBranches(branchList);
        const available = repoInfo.in_repo && prProbe.available;
        setPrAvailable(available);
        setView((v) => (v === 'pr' && !available ? 'changes' : v));
        if (!repoInfo.in_repo) {
          setSelected(null);
          setDetail(null);
        }
      } catch (err) {
        setError(String(err));
      } finally {
        endSpin();
        setPrNonce((n) => n + 1);
        if (!silent) setLoading(false);
      }
    },
    [beginSpin, endSpin],
  );

  // Refetch on mount and whenever the workspace changes; the Git view
  // is scoped to the whole repository of the active workspace even
  // though it lives inside one chat's rail.
  useEffect(() => {
    void refresh();
  }, [refresh, workspace]);

  // Other panels or chats may run git writes too; the binding emits
  // git_changed after every successful mutation, so refresh when the
  // workspace repository changes underneath this panel.
  useEffect(() => {
    const off = EventsOn('opencraft:ui', (ev: unknown) => {
      const msg = ev as { type?: string };
      // git_changed covers UI writes from any panel; turn_end covers
      // agent/automation turns that may have touched the repository.
      if (msg?.type === 'git_changed' || msg?.type === 'turn_end') {
        void refresh(true);
      }
    });
    return () => off();
  }, [refresh]);

  // A finished conversation turn may have changed files (or created
  // the repository itself); refresh when the busy edge falls.
  useEffect(() => {
    if (prevBusy.current && !busy) void refresh();
    prevBusy.current = busy;
  }, [busy, refresh]);

  // External editors change files without backend events; a light poll
  // of the status snapshot keeps the panel current. Log/branches stay
  // event-driven so the poll stays one bounded git status call.
  const pollStatus = useCallback(async () => {
    beginSpin();
    try {
      const snapshot = await api.gitStatus();
      setStatus(snapshot);
    } catch {
      // Transient failures (repo removed mid-poll) are fine: the
      // WorkspacePanel probe decides whether the Git segment stays.
    } finally {
      endSpin();
    }
  }, [beginSpin, endSpin]);
  useEffect(() => {
    if (pollMs <= 0) return;
    let stopped = false;
    let timer: number | undefined;
    const tick = async () => {
      if (stopped) return;
      await pollStatus();
      if (!stopped) timer = window.setTimeout(tick, pollMs);
    };
    timer = window.setTimeout(tick, pollMs);
    return () => {
      stopped = true;
      if (timer) window.clearTimeout(timer);
    };
  }, [pollStatus, pollMs]);

  // Returning to the window after editing outside the app refreshes
  // immediately instead of waiting for the next poll tick.
  useEffect(() => {
    const onFocus = () => void refresh(true);
    window.addEventListener('focus', onFocus);
    return () => window.removeEventListener('focus', onFocus);
  }, [refresh]);

  const groups = useMemo(() => groupEntries(status?.entries ?? []), [status]);
  // ordered is the single navigation order used by the diff modal's
  // previous/next controls (conflicts first, then staged, unstaged and
  // untracked, matching the flat list sections).
  const ordered = useMemo(
    () => [
      ...groups.unmerged,
      ...groups.staged,
      ...groups.unstaged,
      ...groups.untracked,
    ],
    [groups],
  );
  const selectedIndex = selected
    ? ordered.findIndex((e) => e.path === selected.path)
    : -1;

  const closeDiff = useCallback(() => {
    setSelected(null);
    setDetail(null);
  }, []);

  const runWrite = useCallback(
    async (label: string, fn: () => Promise<unknown>) => {
      setOp(label);
      setConfirm(null);
      try {
        const out = await fn();
        if (typeof out === 'string' && out) {
          flash(out.length > 2000 ? `${out.slice(0, 2000)}…` : out);
        }
        await refresh();
      } catch (err) {
        flash(String(err));
      } finally {
        setOp(null);
      }
    },
    [flash, refresh],
  );

  const loadSide = useCallback(async (e: GitChange, side: DiffSide) => {
    const cached = side === 'staged';
    setDetail(null);
    if (e.untracked || e.kind === 'untracked') {
      setDetail({ diff: null, label: '', message: 'untrackedNoDiff' });
      return;
    }
    if (e.unmerged) {
      setDetail({ diff: null, label: '', message: 'unmergedHint' });
      return;
    }
    if (e.is_binary) {
      setDetail({ diff: null, label: '', message: 'binaryNoDiff' });
      return;
    }
    try {
      const diff = await api.gitDiff(e.path, cached);
      setDetail({
        diff,
        label: cached ? 'stagedDiff' : 'worktreeDiff',
        message: '',
      });
    } catch (err) {
      setDetail({ diff: null, label: '', message: '', error: String(err) });
    }
  }, []);

  const loadDiff = useCallback(
    async (e: GitChange) => {
      setSelected(e);
      setDetail(null);
      if (e.untracked || e.kind === 'untracked') {
        setDetail({ diff: null, label: '', message: 'untrackedNoDiff' });
        return;
      }
      if (e.unmerged) {
        setDetail({ diff: null, label: '', message: 'unmergedHint' });
        return;
      }
      if (e.is_binary) {
        setDetail({ diff: null, label: '', message: 'binaryNoDiff' });
        return;
      }
      // Dual-state entries live under the Staged group; start on the
      // staged half and let the modal toggle to the working tree.
      const side: DiffSide =
        e.staged && e.unstaged ? diffSide : e.staged ? 'staged' : 'worktree';
      await loadSide(e, side);
    },
    [diffSide, loadSide],
  );

  const openAt = useCallback(
    (index: number) => {
      const entry = ordered[index];
      if (entry) void loadDiff(entry);
    },
    [loadDiff, ordered],
  );

  // Modal keyboard support: Escape closes, ArrowUp/ArrowDown move
  // between changed files.
  useEffect(() => {
    if (!selected) return;
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        closeDiff();
      } else if (e.key === 'ArrowDown') {
        e.preventDefault();
        openAt(selectedIndex + 1);
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        openAt(selectedIndex - 1);
      }
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [selected, selectedIndex, openAt, closeDiff]);

  const parsedFiles = useMemo(() => {
    if (!detail?.diff?.content) return null;
    return parseUnifiedDiff(detail.diff.content);
  }, [detail]);

  const disabled = busy || op !== null;

  if (repo && !repo.in_repo) {
    return (
      <div className="grid h-full place-items-center text-xs text-dim">
        {t('git.notRepo')}
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col bg-panel">
      <div className="flex h-9 shrink-0 items-center gap-1.5 border-b border-edge px-2">
        <BranchMenu
          repo={repo}
          branches={branches}
          open={branchOpen}
          disabled={disabled}
          onToggle={() => setBranchOpen((v) => !v)}
          onPick={(name) =>
            void runWrite('checkout', () => api.gitCheckout(name))
          }
          onNew={() => {
            setNewBranchName('');
            setNewBranchOpen(true);
          }}
        />
        {repo?.upstream ? (
          <span className="shrink-0 text-[0.7143rem] text-dim tabular-nums">
            {repo.ahead > 0 && `↑${repo.ahead} `}
            {repo.behind > 0 && `↓${repo.behind}`}
          </span>
        ) : null}
        <span className="flex-1" />
        <HeaderAction
          label={t('git.pull')}
          disabled={disabled || !repo?.upstream}
          onClick={() => void runWrite('pull', () => api.gitPull())}
        >
          <GitPullRequest size="0.8571rem" />
        </HeaderAction>
        <HeaderAction
          label={t('git.push')}
          disabled={disabled || !repo?.upstream}
          onClick={() => void runWrite('push', () => api.gitPush(false))}
        >
          <UploadCloud size="0.8571rem" />
        </HeaderAction>
        <HeaderAction
          label={t('git.forcePush')}
          disabled={disabled || !repo?.upstream}
          onClick={() => {
            setForceBranch('');
            setForceOpen(true);
          }}
        >
          <AlertTriangle size="0.8571rem" />
        </HeaderAction>
        <RefreshControl
          spinning={spinning || loading}
          value={pollMs}
          onChange={setPollMs}
          onRefresh={() => void refresh()}
        />
      </div>

      <div className="flex h-9 shrink-0 items-center gap-1 border-b border-edge px-2">
        <ViewToggle
          active={view === 'changes'}
          icon={<GitCommitHorizontal size="0.8571rem" />}
          label={t('git.changes')}
          onClick={() => setView('changes')}
        />
        <ViewToggle
          active={view === 'history'}
          icon={<History size="0.8571rem" />}
          label={t('git.history')}
          onClick={() => setView('history')}
        />
        {prAvailable && (
          <ViewToggle
            active={view === 'pr'}
            icon={<GitPullRequest size="0.8571rem" />}
            label={t('git.pr')}
            onClick={() => setView('pr')}
          />
        )}
        {view === 'changes' && (
          <>
            {groups.staged.length > 0 && (
              <span className="ml-auto text-[0.7143rem] text-dim tabular-nums">
                {groups.staged.length} {t('git.staged').toLowerCase()}
              </span>
            )}
            <div
              className={`flex items-center gap-0.5 ${
                groups.staged.length === 0 ? 'ml-auto' : 'ml-2'
              }`}
            >
              <ViewToggle
                active={listMode === 'list'}
                icon={<List size="0.8571rem" />}
                label={t('git.listView')}
                onClick={() => setListMode('list')}
              />
              <ViewToggle
                active={listMode === 'tree'}
                icon={<ListTree size="0.8571rem" />}
                label={t('git.treeView')}
                onClick={() => setListMode('tree')}
              />
            </div>
          </>
        )}
      </div>

      {error ? (
        <div className="flex items-start gap-2 border-b border-err/20 bg-err/5 px-3 py-2 text-xs text-err">
          <AlertTriangle size="0.8571rem" className="mt-0.5 shrink-0" />
          <span className="break-words">{error}</span>
        </div>
      ) : null}

      <div className="min-h-0 flex-1">
        {loading && !status ? (
          <div className="grid h-full place-items-center text-dim">
            <Loader2 size="1.1429rem" className="animate-spin" />
          </div>
        ) : view === 'pr' && prAvailable ? (
          <PRView nonce={prNonce} />
        ) : view === 'changes' ? (
          <ChangesView
            groups={groups}
            listMode={listMode}
            selectedPath={selected?.path ?? null}
            truncated={status?.truncated ?? false}
            disabled={disabled}
            onPick={(e) => void loadDiff(e)}
            onStage={(paths) =>
              void runWrite('stage', () => api.gitStage(paths))
            }
            onUnstage={(paths) =>
              void runWrite('unstage', () => api.gitUnstage(paths))
            }
            onDiscard={(e, staged) =>
              setConfirm({
                title: staged
                  ? t('git.confirmDiscardStaged')
                  : t('git.confirmDiscard'),
                body: e.path,
                confirmLabel: t('git.discard'),
                danger: true,
                action: () =>
                  runWrite('discard', () => api.gitDiscard([e.path], staged)),
              })
            }
            onClean={(e) =>
              setConfirm({
                title: t('git.confirmClean'),
                body: e.path,
                confirmLabel: t('git.clean'),
                danger: true,
                action: () => runWrite('clean', () => api.gitClean([e.path])),
              })
            }
          />
        ) : (
          <HistoryView entries={logs} onPick={setCommitEntry} />
        )}
      </div>

      {view === 'changes' && (
        <CommitBar
          value={commitMsg}
          disabled={disabled || groups.staged.length === 0}
          onChange={setCommitMsg}
          onCommit={() =>
            void runWrite('commit', async () => {
              const msg = commitMsg.trim();
              setCommitMsg('');
              return api.gitCommit(msg);
            })
          }
        />
      )}

      {view === 'changes' && selected && (
        <DiffModal
          entry={selected}
          detail={detail}
          files={parsedFiles}
          index={selectedIndex}
          total={ordered.length}
          side={diffSide}
          dual={
            selected.staged &&
            selected.unstaged &&
            !selected.is_binary &&
            !selected.unmerged
          }
          onSide={(side) => {
            setDiffSide(side);
            void loadSide(selected, side);
          }}
          onPrev={() => openAt(selectedIndex - 1)}
          onNext={() => openAt(selectedIndex + 1)}
          onClose={closeDiff}
        />
      )}

      {confirm && (
        <ConfirmDialog spec={confirm} onCancel={() => setConfirm(null)} />
      )}
      {forceOpen && (
        <ForcePushDialog
          branch={repo?.branch ?? ''}
          value={forceBranch}
          onChange={setForceBranch}
          onCancel={() => setForceOpen(false)}
          onConfirm={() => {
            setForceOpen(false);
            setForceBranch('');
            void runWrite('force_push', () => api.gitPush(true));
          }}
        />
      )}
      {newBranchOpen && (
        <NewBranchDialog
          value={newBranchName}
          onChange={setNewBranchName}
          onCancel={() => setNewBranchOpen(false)}
          onConfirm={() => {
            const name = newBranchName.trim();
            setNewBranchOpen(false);
            setNewBranchName('');
            void runWrite('new_branch', () => api.gitNewBranch(name));
          }}
        />
      )}
      {view === 'history' && commitEntry && (
        <CommitDetailModal
          entry={commitEntry}
          onClose={() => setCommitEntry(null)}
        />
      )}
    </div>
  );
}

function ViewToggle({
  active,
  icon,
  label,
  onClick,
}: {
  active: boolean;
  icon: ReactNode;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      className={`flex items-center gap-1.5 rounded-lg px-2.5 py-1 text-xs transition-colors ${
        active
          ? 'bg-accent/15 text-accent'
          : 'text-dim hover:bg-panel2 hover:text-fg'
      }`}
    >
      {icon}
      {label}
    </button>
  );
}

function BranchMenu({
  repo,
  branches,
  open,
  disabled,
  onToggle,
  onPick,
  onNew,
}: {
  repo: GitRepo | null;
  branches: GitBranch[];
  open: boolean;
  disabled: boolean;
  onToggle: () => void;
  onPick: (name: string) => void;
  onNew: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="relative">
      <button
        onClick={onToggle}
        disabled={disabled}
        aria-label={t('git.switchBranch')}
        className="flex items-center gap-1.5 rounded-lg border border-edge px-2 py-1 text-xs text-fg hover:border-accent/50 disabled:opacity-50"
        title={t('git.switchBranch')}
      >
        <GitBranchIcon size="0.8571rem" className="text-accent" />
        <span className="max-w-40 truncate font-mono">
          {repo?.branch || '…'}
        </span>
        <ChevronDown size="0.7857rem" className="text-dim" />
      </button>
      {open && (
        <>
          <div className="fixed inset-0 z-30" onClick={onToggle} />
          <div className="absolute top-full left-0 z-40 mt-1.5 max-h-72 w-64 overflow-y-auto rounded-lg border border-edge bg-panel py-1 shadow-xl">
            {branches.map((b) => (
              <button
                key={b.name}
                disabled={disabled || b.current}
                onClick={() => {
                  onToggle();
                  if (!b.current) onPick(b.name);
                }}
                className={`flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs ${
                  b.current
                    ? 'text-accent'
                    : 'text-dim hover:bg-panel2 hover:text-fg disabled:opacity-50'
                }`}
              >
                <span className="min-w-0 flex-1 truncate font-mono">
                  {b.name}
                </span>
                {b.current && <Check size="0.8571rem" />}
              </button>
            ))}
            <div className="border-t border-edge p-1">
              <button
                onClick={() => {
                  onToggle();
                  onNew();
                }}
                className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs text-dim hover:bg-panel2 hover:text-fg"
              >
                <GitBranchPlus size="0.8571rem" />
                {t('git.newBranch')}
              </button>
            </div>
          </div>
        </>
      )}
    </div>
  );
}

function HeaderAction({
  label,
  disabled,
  onClick,
  children,
}: {
  label: string;
  disabled: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      title={label}
      aria-label={label}
      className="grid h-7 w-7 place-items-center rounded-lg text-dim hover:bg-panel2 hover:text-fg disabled:cursor-not-allowed disabled:opacity-40"
    >
      {children}
    </button>
  );
}

const REFRESH_INTERVALS_MS = [0, 5000, 10_000, 30_000, 60_000];

// RefreshControl is the split refresh button: the icon triggers an
// immediate refresh and the adjacent picker sets the auto-refresh
// interval (Off / 5s / 10s / 30s / 60s), styled like the automation
// create split button.
function RefreshControl({
  spinning,
  value,
  onChange,
  onRefresh,
}: {
  spinning: boolean;
  value: number;
  onChange: (ms: number) => void;
  onRefresh: () => void;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onDown = (e: PointerEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener('pointerdown', onDown);
    return () => document.removeEventListener('pointerdown', onDown);
  }, [open]);
  const label = value > 0 ? `${value / 1000} s` : t('git.off');
  return (
    <div className="relative" ref={ref}>
      <div className="flex items-center overflow-hidden rounded-lg border border-edge">
        <button
          onClick={() => {
            setOpen(false);
            onRefresh();
          }}
          className="grid h-7 w-7 place-items-center text-dim hover:bg-panel2 hover:text-fg"
          title={t('git.refresh')}
          aria-label={t('git.refresh')}
        >
          <RefreshCw
            size="0.9286rem"
            className={spinning ? 'animate-spin' : ''}
          />
        </button>
        <button
          onClick={() => setOpen((v) => !v)}
          className="flex h-7 items-center gap-1 border-l border-edge px-1.5 text-[0.7143rem] text-dim hover:bg-panel2 hover:text-fg"
          title={t('git.autoRefreshInterval')}
          aria-label={t('git.autoRefreshInterval')}
          aria-haspopup="listbox"
          aria-expanded={open}
        >
          {label}
          <ChevronDown size="0.7857rem" />
        </button>
      </div>
      {open && (
        <div
          role="listbox"
          className="absolute right-0 top-full z-40 mt-1.5 w-44 rounded-lg border border-edge bg-panel p-1 shadow-xl"
        >
          {REFRESH_INTERVALS_MS.map((ms) => {
            const optionLabel = ms > 0 ? `${ms / 1000} s` : t('git.off');
            const active = value === ms;
            return (
              <button
                key={ms}
                role="option"
                aria-selected={active}
                onClick={() => {
                  setOpen(false);
                  onChange(ms);
                }}
                className={`flex w-full items-center justify-between rounded-md px-2 py-1.5 text-left text-xs ${
                  active
                    ? 'bg-accent/10 text-accent'
                    : 'text-dim hover:bg-panel2 hover:text-fg'
                }`}
              >
                <span>{optionLabel}</span>
                {active && <Check size="0.8571rem" />}
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}

function SectionLabel({ label, count }: { label: string; count: number }) {
  if (count === 0) return null;
  return (
    <div className="sticky top-0 z-10 bg-panel px-3 pb-1 pt-2 text-[0.7143rem] font-medium uppercase tracking-wide text-dim">
      {label} <span className="tabular-nums">{count}</span>
    </div>
  );
}

// ChangeStateIcon colors the index state of one change: staged entries
// get a solid accent check, working-tree changes an amber dot, and
// untracked entries a muted dot. Conflicts use a warning triangle.
function ChangeStateIcon({ entry }: { entry: GitChange }) {
  if (entry.unmerged) {
    return (
      <AlertTriangle
        size="0.7857rem"
        className="shrink-0 text-warn"
        aria-label="unmerged"
      />
    );
  }
  if (entry.untracked) {
    return <CircleDot size="0.7857rem" className="shrink-0 text-dim" />;
  }
  return (
    <span className="flex shrink-0 items-center">
      {entry.staged && (
        <CircleCheck
          size="0.7857rem"
          className="text-accent"
          aria-label="staged"
        />
      )}
      {entry.unstaged && (
        <CircleDot
          size="0.7857rem"
          className={entry.staged ? '-ml-1 text-warn' : 'text-warn'}
          aria-label="unstaged"
        />
      )}
    </span>
  );
}

function ChangeRow({
  entry,
  selected,
  disabled,
  onPick,
  onAction,
  depth = 0,
  label,
}: {
  entry: GitChange;
  selected: boolean;
  disabled: boolean;
  onPick: () => void;
  onAction: (action: 'stage' | 'unstage' | 'discard' | 'clean') => void;
  depth?: number;
  label?: string;
}) {
  const { t } = useTranslation();
  const mark = KIND_MARK[entry.kind] ?? '?';
  const name = label ?? entry.path;
  return (
    <div
      className={`group flex items-center gap-2 px-3 py-1.5 ${
        selected ? 'bg-accent/10' : 'hover:bg-panel2'
      }`}
      style={depth ? { paddingLeft: 12 + depth * 16 } : undefined}
    >
      <button
        onClick={onPick}
        className="flex min-w-0 flex-1 items-center gap-2 text-left text-xs"
      >
        <ChangeStateIcon entry={entry} />
        <span
          className={`grid h-4 w-4 shrink-0 place-items-center rounded font-mono text-[0.6429rem] ${kindClass(
            entry.kind,
          )}`}
        >
          {mark}
        </span>
        <span
          className="min-w-0 flex-1 truncate font-mono text-fg"
          title={entry.path}
        >
          {name}
        </span>
        {!entry.in_workspace && (
          <span className="shrink-0 rounded border border-edge px-1 text-[0.6429rem] text-dim">
            {t('git.outsideWorkspace')}
          </span>
        )}
        {!entry.is_binary && (entry.additions > 0 || entry.deletions > 0) && (
          <span className="shrink-0 text-[0.7143rem] tabular-nums">
            <span className="text-ok">+{entry.additions}</span>{' '}
            <span className="text-err">−{entry.deletions}</span>
          </span>
        )}
      </button>
      <span className="flex shrink-0 items-center gap-0.5 opacity-70 group-hover:opacity-100">
        {!entry.unmerged && (entry.unstaged || entry.untracked) && (
          <RowAction
            label={t('git.stage')}
            disabled={disabled}
            onClick={() => onAction('stage')}
          >
            <PlusCircle size="0.8571rem" className="text-ok" />
          </RowAction>
        )}
        {entry.staged && (
          <RowAction
            label={t('git.unstage')}
            disabled={disabled}
            onClick={() => onAction('unstage')}
          >
            <Undo2 size="0.8571rem" />
          </RowAction>
        )}
        {!entry.untracked && !entry.unmerged && (
          <RowAction
            label={t('git.discard')}
            disabled={disabled}
            onClick={() => onAction('discard')}
          >
            <Trash2 size="0.8571rem" className="text-err" />
          </RowAction>
        )}
        {entry.untracked && (
          <RowAction
            label={t('git.clean')}
            disabled={disabled}
            onClick={() => onAction('clean')}
          >
            <Trash2 size="0.8571rem" className="text-err" />
          </RowAction>
        )}
      </span>
    </div>
  );
}

function RowAction({
  label,
  disabled,
  onClick,
  children,
}: {
  label: string;
  disabled: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      title={label}
      aria-label={label}
      className="grid h-6 w-6 place-items-center rounded text-dim hover:bg-panel2 disabled:cursor-not-allowed disabled:opacity-40"
    >
      {children}
    </button>
  );
}

function ChangesView({
  groups,
  listMode,
  selectedPath,
  truncated,
  disabled,
  onPick,
  onStage,
  onUnstage,
  onDiscard,
  onClean,
}: {
  groups: ReturnType<typeof groupEntries>;
  listMode: 'list' | 'tree';
  selectedPath: string | null;
  truncated: boolean;
  disabled: boolean;
  onPick: (e: GitChange) => void;
  onStage: (paths: string[]) => void;
  onUnstage: (paths: string[]) => void;
  onDiscard: (e: GitChange, staged: boolean) => void;
  onClean: (e: GitChange) => void;
}) {
  const { t } = useTranslation();
  const empty =
    groups.staged.length === 0 &&
    groups.unstaged.length === 0 &&
    groups.untracked.length === 0 &&
    groups.unmerged.length === 0;
  if (empty) {
    return (
      <div className="grid h-full place-items-center text-xs text-dim">
        {t('git.noChanges')}
      </div>
    );
  }
  const sections: { label: string; items: GitChange[] }[] = [
    { label: t('git.unmerged'), items: groups.unmerged },
    { label: t('git.staged'), items: groups.staged },
    { label: t('git.workingTree'), items: groups.unstaged },
    { label: t('git.untracked'), items: groups.untracked },
  ];
  const all = [
    ...groups.unmerged,
    ...groups.staged,
    ...groups.unstaged,
    ...groups.untracked,
  ];
  return (
    <div className="h-full overflow-y-auto pb-1">
      {listMode === 'tree' ? (
        <ChangeTree
          entries={all}
          selectedPath={selectedPath}
          disabled={disabled}
          onPick={onPick}
          onStage={onStage}
          onUnstage={onUnstage}
          onDiscard={onDiscard}
          onClean={onClean}
        />
      ) : (
        sections.map(
          (section) =>
            section.items.length > 0 && (
              <div key={section.label}>
                <SectionLabel
                  label={section.label}
                  count={section.items.length}
                />
                {section.items.map((e) => (
                  <ChangeRow
                    key={`${e.path}-${e.orig_path ?? ''}`}
                    entry={e}
                    selected={selectedPath === e.path}
                    disabled={disabled}
                    onPick={() => onPick(e)}
                    onAction={(action) => {
                      if (action === 'stage') onStage([e.path]);
                      else if (action === 'unstage') onUnstage([e.path]);
                      else if (action === 'clean') onClean(e);
                      else onDiscard(e, e.staged && !e.unstaged);
                    }}
                  />
                ))}
              </div>
            ),
        )
      )}
      {truncated && (
        <div className="px-3 pt-2 text-[0.7143rem] text-dim">
          {t('git.truncatedList')}
        </div>
      )}
    </div>
  );
}

type ChangeTreeNode =
  | {
      kind: 'dir';
      name: string;
      path: string;
      children: ChangeTreeNode[];
      additions: number;
      deletions: number;
      stagedCount: number;
      unstagedCount: number;
      untrackedCount: number;
      unmergedCount: number;
    }
  | { kind: 'file'; entry: GitChange };

interface TreeAcc {
  dirs: Map<string, TreeAcc>;
  files: GitChange[];
}

// buildChangeTree nests changed paths under their directories and
// aggregates +/- counts per folder. Order is dirs-first, alphabetical,
// matching a file explorer.
function buildChangeTree(entries: GitChange[]): ChangeTreeNode[] {
  const root: TreeAcc = { dirs: new Map(), files: [] };
  for (const entry of entries) {
    const parts = entry.path.split('/');
    let acc = root;
    for (let i = 0; i < parts.length - 1; i++) {
      let next = acc.dirs.get(parts[i]);
      if (!next) {
        next = { dirs: new Map(), files: [] };
        acc.dirs.set(parts[i], next);
      }
      acc = next;
    }
    acc.files.push(entry);
  }
  return dirNodes(root, '');
}

function dirNodes(acc: TreeAcc, base: string): ChangeTreeNode[] {
  const dirNames = [...acc.dirs.keys()].sort((a, b) => a.localeCompare(b));
  const dirs: ChangeTreeNode[] = [];
  for (const name of dirNames) {
    const childAcc = acc.dirs.get(name)!;
    const children = dirNodes(childAcc, base ? `${base}/${name}` : name);
    const dir: ChangeTreeNode = {
      kind: 'dir',
      name,
      path: base ? `${base}/${name}` : name,
      children,
      additions: 0,
      deletions: 0,
      stagedCount: 0,
      unstagedCount: 0,
      untrackedCount: 0,
      unmergedCount: 0,
    };
    aggregateInto(dir, children);
    dirs.push(dir);
  }
  const files: ChangeTreeNode[] = acc.files
    .slice()
    .sort((a, b) => a.path.localeCompare(b.path))
    .map((entry) => ({ kind: 'file', entry }));
  return [...dirs, ...files];
}

function aggregateInto(
  dir: Extract<ChangeTreeNode, { kind: 'dir' }>,
  nodes: ChangeTreeNode[],
) {
  for (const node of nodes) {
    if (node.kind === 'file') {
      const e = node.entry;
      dir.additions += e.is_binary || e.untracked ? 0 : e.additions;
      dir.deletions += e.is_binary || e.untracked ? 0 : e.deletions;
      if (e.unmerged) dir.unmergedCount++;
      else {
        if (e.untracked) dir.untrackedCount++;
        if (e.staged) dir.stagedCount++;
        if (e.unstaged) dir.unstagedCount++;
      }
    } else {
      dir.additions += node.additions;
      dir.deletions += node.deletions;
      dir.stagedCount += node.stagedCount;
      dir.unstagedCount += node.unstagedCount;
      dir.untrackedCount += node.untrackedCount;
      dir.unmergedCount += node.unmergedCount;
    }
  }
}

function ChangeTree({
  entries,
  selectedPath,
  disabled,
  onPick,
  onStage,
  onUnstage,
  onDiscard,
  onClean,
}: {
  entries: GitChange[];
  selectedPath: string | null;
  disabled: boolean;
  onPick: (e: GitChange) => void;
  onStage: (paths: string[]) => void;
  onUnstage: (paths: string[]) => void;
  onDiscard: (e: GitChange, staged: boolean) => void;
  onClean: (e: GitChange) => void;
}) {
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});
  const nodes = useMemo(() => buildChangeTree(entries), [entries]);
  const rows: ReactNode[] = [];
  const visit = (node: ChangeTreeNode, depth: number) => {
    if (node.kind === 'file') {
      const e = node.entry;
      rows.push(
        <ChangeRow
          key={`${e.path}-${e.orig_path ?? ''}`}
          entry={e}
          selected={selectedPath === e.path}
          disabled={disabled}
          depth={depth}
          label={e.path.split('/').pop()}
          onPick={() => onPick(e)}
          onAction={(action) => {
            if (action === 'stage') onStage([e.path]);
            else if (action === 'unstage') onUnstage([e.path]);
            else if (action === 'clean') onClean(e);
            else onDiscard(e, e.staged && !e.unstaged);
          }}
        />,
      );
      return;
    }
    const open = !collapsed[node.path];
    rows.push(
      <DirRow
        key={node.path}
        node={node}
        depth={depth}
        open={open}
        onToggle={() =>
          setCollapsed((prev) => ({
            ...prev,
            [node.path]: !open,
          }))
        }
      />,
    );
    if (open) node.children.forEach((child) => visit(child, depth + 1));
  };
  nodes.forEach((node) => visit(node, 0));
  return <div>{rows}</div>;
}

function DirRow({
  node,
  depth,
  open,
  onToggle,
}: {
  node: Extract<ChangeTreeNode, { kind: 'dir' }>;
  depth: number;
  open: boolean;
  onToggle: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div
      className="flex h-7 items-center gap-1.5 px-3 text-xs text-fg hover:bg-panel2"
      style={{ paddingLeft: 12 + depth * 16 }}
    >
      <button
        onClick={onToggle}
        className="grid h-5 w-5 shrink-0 place-items-center rounded text-dim hover:text-fg"
        aria-label={`${node.name} ${open ? t('git.collapse') : t('git.expand')}`}
      >
        {open ? (
          <ChevronDown size="0.8571rem" />
        ) : (
          <ChevronRight size="0.8571rem" />
        )}
      </button>
      {open ? (
        <FolderOpen
          size="0.9286rem"
          className={
            node.unmergedCount > 0
              ? 'shrink-0 text-warn'
              : node.stagedCount > 0
                ? 'shrink-0 text-accent'
                : node.untrackedCount > 0
                  ? 'shrink-0 text-dim'
                  : 'shrink-0 text-warn'
          }
        />
      ) : (
        <Folder
          size="0.9286rem"
          className={
            node.unmergedCount > 0
              ? 'shrink-0 text-warn'
              : node.stagedCount > 0
                ? 'shrink-0 text-accent'
                : node.untrackedCount > 0
                  ? 'shrink-0 text-dim'
                  : 'shrink-0 text-warn'
          }
        />
      )}
      <span className="min-w-0 flex-1 truncate font-mono">{node.name}</span>
      {node.stagedCount > 0 && (
        <span className="shrink-0 text-[0.7143rem] text-accent tabular-nums">
          {node.stagedCount}
        </span>
      )}
      {node.unstagedCount > 0 && (
        <span className="shrink-0 text-[0.7143rem] text-warn tabular-nums">
          {node.unstagedCount}
        </span>
      )}
      {node.untrackedCount > 0 && (
        <span className="shrink-0 text-[0.7143rem] text-dim tabular-nums">
          {node.untrackedCount}
        </span>
      )}
      {node.unmergedCount > 0 && (
        <span className="shrink-0 text-[0.7143rem] text-warn tabular-nums">
          {node.unmergedCount}
        </span>
      )}
      {(node.additions > 0 || node.deletions > 0) && (
        <span className="shrink-0 text-[0.7143rem] tabular-nums">
          <span className="text-ok">+{node.additions}</span>{' '}
          <span className="text-err">−{node.deletions}</span>
        </span>
      )}
    </div>
  );
}

function CommitBar({
  value,
  disabled,
  onChange,
  onCommit,
}: {
  value: string;
  disabled: boolean;
  onChange: (v: string) => void;
  onCommit: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="flex shrink-0 items-center gap-2 border-t border-edge px-3 py-2">
      <input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && !e.shiftKey && !disabled && value.trim()) {
            e.preventDefault();
            onCommit();
          }
        }}
        placeholder={t('git.commitPlaceholder')}
        className="min-w-0 flex-1 rounded-lg border border-edge bg-panel2/50 px-2.5 py-1.5 text-xs text-fg placeholder:text-dim focus:border-accent/60 focus:outline-none"
      />
      <button
        onClick={onCommit}
        disabled={disabled}
        className="shrink-0 rounded-lg bg-accent px-3 py-1.5 text-xs font-medium text-white hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40"
      >
        {t('git.commit')}
      </button>
    </div>
  );
}

function DiffModal({
  entry,
  detail,
  files,
  index,
  total,
  side,
  dual,
  onSide,
  onPrev,
  onNext,
  onClose,
}: {
  entry: GitChange;
  detail: {
    diff: GitDiff | null;
    label: string;
    message: string;
    error?: string;
  } | null;
  files: ReturnType<typeof parseUnifiedDiff>;
  index: number;
  total: number;
  side: DiffSide;
  dual: boolean;
  onSide: (side: DiffSide) => void;
  onPrev: () => void;
  onNext: () => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div
      role="dialog"
      aria-modal="true"
      className="fixed inset-0 z-40 flex items-center justify-center bg-black/60 p-6"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="flex max-h-[85vh] w-[min(94vw,960px)] flex-col overflow-hidden rounded-xl border border-edge bg-panel shadow-2xl">
        <div className="flex h-11 shrink-0 items-center gap-2 border-b border-edge bg-panel2/40 px-3">
          <span className="min-w-0 flex-1 truncate font-mono text-xs text-fg">
            {entry.path}
          </span>
          {dual ? (
            <span
              role="group"
              aria-label={t('git.diffSide')}
              className="flex shrink-0 items-center gap-0.5 rounded-lg border border-edge p-0.5"
            >
              <button
                type="button"
                aria-pressed={side === 'staged'}
                onClick={() => onSide('staged')}
                className={`rounded px-1.5 py-0.5 text-[0.7143rem] transition-colors ${
                  side === 'staged'
                    ? 'bg-accent/15 text-accent'
                    : 'text-dim hover:bg-panel2 hover:text-fg'
                }`}
              >
                {t('git.staged')}
              </button>
              <button
                type="button"
                aria-pressed={side === 'worktree'}
                onClick={() => onSide('worktree')}
                className={`rounded px-1.5 py-0.5 text-[0.7143rem] transition-colors ${
                  side === 'worktree'
                    ? 'bg-accent/15 text-accent'
                    : 'text-dim hover:bg-panel2 hover:text-fg'
                }`}
              >
                {t('git.workingTree')}
              </button>
            </span>
          ) : detail?.label ? (
            <span className="shrink-0 text-[0.7143rem] text-dim">
              {t(`git.${detail.label}`)}
            </span>
          ) : null}
          {!entry.is_binary &&
            !entry.untracked &&
            (entry.additions > 0 || entry.deletions > 0) && (
              <span className="shrink-0 text-[0.7143rem] tabular-nums">
                <span className="text-ok">+{entry.additions}</span>{' '}
                <span className="text-err">−{entry.deletions}</span>
              </span>
            )}
          <span className="shrink-0 text-[0.7143rem] text-dim tabular-nums">
            {index + 1}/{total}
          </span>
          <button
            onClick={onPrev}
            disabled={index <= 0}
            className="grid h-7 w-7 place-items-center rounded-lg text-dim hover:bg-panel2 hover:text-fg disabled:cursor-not-allowed disabled:opacity-40"
            title={t('git.previousChange')}
            aria-label={t('git.previousChange')}
          >
            <ArrowUp size="0.9286rem" />
          </button>
          <button
            onClick={onNext}
            disabled={index < 0 || index >= total - 1}
            className="grid h-7 w-7 place-items-center rounded-lg text-dim hover:bg-panel2 hover:text-fg disabled:cursor-not-allowed disabled:opacity-40"
            title={t('git.nextChange')}
            aria-label={t('git.nextChange')}
          >
            <ArrowDown size="0.9286rem" />
          </button>
          <button
            onClick={onClose}
            className="grid h-7 w-7 place-items-center rounded-lg text-dim hover:bg-panel2 hover:text-fg"
            aria-label={t('chat.dismiss')}
          >
            <X size="0.9286rem" />
          </button>
        </div>
        <div className="min-h-0 flex-1 overflow-auto bg-panel/60 p-3">
          {!detail ? (
            <div className="grid h-full place-items-center text-dim">
              <Loader2 size="1.1429rem" className="animate-spin" />
            </div>
          ) : detail.error ? (
            <div className="break-words p-3 text-xs text-err">
              {detail.error}
            </div>
          ) : detail.message ? (
            <div className="p-3 text-xs text-dim">
              {t(`git.${detail.message}`)}
            </div>
          ) : files ? (
            <GitDiffView files={files} collapsible={false} maxHeight="h-full" />
          ) : detail.diff?.content ? (
            <pre className="whitespace-pre-wrap break-all px-3 py-2 font-mono text-xs text-fg">
              {detail.diff.content}
            </pre>
          ) : (
            <div className="p-3 text-xs text-dim">{t('git.noDiffHint')}</div>
          )}
          {detail?.diff?.truncated && (
            <div className="px-3 pb-2 text-[0.7143rem] text-dim">
              {t('git.truncatedDiff')}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function HistoryView({
  entries,
  onPick,
}: {
  entries: GitLogEntry[];
  onPick: (entry: GitLogEntry) => void;
}) {
  const { t } = useTranslation();
  if (entries.length === 0) {
    return (
      <div className="grid h-full place-items-center text-xs text-dim">
        {t('git.historyEmpty')}
      </div>
    );
  }
  return (
    <div className="h-full overflow-y-auto pb-2">
      {entries.map((e) => (
        <button
          key={e.oid}
          type="button"
          onClick={() => onPick(e)}
          title={e.subject}
          className="flex w-full items-start gap-2 border-b border-edge/60 px-3 py-2 text-left hover:bg-panel2/60"
        >
          <GitCommitHorizontal
            size="0.9286rem"
            className="mt-0.5 shrink-0 text-accent"
          />
          <div className="min-w-0 flex-1">
            <div className="truncate text-xs text-fg">{e.subject}</div>
            <div className="mt-0.5 truncate text-[0.7143rem] text-dim">
              <span className="font-mono">{e.short_oid}</span> · {e.author} ·{' '}
              {dateLabel(e.date)}
            </div>
          </div>
        </button>
      ))}
    </div>
  );
}

function ConfirmDialog({
  spec,
  onCancel,
}: {
  spec: ConfirmSpec;
  onCancel: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="fixed inset-0 z-40 grid place-items-center bg-black/60 p-4">
      <div
        role="alertdialog"
        aria-modal="true"
        className="w-96 max-w-full rounded-xl border border-edge bg-panel p-4 shadow-2xl"
      >
        <h2 className="text-sm font-semibold text-fg">{spec.title}</h2>
        <p className="mt-1 break-words font-mono text-xs text-dim">
          {spec.body}
        </p>
        <div className="mt-4 flex justify-end gap-2">
          <button
            onClick={onCancel}
            className="rounded-lg border border-edge px-3 py-1.5 text-xs text-dim hover:text-fg"
          >
            {t('interact.cancel')}
          </button>
          <button
            onClick={() => void spec.action()}
            className={`rounded-lg px-3 py-1.5 text-xs font-medium text-white ${
              spec.danger
                ? 'bg-err hover:opacity-90'
                : 'bg-accent hover:opacity-90'
            }`}
          >
            {spec.confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}

function ForcePushDialog({
  branch,
  value,
  onChange,
  onCancel,
  onConfirm,
}: {
  branch: string;
  value: string;
  onChange: (v: string) => void;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const { t } = useTranslation();
  const ready = value.trim() === branch && branch !== '';
  return (
    <div className="fixed inset-0 z-40 grid place-items-center bg-black/60 p-4">
      <div
        role="alertdialog"
        aria-modal="true"
        className="w-96 max-w-full rounded-xl border border-edge bg-panel p-4 shadow-2xl"
      >
        <h2 className="text-sm font-semibold text-err">{t('git.forcePush')}</h2>
        <p className="mt-1 text-xs leading-relaxed text-dim">
          {t('git.forcePushBody')}
        </p>
        <input
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={branch}
          className="mt-3 w-full rounded-lg border border-edge bg-panel2/50 px-2.5 py-1.5 font-mono text-xs text-fg focus:border-err/60 focus:outline-none"
        />
        <div className="mt-4 flex justify-end gap-2">
          <button
            onClick={onCancel}
            className="rounded-lg border border-edge px-3 py-1.5 text-xs text-dim hover:text-fg"
          >
            {t('interact.cancel')}
          </button>
          <button
            onClick={onConfirm}
            disabled={!ready}
            className="rounded-lg bg-err px-3 py-1.5 text-xs font-medium text-white hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40"
          >
            {t('git.forcePush')}
          </button>
        </div>
      </div>
    </div>
  );
}

function NewBranchDialog({
  value,
  onChange,
  onCancel,
  onConfirm,
}: {
  value: string;
  onChange: (v: string) => void;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="fixed inset-0 z-40 grid place-items-center bg-black/60 p-4">
      <div
        role="alertdialog"
        aria-modal="true"
        className="w-96 max-w-full rounded-xl border border-edge bg-panel p-4 shadow-2xl"
      >
        <h2 className="text-sm font-semibold text-fg">{t('git.newBranch')}</h2>
        <input
          value={value}
          onChange={(e) => onChange(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && value.trim()) {
              e.preventDefault();
              onConfirm();
            }
          }}
          placeholder={t('git.newBranchPlaceholder')}
          className="mt-3 w-full rounded-lg border border-edge bg-panel2/50 px-2.5 py-1.5 font-mono text-xs text-fg focus:border-accent/60 focus:outline-none"
        />
        <div className="mt-4 flex justify-end gap-2">
          <button
            onClick={onCancel}
            className="rounded-lg border border-edge px-3 py-1.5 text-xs text-dim hover:text-fg"
          >
            {t('interact.cancel')}
          </button>
          <button
            onClick={onConfirm}
            disabled={!value.trim()}
            className="rounded-lg bg-accent px-3 py-1.5 text-xs font-medium text-white hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40"
          >
            {t('git.createBranch')}
          </button>
        </div>
      </div>
    </div>
  );
}
