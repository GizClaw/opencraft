import { Route } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api } from '../lib/api';
import type { PathEnvironment, PathSegment } from '../lib/types';

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
  const [env, setEnv] = useState<PathEnvironment | null>(null);
  const [draft, setDraft] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const apply = (next: PathEnvironment) => {
    setEnv(next);
    setDraft(next.prepend.join('\n'));
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

  const sourceLabel = (segment: PathSegment) => {
    switch (segment.source) {
      case 'prepend':
        return t('config.diagPathSourcePrepend');
      case 'candidate':
        return t('config.diagPathSourceCandidate');
      default:
        return t('config.diagPathSourceInherited');
    }
  };

  if (!env) {
    return error ? (
      <p className="text-xs text-err break-words">{error}</p>
    ) : null;
  }

  const dirs = draft
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line !== '');

  return (
    <div className="rounded-xl border border-edge bg-panel2 p-3">
      <div className="flex items-center gap-2 text-sm font-medium">
        <Route size="1.0000rem" className="text-accent" />
        {t('config.diagPathTitle')}
      </div>
      <p className="mt-1 text-xs text-dim">{t('config.diagPathHint')}</p>

      <div className="mt-2 space-y-1">
        {env.segments.map((segment) => (
          <div
            key={`${segment.source}:${segment.dir}`}
            className="flex items-baseline justify-between gap-3"
          >
            <span
              className={`break-all font-mono text-[0.7143rem] ${
                segment.present ? 'text-fg' : 'text-warn'
              }`}
            >
              {segment.dir}
              {!segment.present && ` · ${t('config.diagPathAbsent')}`}
            </span>
            <span className="shrink-0 text-[0.7143rem] text-dim">
              {sourceLabel(segment)}
            </span>
          </div>
        ))}
      </div>

      {env.rejected.length > 0 && (
        <p className="mt-2 text-[0.7857rem] text-warn break-words">
          {t('config.diagPathRejected', { dirs: env.rejected.join(', ') })}
        </p>
      )}
      {env.missing.length > 0 && (
        <p className="mt-1 text-[0.7857rem] text-dim break-words">
          {t('config.diagPathMissing', { dirs: env.missing.join(', ') })}
        </p>
      )}

      <p className="mt-2 text-[0.7857rem] text-dim">
        {env.reloaded
          ? t('config.diagPathReloaded')
          : t('config.diagPathReloadSkipped')}
      </p>
      <p className="mt-1 text-[0.7857rem] text-dim/80">
        {t('config.diagPathDeployNote')}
      </p>

      <label className="mt-3 block text-xs text-dim">
        {t('config.diagPathPrependLabel')}
      </label>
      <textarea
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        spellCheck={false}
        rows={3}
        placeholder={t('config.diagPathPrependPlaceholder')}
        className="mt-1 w-full resize-y rounded-lg border border-edge bg-panel px-2 py-1.5 font-mono text-[0.7857rem] text-fg outline-none focus:border-accent"
      />
      <div className="mt-2 flex gap-2">
        <button
          onClick={() => void run(() => api.setPathPrepend(dirs))}
          disabled={busy}
          className="rounded-lg bg-accent px-3 py-1.5 text-sm text-white hover:opacity-90 disabled:opacity-40"
        >
          {t('config.diagPathSave')}
        </button>
        <button
          onClick={() => void run(() => api.resolvePath())}
          disabled={busy}
          className="rounded-lg border border-edge px-3 py-1.5 text-sm text-dim hover:bg-panel hover:text-fg disabled:opacity-40"
        >
          {t('config.diagPathReresolve')}
        </button>
      </div>
      {error && (
        <p className="mt-1.5 text-[0.7857rem] text-err break-words">{error}</p>
      )}
    </div>
  );
}
