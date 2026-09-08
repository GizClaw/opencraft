// PRView lists the open GitHub pull requests of the repository shown
// in the Git rail and opens the full PR detail modal. The component
// only mounts when the backend reported a GitHub provider, so a
// missing gh CLI or a non-GitHub remote never shows this surface.
import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { AlertTriangle, GitPullRequest, Loader2 } from 'lucide-react';
import { api } from '../lib/api';
import { dateLabel } from '../lib/format';
import type { GitHubPull } from '../lib/types';
import { PRDetailModal } from './PRDetailModal';
import { AvatarBadge } from './viewer/AvatarBadge';

export function PRView({ nonce }: { nonce: number }) {
  const { t } = useTranslation();
  const [rows, setRows] = useState<GitHubPull[] | null>(null);
  const [error, setError] = useState('');
  const [selected, setSelected] = useState<GitHubPull | null>(null);

  const load = useCallback(async () => {
    setError('');
    try {
      const list = await api.gitHubPRList();
      setRows(list);
    } catch (err) {
      setError(String(err));
      setRows([]);
    }
  }, []);

  // Reload on mount and whenever the GitPanel refresh nonce changes
  // (manual refresh, window focus or a git_changed event).
  useEffect(() => {
    void load();
  }, [load, nonce]);

  return (
    <div className="flex h-full min-h-0 flex-col">
      {error ? (
        <div className="flex items-center gap-2 border-b border-err/20 bg-err/5 px-3 py-2 text-xs text-err">
          <AlertTriangle size="0.8571rem" className="shrink-0" />
          <span className="min-w-0 flex-1 break-words">{error}</span>
          <button
            onClick={() => void load()}
            className="shrink-0 rounded-md px-2 py-1 text-err hover:bg-err/10"
          >
            {t('git.retry')}
          </button>
        </div>
      ) : null}

      <div className="min-h-0 flex-1 overflow-y-auto pb-2">
        {rows === null ? (
          <div className="grid h-full place-items-center text-dim">
            <Loader2 size="1.1429rem" className="animate-spin" />
          </div>
        ) : rows.length === 0 ? (
          <div className="grid h-full place-items-center px-4 text-center text-xs text-dim">
            {t('git.prEmpty')}
          </div>
        ) : (
          rows.map((pr) => (
            <button
              key={pr.number}
              type="button"
              onClick={() => setSelected(pr)}
              className="flex w-full items-start gap-2.5 border-b border-edge/60 px-3 py-2 text-left hover:bg-panel2/60"
            >
              <GitPullRequest
                size="0.9286rem"
                className={`mt-0.5 shrink-0 ${
                  pr.state === 'open'
                    ? 'text-ok'
                    : pr.state === 'merged'
                      ? 'text-accent'
                      : 'text-dim'
                }`}
              />
              <span className="min-w-0 flex-1">
                <span className="flex items-center gap-1.5">
                  <span className="min-w-0 truncate text-xs font-medium text-fg">
                    {pr.title}
                  </span>
                  <span className="shrink-0 text-[0.7143rem] text-dim">
                    #{pr.number}
                  </span>
                  {pr.draft && (
                    <span className="shrink-0 rounded border border-dim/30 px-1 py-px text-[0.6429rem] text-dim">
                      {t('git.draft')}
                    </span>
                  )}
                </span>
                <span className="mt-0.5 flex items-center gap-1.5 text-[0.7143rem] text-dim">
                  <AvatarBadge login={pr.author.login} />
                  <span className="truncate">
                    {pr.author.login} · {pr.base} ← {pr.head}
                  </span>
                  <span className="shrink-0">· {dateLabel(pr.updated_at)}</span>
                </span>
              </span>
            </button>
          ))
        )}
      </div>

      {selected && (
        <PRDetailModal pr={selected} onClose={() => setSelected(null)} />
      )}
    </div>
  );
}
