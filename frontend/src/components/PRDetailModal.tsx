// PRDetailModal is the full GitHub pull-request page shown as a modal
// from the Git rail's PR view: header, CI checks, description, commits,
// inline review threads (with the code snippet each comment refers to)
// and the conversation timeline. Everything is read-only; "Open in
// GitHub" hands the thread to the system browser.
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  AlertTriangle,
  CircleCheck,
  CircleDot,
  ExternalLink,
  GitCommitHorizontal,
  Loader2,
  X,
} from 'lucide-react';
import { api } from '../lib/api';
import { parseUnifiedDiff } from '../lib/diff';
import { dateLabel } from '../lib/format';
import type {
  GitHubCheck,
  GitHubComment,
  GitHubPRDetail,
  GitHubPull,
  GitHubReviewThread,
  GitHubTimelineItem,
  PatchFileDTO,
  PatchLineDTO,
} from '../lib/types';
import { Markdown } from './Markdown';
import { AvatarBadge } from './viewer/AvatarBadge';

export function PRDetailModal({
  pr,
  onClose,
}: {
  pr: GitHubPull;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [detail, setDetail] = useState<GitHubPRDetail | null>(null);
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let alive = true;
    setError('');
    setDetail(null);
    void api
      .gitHubPRDetail(pr.number)
      .then((d) => {
        if (alive) setDetail(d);
      })
      .catch((err: unknown) => {
        if (alive) setError(String(err));
      });
    return () => {
      alive = false;
    };
  }, [pr.number, attempt]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        onClose();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const openURL = detail?.html_url || pr.html_url;

  return (
    <div
      role="dialog"
      aria-modal="true"
      className="fixed inset-0 z-40 flex items-center justify-center bg-black/60 p-4"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="flex max-h-[92vh] w-[min(96vw,1080px)] flex-col overflow-hidden rounded-xl border border-edge bg-panel shadow-2xl">
        <div className="flex h-12 shrink-0 items-center gap-2 border-b border-edge bg-panel2/40 px-3">
          <StateChip state={pr.state} />
          <span className="shrink-0 font-mono text-[0.7143rem] text-dim">
            #{pr.number}
          </span>
          {pr.draft && (
            <span className="shrink-0 rounded border border-dim/30 px-1 py-px text-[0.6429rem] text-dim">
              {t('git.draft')}
            </span>
          )}
          <span className="min-w-0 flex-1 truncate text-xs font-medium text-fg">
            {pr.title}
          </span>
          <button
            onClick={() => void api.openExternal(openURL)}
            className="flex shrink-0 items-center gap-1.5 rounded-lg px-2 py-1.5 text-[0.7143rem] text-accent hover:bg-accent/10"
          >
            <ExternalLink size="0.7857rem" />
            {t('git.openInGithub')}
          </button>
          <button
            onClick={onClose}
            className="grid h-7 w-7 place-items-center rounded-lg text-dim hover:bg-panel2 hover:text-fg"
            aria-label={t('chat.dismiss')}
          >
            <X size="0.9286rem" />
          </button>
        </div>

        <div className="min-h-0 flex-1 overflow-y-auto bg-panel/40">
          {!detail && !error ? (
            <div className="grid h-full place-items-center text-dim">
              <Loader2 size="1.1429rem" className="animate-spin" />
            </div>
          ) : error ? (
            <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center">
              <AlertTriangle size="1.1429rem" className="text-err" />
              <div className="break-words text-xs text-err">{error}</div>
              <button
                onClick={() => setAttempt((n) => n + 1)}
                className="rounded-lg border border-edge px-3 py-1.5 text-xs text-fg hover:bg-panel2"
              >
                {t('git.retry')}
              </button>
            </div>
          ) : detail ? (
            <DetailBody detail={detail} />
          ) : null}
        </div>
      </div>
    </div>
  );
}

