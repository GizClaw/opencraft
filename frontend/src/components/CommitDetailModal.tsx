// CommitDetailModal shows one commit's changed files and per-file
// diffs. It is opened from the History view; merge commits compare
// against their first parent (the backend resolves that). The layout
// follows the diff modal language: centered dialog, file list on the
// left, the selected file's diff on the right, ↑/↓ navigation.
import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  AlertTriangle,
  ArrowDown,
  ArrowUp,
  GitCommitHorizontal,
  Loader2,
  X,
} from 'lucide-react';
import { api } from '../lib/api';
import { parseUnifiedDiff } from '../lib/diff';
import type {
  GitCommitFile,
  GitCommitFiles,
  GitDiff,
  GitLogEntry,
  GitChangeKind,
} from '../lib/types';
import { GitDiffView } from './viewer/DiffView';

export function CommitDetailModal({
  entry,
  onClose,
}: {
  entry: GitLogEntry;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [snapshot, setSnapshot] = useState<GitCommitFiles | null>(null);
  const [listError, setListError] = useState('');
  const [selectedPath, setSelectedPath] = useState<string | null>(null);
  const [diff, setDiff] = useState<GitDiff | null>(null);
  const [diffMessage, setDiffMessage] = useState('');
  const [diffError, setDiffError] = useState('');
  const [diffLoading, setDiffLoading] = useState(false);

  const selectFile = useCallback(
    async (file: GitCommitFile) => {
      setSelectedPath(file.path);
      setDiff(null);
      setDiffMessage('');
      setDiffError('');
      if (file.is_binary) {
        setDiffMessage('binaryNoDiff');
        return;
      }
      setDiffLoading(true);
      try {
        const d = await api.gitCommitDiff(entry.oid, file.path);
        setDiff(d);
      } catch (err) {
        setDiffError(String(err));
      } finally {
        setDiffLoading(false);
      }
    },
    [entry.oid],
  );

  const loadFiles = useCallback(async () => {
    setListError('');
    setSnapshot(null);
    setSelectedPath(null);
    setDiff(null);
    setDiffMessage('');
    try {
      const snap = await api.gitCommitFiles(entry.oid);
      setSnapshot(snap);
      if (snap.files.length > 0) void selectFile(snap.files[0]);
    } catch (err) {
      setListError(String(err));
    }
  }, [entry.oid, selectFile]);

  useEffect(() => {
    void loadFiles();
  }, [loadFiles]);

  const files = useMemo(() => snapshot?.files ?? [], [snapshot]);
  const index = selectedPath
    ? files.findIndex((f) => f.path === selectedPath)
    : -1;

  const openAt = useCallback(
    (i: number) => {
      const file = files[i];
      if (file) void selectFile(file);
    },
    [files, selectFile],
  );

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        onClose();
      } else if (e.key === 'ArrowDown') {
        e.preventDefault();
        openAt(index + 1);
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        openAt(index - 1);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [index, openAt, onClose]);

  const parsed = useMemo(
    () => (diff?.content ? parseUnifiedDiff(diff.content) : null),
    [diff],
  );

  return (
    <div
      role="dialog"
      aria-modal="true"
      className="fixed inset-0 z-40 flex items-center justify-center bg-black/60 p-4"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="flex h-[min(92vh,760px)] w-[min(96vw,1040px)] flex-col overflow-hidden rounded-xl border border-edge bg-panel shadow-2xl">
        <div className="flex h-11 shrink-0 items-center gap-2 border-b border-edge bg-panel2/40 px-3">
          <GitCommitHorizontal
            size="0.9286rem"
            className="shrink-0 text-accent"
          />
          <span className="min-w-0 flex-1 truncate text-xs font-medium text-fg">
            {entry.subject}
          </span>
          <span className="shrink-0 font-mono text-[0.7143rem] text-dim">
            {entry.short_oid}
          </span>
          {snapshot && files.length > 0 && (
            <span className="shrink-0 text-[0.7143rem] text-dim tabular-nums">
              {index + 1}/{files.length}
            </span>
          )}
          <button
            onClick={() => openAt(index - 1)}
            disabled={index <= 0}
            className="grid h-7 w-7 place-items-center rounded-lg text-dim hover:bg-panel2 hover:text-fg disabled:cursor-not-allowed disabled:opacity-40"
            title={t('git.previousChange')}
            aria-label={t('git.previousChange')}
          >
            <ArrowUp size="0.9286rem" />
          </button>
          <button
            onClick={() => openAt(index + 1)}
            disabled={index < 0 || index >= files.length - 1}
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

        <div className="flex min-h-0 flex-1">
          <div className="flex w-72 shrink-0 flex-col border-r border-edge bg-panel/60">
            <div className="flex h-8 shrink-0 items-center border-b border-edge px-3 text-[0.7143rem] uppercase tracking-wide text-dim">
              {t('git.commitFiles')}
              {files.length > 0 ? ` · ${files.length}` : ''}
            </div>
            <div className="min-h-0 flex-1 overflow-y-auto">
              {!snapshot && !listError ? (
                <div className="grid h-full place-items-center text-dim">
                  <Loader2 size="1.1429rem" className="animate-spin" />
                </div>
              ) : listError ? (
                <div className="flex flex-col items-start gap-2 p-3">
                  <span className="flex items-start gap-1.5 text-xs text-err">
                    <AlertTriangle
                      size="0.8571rem"
                      className="mt-0.5 shrink-0"
                    />
                    <span className="break-words">{listError}</span>
                  </span>
                  <button
                    onClick={() => void loadFiles()}
                    className="rounded-lg border border-edge px-2 py-1 text-xs text-fg hover:bg-panel2"
                  >
                    {t('git.retry')}
                  </button>
                </div>
              ) : files.length === 0 ? (
                <div className="grid h-full place-items-center px-3 text-center text-xs text-dim">
                  {t('git.commitNoFiles')}
                </div>
              ) : (
                files.map((file) => (
                  <CommitFileRow
                    key={file.path}
                    file={file}
                    active={file.path === selectedPath}
                    onPick={() => void selectFile(file)}
                  />
                ))
              )}
            </div>
            {snapshot?.truncated && (
              <div className="shrink-0 border-t border-edge px-3 py-1.5 text-[0.7143rem] text-dim">
                {t('git.truncatedList')}
              </div>
            )}
          </div>

          <div className="min-h-0 min-w-0 flex-1 overflow-auto bg-panel/40 p-3">
            {diffLoading ? (
              <div className="grid h-full place-items-center text-dim">
                <Loader2 size="1.1429rem" className="animate-spin" />
              </div>
            ) : diffError ? (
              <div className="break-words text-xs text-err">{diffError}</div>
            ) : diffMessage ? (
              <div className="p-3 text-xs text-dim">
                {t(`git.${diffMessage}`)}
              </div>
            ) : parsed ? (
              <GitDiffView
                files={parsed}
                collapsible={false}
                maxHeight="h-full"
              />
            ) : diff?.content ? (
              <pre className="whitespace-pre-wrap break-all px-3 py-2 font-mono text-xs text-fg">
                {diff.content}
              </pre>
            ) : selectedPath ? (
              <div className="p-3 text-xs text-dim">{t('git.noDiffHint')}</div>
            ) : null}
            {diff?.truncated && (
              <div className="px-3 pb-2 text-[0.7143rem] text-dim">
                {t('git.truncatedDiff')}
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

const KIND_MARK: Partial<Record<GitChangeKind, string>> = {
  added: 'A',
  modified: 'M',
  deleted: 'D',
  renamed: 'R',
  copied: 'C',
  typechange: 'T',
};

function kindClass(kind: GitChangeKind): string {
  switch (kind) {
    case 'added':
      return 'bg-ok/15 text-ok';
    case 'deleted':
      return 'bg-err/15 text-err';
    case 'typechange':
      return 'bg-warn/15 text-warn';
    default:
      return 'bg-accent/15 text-accent';
  }
}

function CommitFileRow({
  file,
  active,
  onPick,
}: {
  file: GitCommitFile;
  active: boolean;
  onPick: () => void;
}) {
  const { t } = useTranslation();
  const mark = KIND_MARK[file.kind] ?? '?';
  const renamed =
    file.orig_path && (file.kind === 'renamed' || file.kind === 'copied');
  return (
    <button
      type="button"
      onClick={onPick}
      className={`flex w-full items-start gap-2 border-b border-edge/60 px-2.5 py-2 text-left ${
        active ? 'bg-accent/10' : 'hover:bg-panel2/60'
      }`}
    >
      <span
        className={`mt-0.5 grid h-4 w-4 shrink-0 place-items-center rounded text-[0.6429rem] font-semibold ${kindClass(file.kind)}`}
        title={file.kind}
      >
        {mark}
      </span>
      <span className="min-w-0 flex-1">
        <span className="block truncate font-mono text-xs text-fg">
          {renamed ? `${file.orig_path} → ${file.path}` : file.path}
        </span>
        {!file.is_binary && (file.additions > 0 || file.deletions > 0) && (
          <span className="text-[0.7143rem] tabular-nums">
            <span className="text-ok">+{file.additions}</span>{' '}
            <span className="text-err">−{file.deletions}</span>
          </span>
        )}
        {file.is_binary && (
          <span className="text-[0.7143rem] text-dim">
            {t('git.binaryFile')}
          </span>
        )}
      </span>
    </button>
  );
}
