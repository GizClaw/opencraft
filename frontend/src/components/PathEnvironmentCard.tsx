import { AlertTriangle, Copy, Info, Route } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api } from '../lib/api';
import { useStore } from '../lib/store';
import type { PathEnvironment, PathSegment } from '../lib/types';
import { ICON } from './ui/icon';
import { Textarea } from './ui/Textarea';

// PathEnvironmentCard is the diagnostics view of the PATH the app runs
// with. The desktop app is usually started from Finder/Dock, which
// inherits launchd's minimal PATH rather than the shell's, so a CLI
// installed by Homebrew, npm-global or any user-local prefix "exists in
// the terminal but not in the app" — for MCP servers, for the commands an
// agent runs, and for the app's own gh/git lookups. The card shows where
// every entry came from and lets the user prepend a directory the host
// cannot guess (a version manager's active path, a custom prefix).
export function PathEnvironmentCard() {
  const { t } = useTranslation();
  const toast = useStore((s) => s.toast);
  const [env, setEnv] = useState<PathEnvironment | null>(null);
  const [draft, setDraft] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const apply = (next: PathEnvironment) => {
    setEnv(next);
    setDraft((next.prepend ?? []).join('\n'));
  };

  useEffect(() => {
    void (async () => {
      try {
        apply(await api.pathEnvironment());
      } catch (err) {
        setError(String(err));
      }
    })();
  }, []);

  // Both actions persist-and-apply, so they share one path through the
  // busy/error bookkeeping.
  const run = async (action: () => Promise<PathEnvironment>) => {
    setBusy(true);
    try {
      apply(await action());
      setError('');
    } catch (err) {
      setError(String(err));
    } finally {
      setBusy(false);
    }
  };

  const sourceLabel = (source: string) => {
    switch (source) {
      case 'prepend':
        return t('config.diagPathSourcePrepend');
      case 'candidate':
        return t('config.diagPathSourceCandidate');
      case 'inherited':
        return t('config.diagPathSourceInherited');
      default:
        // An unknown source is shown verbatim rather than hidden: the
        // card must not drop an entry it cannot explain.
        return source;
    }
  };

  const sourceDot = (source: string) => {
    switch (source) {
      case 'prepend':
        return 'bg-accent';
      case 'candidate':
        return 'bg-ok';
      default:
        return 'bg-dim';
    }
  };

  // A present prepend entry gets the accent tint so the directory that
  // wins precedence is visible at a glance; everything else stays neutral
  // and only an absent directory is flagged.
  const pillClass = (segment: PathSegment) => {
    if (!segment.present) return 'border-warn/40 bg-warn/10 text-warn';
    if ((segment.source ?? 'inherited') === 'prepend') {
      return 'border-accent/40 bg-accent/10 text-fg';
    }
    return 'border-edge bg-panel text-fg';
  };

  if (!env) {
    return error ? (
      <p className="text-xs text-err break-words">{error}</p>
    ) : null;
  }

  // The host sends lists, but an empty Go slice used to marshal to null and
  // took the whole page down through `.length`; a diagnostics view should
  // never crash on the state it is reporting.
  const segments = env.segments ?? [];
  const rejected = env.rejected ?? [];
  const missing = env.missing ?? [];
  // Resolution merges prepend → inherited → candidates, so grouping by
  // source keeps the PATH order while giving the provenance a shape: one
  // labelled row per origin instead of a flat list of directories.
  const groups: { source: string; segments: PathSegment[] }[] = [];
  for (const segment of segments) {
    const source = segment.source ?? 'inherited';
    const group = groups.find((candidate) => candidate.source === source);
    if (group) group.segments.push(segment);
    else groups.push({ source, segments: [segment] });
  }
  const dirs = draft
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line !== '');

  const copyPath = async () => {
    try {
      await navigator.clipboard.writeText(env.path);
      toast(t('config.diagPathCopied'));
    } catch (err) {
      toast(String(err));
    }
  };

  return (
    <div className="rounded-card border border-edge bg-panel2 p-3">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2 text-sm font-medium">
            <Route size={ICON.sm} className="shrink-0 text-accent" />
            {t('config.diagPathTitle')}
          </div>
          <p className="mt-1 text-xs text-faint">{t('config.diagPathHint')}</p>
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          <span
            data-tip={
              env.reloaded
                ? t('config.diagPathReloaded')
                : t('config.diagPathReloadSkipped')
            }
            className={`inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-micro ${
              env.reloaded ? 'border-ok/40 text-ok' : 'border-warn/40 text-warn'
            }`}
          >
            <span
              className={`h-1.5 w-1.5 rounded-full ${
                env.reloaded ? 'bg-ok' : 'bg-warn'
              }`}
            />
            {env.reloaded
              ? t('config.diagPathStateApplied')
              : t('config.diagPathStateStale')}
          </span>
          <button
            onClick={() => void copyPath()}
            data-tip={t('config.diagPathCopy')}
            aria-label={t('config.diagPathCopy')}
            className="rounded-control border border-edge p-1 text-dim transition-colors hover:bg-panel hover:text-fg"
          >
            <Copy size={ICON.xs} />
          </button>
        </div>
      </div>

      <div className="mt-2.5 space-y-2">
        {groups.map((group) => (
          <div key={group.source}>
            <div className="flex items-baseline gap-1.5 text-micro text-dim">
              <span
                className={`h-1.5 w-1.5 rounded-full ${sourceDot(group.source)}`}
              />
              <span>{sourceLabel(group.source)}</span>
              <span className="text-faint">· {group.segments.length}</span>
            </div>
            <div className="mt-1 flex flex-wrap gap-1.5">
              {group.segments.map((segment) => (
                <span
                  key={segment.dir}
                  data-tip={
                    segment.present ? undefined : t('config.diagPathAbsent')
                  }
                  className={`inline-flex max-w-full items-baseline rounded-control border px-1.5 py-0.5 font-mono text-micro break-all ${pillClass(segment)}`}
                >
                  {segment.dir}
                  {!segment.present && (
                    <span className="ml-1.5 shrink-0 text-dim">
                      {t('config.diagPathAbsent')}
                    </span>
                  )}
                </span>
              ))}
            </div>
          </div>
        ))}
      </div>

      {(rejected.length > 0 || missing.length > 0) && (
        <div className="mt-2.5 space-y-1">
          {rejected.length > 0 && (
            <p className="flex items-start gap-1.5 text-label text-warn">
              <AlertTriangle size={ICON.xs} className="mt-0.5 shrink-0" />
              <span className="break-words">
                {t('config.diagPathRejectedLabel')}{' '}
                <span className="font-mono text-micro">
                  {rejected.join(', ')}
                </span>
              </span>
            </p>
          )}
          {missing.length > 0 && (
            <p className="flex items-start gap-1.5 text-label text-dim">
              <Info size={ICON.xs} className="mt-0.5 shrink-0" />
              <span className="break-words">
                {t('config.diagPathMissingLabel')}{' '}
                <span className="font-mono text-micro">
                  {missing.join(', ')}
                </span>
              </span>
            </p>
          )}
        </div>
      )}

      <p className="mt-2.5 text-label text-faint">
        {t('config.diagPathDeployNote')}
      </p>

      <div className="mt-3 border-t border-edge pt-3">
        <label className="block text-xs text-dim">
          {t('config.diagPathPrependLabel')}
        </label>
        <Textarea
          mono
          size="sm"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          spellCheck={false}
          rows={3}
          placeholder={t('config.diagPathPrependPlaceholder')}
          className="mt-1"
        />
        <div className="mt-2 flex items-center gap-2">
          <button
            onClick={() => void run(() => api.setPathPrepend(dirs))}
            disabled={busy}
            className="rounded-control bg-accent px-3 py-1.5 text-sm text-white hover:opacity-90 disabled:opacity-40"
          >
            {t('config.diagPathSave')}
          </button>
          <button
            onClick={() => void run(() => api.resolvePath())}
            disabled={busy}
            className="rounded-control border border-edge px-3 py-1.5 text-sm text-dim hover:bg-panel hover:text-fg disabled:opacity-40"
          >
            {t('config.diagPathReresolve')}
          </button>
        </div>
        {error && (
          <p className="mt-1.5 flex items-start gap-1.5 text-label text-err">
            <AlertTriangle size={ICON.xs} className="mt-0.5 shrink-0" />
            <span className="break-words">{error}</span>
          </p>
        )}
      </div>
    </div>
  );
}