function DetailBody({ detail }: { detail: GitHubPRDetail }) {
  const { t } = useTranslation();
  return (
    <div className="pb-4">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-edge px-4 py-2 text-[0.7143rem] text-dim">
        <span className="flex items-center gap-1.5">
          <AvatarBadge login={detail.author.login} />
          {detail.author.login}
        </span>
        <span>
          {t('git.prOpened')} · {dateLabel(detail.created_at)}
        </span>
        <span className="font-mono">
          {detail.base} ← {detail.head}
        </span>
        <span className="ml-auto tabular-nums">
          <span className="text-ok">+{detail.additions}</span>{' '}
          <span className="text-err">−{detail.deletions}</span> ·{' '}
          {detail.changed_files}{' '}
          {detail.changed_files === 1 ? t('git.prFile') : t('git.prFiles')}
        </span>
      </div>

      {detail.checks.length > 0 ? (
        <div className="border-b border-edge px-4 py-2">
          <SectionTitle label={t('git.checks')} count={detail.checks.length} />
          <div className="mt-1.5 flex flex-wrap gap-1.5">
            {detail.checks.map((check, i) => (
              <CheckChip key={`${check.kind}-${i}`} check={check} />
            ))}
          </div>
        </div>
      ) : null}

      <div className="border-b border-edge px-4 py-3">
        {detail.body ? (
          <Markdown text={detail.body} />
        ) : (
          <span className="text-xs text-dim">{t('git.prNoBody')}</span>
        )}
      </div>

      {detail.threads.length > 0 && (
        <div className="border-b border-edge px-4 py-3">
          <SectionTitle
            label={t('git.reviewThreads')}
            count={detail.threads.length}
          />
          <div className="mt-2 overflow-hidden rounded-lg border border-edge">
            {detail.threads.map((thread) => (
              <ReviewThreadBlock
                key={`${thread.path}:${thread.comments[0]?.id ?? thread.line}`}
                thread={thread}
              />
            ))}
          </div>
        </div>
      )}

      <div className="border-b border-edge px-4 py-3">
        <SectionTitle
          label={t('git.commits')}
          count={detail.commits_count || detail.commits.length}
        />
        {detail.commits.length === 0 ? (
          <p className="mt-2 text-xs text-dim">{t('git.prNoCommits')}</p>
        ) : (
          <div className="mt-2">
            {detail.commits.map((c) => (
              <div
                key={c.sha}
                className="flex items-start gap-2 border-b border-edge/60 py-1.5 last:border-b-0"
              >
                <GitCommitHorizontal
                  size="0.8571rem"
                  className="mt-0.5 shrink-0 text-accent"
                />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-xs text-fg">
                    {c.message.split('\n')[0]}
                  </span>
                  <span className="text-[0.7143rem] text-dim">
                    <span className="font-mono">{c.short_sha}</span> ·{' '}
                    {c.author} · {dateLabel(c.date)}
                  </span>
                </span>
              </div>
            ))}
          </div>
        )}
      </div>

      <div className="px-4 py-3">
        <SectionTitle
          label={t('git.conversation')}
          count={detail.conversation.length}
        />
        {detail.conversation.length === 0 ? (
          <p className="mt-2 text-xs text-dim">{t('git.prNoComments')}</p>
        ) : (
          <div className="mt-2">
            {detail.conversation.map((item) => (
              <TimelineEntry key={item.id} item={item} />
            ))}
          </div>
        )}
      </div>

      {detail.truncated && (
        <p className="px-4 pt-1 text-[0.7143rem] text-dim">
          {t('git.prTruncated')}
        </p>
      )}
    </div>
  );
}

function SectionTitle({ label, count }: { label: string; count: number }) {
  return (
    <h3 className="text-xs font-medium uppercase tracking-wide text-dim">
      {label}
      <span className="ml-1.5 tabular-nums">· {count}</span>
    </h3>
  );
}

function StateChip({ state }: { state: GitHubPRDetail['state'] }) {
  const { t } = useTranslation();
  const meta: Record<GitHubPRDetail['state'], { label: string; cls: string }> =
    {
      open: {
        label: t('git.prStateOpen'),
        cls: 'border-ok/40 bg-ok/15 text-ok',
      },
      merged: {
        label: t('git.prStateMerged'),
        cls: 'border-accent/40 bg-accent/15 text-accent',
      },
      closed: {
        label: t('git.prStateClosed'),
        cls: 'border-dim/30 bg-dim/10 text-dim',
      },
    };
  const m = meta[state];
  return (
    <span
      className={`shrink-0 rounded border px-1.5 py-px text-[0.6429rem] font-semibold ${m.cls}`}
    >
      {m.label}
    </span>
  );
}

const CHECK_CLS: Record<string, string> = {
  success: 'text-ok border-ok/30',
  failure: 'text-err border-err/40',
  error: 'text-err border-err/40',
  timed_out: 'text-err border-err/40',
  pending: 'text-warn border-warn/40',
  action_required: 'text-warn border-warn/40',
  cancelled: 'text-dim border-edge',
  skipped: 'text-dim border-edge',
  neutral: 'text-dim border-edge',
  stale: 'text-dim border-edge',
};

function checkStateLabel(state: string, t: (k: string) => string): string {
  const key = `git.check_${state}`;
  const known = [
    'success',
    'failure',
    'pending',
    'error',
    'cancelled',
    'skipped',
    'neutral',
    'action_required',
    'timed_out',
    'stale',
  ];
  return known.includes(state) ? t(key) : state;
}

function CheckChip({ check }: { check: GitHubCheck }) {
  const { t } = useTranslation();
  const cls = CHECK_CLS[check.state] ?? 'text-dim border-edge';
  const Icon =
    check.state === 'success'
      ? CircleCheck
      : check.state === 'pending'
        ? Loader2
        : check.state === 'failure' ||
            check.state === 'error' ||
            check.state === 'timed_out'
          ? AlertTriangle
          : CircleDot;
  const title = check.description
    ? `${checkStateLabel(check.state, t)} — ${check.description}`
    : checkStateLabel(check.state, t);
  return (
    <button
      type="button"
      title={title}
      onClick={() => check.url && void api.openExternal(check.url)}
      className={`flex items-center gap-1.5 rounded border bg-panel2/40 px-1.5 py-0.5 text-[0.7143rem] ${cls} ${
        check.url ? 'hover:bg-panel2' : 'cursor-default'
      }`}
    >
      <Icon
        size="0.7857rem"
        className={check.state === 'pending' ? 'animate-spin' : ''}
      />
      <span className="max-w-56 truncate">{check.name}</span>
    </button>
  );
}

// ---- inline review threads ----

function ReviewThreadBlock({ thread }: { thread: GitHubReviewThread }) {
  const { t } = useTranslation();
  const anchor = anchorLabel(thread);
  const first = thread.comments[0];
  return (
    <div className="border-b border-edge last:border-b-0">
      <div className="flex items-center gap-2 bg-panel2/40 px-2.5 py-1.5">
        <span className="min-w-0 truncate font-mono text-[0.7143rem] text-fg">
          {thread.path}:{anchor}
        </span>
        {thread.side === 'LEFT' && (
          <span className="shrink-0 rounded bg-dim/10 px-1 text-[0.6429rem] text-dim">
            {t('git.sideOld')}
          </span>
        )}
        <span className="flex-1" />
        {first?.html_url && (
          <button
            onClick={() => void api.openExternal(first.html_url)}
            className="shrink-0 text-dim hover:text-accent"
            title={t('git.openInGithub')}
            aria-label={t('git.openInGithub')}
          >
            <ExternalLink size="0.7857rem" />
          </button>
        )}
      </div>
      <div className="px-2.5 pb-2">
        <ReviewSnippet thread={thread} />
        {thread.comments.map((c, i) => (
          <CommentBlock key={c.id} comment={c} indented={i > 0} />
        ))}
      </div>
    </div>
  );
}

function anchorLabel(thread: GitHubReviewThread): string {
  if ((thread.side ?? 'RIGHT') === 'LEFT') {
    const lo =
      thread.original_start_line || thread.original_line || thread.line;
    const hi = thread.original_line || thread.line;
    return lo && lo !== hi ? `L${lo}-${hi}` : `L${hi}`;
  }
  const lo = thread.start_line || thread.line || thread.original_line;
  const hi = thread.line || thread.original_line;
  return lo && lo !== hi ? `L${lo}-${hi}` : `L${hi || lo}`;
}

function parseThreadFiles(thread: GitHubReviewThread): PatchFileDTO[] | null {
  if (!thread.diff_hunk) return null;
  const header =
    `diff --git a/${thread.path} b/${thread.path}\n` +
    `--- a/${thread.path}\n+++ b/${thread.path}\n`;
  return parseUnifiedDiff(header + thread.diff_hunk + '\n');
}

function anchorRange(thread: GitHubReviewThread): [number, number] | null {
  const isLeft = (thread.side ?? 'RIGHT') === 'LEFT';
  let lo: number;
  let hi: number;
  if (isLeft) {
    hi = thread.original_line || thread.line;
    lo = thread.original_start_line || hi;
  } else {
    hi = thread.line || thread.original_line;
    lo = thread.start_line || hi;
  }
  if (!hi) return null;
  return [lo || hi, hi];
}

function inAnchorRange(
  line: PatchLineDTO,
  isLeft: boolean,
  range: [number, number] | null,
): boolean {
  if (!range) return false;
  const num = isLeft ? line.old_num : line.new_num;
  return num !== undefined && num >= range[0] && num <= range[1];
}

function ReviewSnippet({ thread }: { thread: GitHubReviewThread }) {
  const files = parseThreadFiles(thread);
  if (!files || files.length === 0) {
    return (
      <pre className="mt-2 max-h-56 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-panel2/40 px-2.5 py-2 font-mono text-[0.7143rem] text-dim">
        {thread.diff_hunk}
      </pre>
    );
  }
  const isLeft = (thread.side ?? 'RIGHT') === 'LEFT';
  const range = anchorRange(thread);
  return (
    <div className="mt-2 max-h-72 overflow-auto rounded-lg border border-edge bg-panel/60">
      {files
        .flatMap((file) => file.lines)
        .map((line, i) => (
          <SnippetRow
            key={i}
            line={line}
            isLeft={isLeft}
            anchor={inAnchorRange(line, isLeft, range)}
          />
        ))}
    </div>
  );
}

function SnippetRow({
  line,
  isLeft,
  anchor,
}: {
  line: PatchLineDTO;
  isLeft: boolean;
  anchor: boolean;
}) {
  const num = isLeft ? line.old_num : line.new_num;
  const marker = line.kind === 'add' ? '+' : line.kind === 'delete' ? '-' : ' ';
  const kindBg =
    line.kind === 'add' ? 'bg-ok/5' : line.kind === 'delete' ? 'bg-err/5' : '';
  return (
    <div
      className={`grid grid-cols-[3rem_minmax(0,1fr)] font-mono text-[0.7143rem] leading-5 ${
        anchor ? 'bg-warn/15' : kindBg
      }`}
    >
      <span className="select-none border-r border-edge/50 px-1.5 text-right text-dim tabular-nums">
        {num ?? ''}
      </span>
      <span
        className={`whitespace-pre px-1.5 ${anchor ? 'text-fg' : 'text-fg/80'}`}
      >
        <span className="select-none text-dim">{marker}</span>
        {line.text}
      </span>
    </div>
  );
}

function CommentBlock({
  comment,
  indented,
}: {
  comment: GitHubComment;
  indented?: boolean;
}) {
  return (
    <div className={`mt-2 flex gap-2 ${indented ? 'ml-5' : ''}`}>
      <AvatarBadge login={comment.author.login} />
      <div className="min-w-0 flex-1">
        <p className="text-[0.7143rem] text-dim">
          <span className="font-medium text-fg">{comment.author.login}</span>
          {' · '}
          {dateLabel(comment.created_at)}
        </p>
        {comment.body ? (
          <div className="prose-chat mt-1 text-xs [&_p]:my-1">
            <Markdown text={comment.body} />
          </div>
        ) : null}
      </div>
    </div>
  );
}

function TimelineEntry({ item }: { item: GitHubTimelineItem }) {
  const { t } = useTranslation();
  const action =
    item.kind === 'review' ? reviewActionLabel(item, t) : t('git.commented');
  const actionCls =
    item.action === 'APPROVED'
      ? 'text-ok'
      : item.action === 'CHANGES_REQUESTED'
        ? 'text-err'
        : 'text-dim';
  return (
    <div className="flex gap-2 border-b border-edge/60 py-2 last:border-b-0">
      <AvatarBadge login={item.author.login} size="md" />
      <div className="min-w-0 flex-1">
        <p className="text-[0.7143rem] text-dim">
          <span className="font-medium text-fg">{item.author.login}</span>{' '}
          <span className={actionCls}>{action}</span>
          {' · '}
          {dateLabel(item.created_at)}
        </p>
        {item.body ? (
          <div className="prose-chat mt-1 text-xs [&_p]:my-1">
            <Markdown text={item.body} />
          </div>
        ) : null}
      </div>
    </div>
  );
}

function reviewActionLabel(
  item: GitHubTimelineItem,
  t: (key: string) => string,
): string {
  switch (item.action) {
    case 'APPROVED':
      return t('git.reviewApproved');
    case 'CHANGES_REQUESTED':
      return t('git.reviewChanges');
    case 'COMMENTED':
      return t('git.reviewCommented');
    default:
      return t('git.reviewed');
  }
}
