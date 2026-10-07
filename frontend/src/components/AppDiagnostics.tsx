import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ExternalLink, File as FileIcon, Folder } from 'lucide-react';
import { api } from '../lib/api';
import { useAppsStore } from '../apps/store';
import { AppIcon } from './AppGallery';
import { Badge } from './ui/Badge';
import { Button } from './ui/Button';
import { ICON } from './ui/icon';
import type { AppFile } from '../apps/types';
import type * as gen from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/models';

/**
 * AppDiagnostics is one application's diagnostic panel: the three roots
 * it owns, what its runtime is doing, and a read-only browse of the
 * private workspace the page writes into.
 *
 * It is the page a broken application sends you to. The most common
 * failure is a document layer the host refuses — the error is the only
 * copy of that refusal, and file browsing is what lets a user look at
 * what their scripts actually produced without leaving the app.
 */
export function AppDiagnostics({ appID }: { appID: string }) {
  const { t } = useTranslation();
  const status = useAppsStore((s) => s.status[appID]);
  const icon = useAppsStore((s) => s.apps.find((a) => a.id === appID)?.icon);
  const refresh = useAppsStore((s) => s.refreshStatus);
  const [dir, setDir] = useState('');
  const [files, setFiles] = useState<AppFile[]>([]);
  const [fileError, setFileError] = useState('');
  const [preview, setPreview] = useState<{ path: string; text: string } | null>(
    null,
  );

  useEffect(() => {
    void refresh(appID);
  }, [appID, refresh]);

  useEffect(() => {
    let cancelled = false;
    setFiles([]);
    setFileError('');
    void api
      .appFiles(appID, dir)
      .then((nodes) => {
        if (cancelled) return;
        setFiles(
          nodes
            .map((n) => ({
              name: n.name,
              path: n.path,
              dir: n.is_dir,
              size: n.size,
            }))
            .sort((a, b) =>
              a.dir === b.dir ? a.name.localeCompare(b.name) : a.dir ? -1 : 1,
            ),
        );
      })
      .catch((err) => {
        if (!cancelled) setFileError(String(err));
      });
    return () => {
      cancelled = true;
    };
  }, [appID, dir]);

  const openFile = async (path: string) => {
    try {
      setPreview({ path, text: await api.appReadFile(appID, path) });
    } catch (err) {
      setPreview({ path, text: String(err) });
    }
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto p-4">
      <section className="rounded-card border border-edge bg-panel2 p-3">
        <div className="flex items-center gap-2">
          <AppIcon id={appID} icon={icon} />
          <h3 className="text-sm font-semibold">{status?.name ?? appID}</h3>
          <Badge tone={status?.serving ? 'ok' : 'neutral'}>
            {status?.serving
              ? t('apps.state.running')
              : status?.retiring
                ? t('apps.state.retiring')
                : t('apps.state.idle')}
          </Badge>
          <span className="flex-1" />
          <Button
            size="sm"
            variant="secondary"
            onClick={() => void refresh(appID)}
          >
            {t('apps.diag.refresh')}
          </Button>
        </div>
        <dl className="mt-2 grid gap-1 text-xs">
          <Root
            label={t('apps.diag.contentRoot')}
            path={status?.content_root}
          />
          <Root label={t('apps.diag.stateRoot')} path={status?.state_root} />
          <Root
            label={t('apps.diag.workDir')}
            path={status?.work_dir}
            onOpen={() => void api.appReveal(appID, '')}
          />
          <div className="flex items-center gap-2">
            <dt className="w-28 shrink-0 text-faint">
              {t('apps.diag.recovery')}
            </dt>
            <dd
              className={`min-w-0 flex-1 ${recoveryTone(status?.recovery)}`}
              data-testid="app-recovery"
            >
              {recoveryLine(t, status?.recovery)}
            </dd>
          </div>
        </dl>
      </section>

      <section className="min-h-0 flex-1 rounded-card border border-edge bg-panel2 p-3">
        <div className="flex items-center gap-2">
          <h3 className="text-sm font-semibold">{t('apps.diag.files')}</h3>
          <span className="flex-1" />
          <Button
            size="sm"
            variant="ghost"
            disabled={!dir}
            onClick={() => setDir(dir.split('/').slice(0, -1).join('/'))}
          >
            {t('apps.diag.up')}
          </Button>
        </div>
        <p className="mt-1 break-all text-xs text-faint">{dir || '/'}</p>
        {fileError && <p className="mt-2 text-xs text-err">{fileError}</p>}
        {files.length === 0 && !fileError && (
          <p className="mt-2 text-xs text-faint">{t('apps.diag.emptyDir')}</p>
        )}
        <ul className="mt-2 flex flex-col">
          {files.map((file) => (
            <li key={file.path}>
              <button
                onClick={() =>
                  file.dir ? setDir(file.path) : void openFile(file.path)
                }
                className="flex w-full items-center gap-2 rounded-tight px-1.5 py-1 text-left text-xs text-dim hover:bg-panel3 hover:text-fg"
              >
                {file.dir ? (
                  <Folder size={ICON.sm} className="shrink-0 text-accent" />
                ) : (
                  <FileIcon size={ICON.sm} className="shrink-0" />
                )}
                <span className="truncate">{file.name}</span>
                <span className="flex-1" />
                {!file.dir && file.size !== undefined && (
                  <span className="text-faint">{file.size}B</span>
                )}
              </button>
            </li>
          ))}
        </ul>
        {preview && (
          <div className="mt-3">
            <div className="flex items-center gap-2">
              <span className="truncate text-xs text-dim">{preview.path}</span>
              <span className="flex-1" />
              <Button
                size="sm"
                variant="ghost"
                onClick={() => void api.appReveal(appID, preview.path)}
              >
                <ExternalLink size={ICON.xs} />
                {t('apps.chat.reveal')}
              </Button>
            </div>
            <pre className="mt-1 max-h-64 overflow-auto whitespace-pre-wrap rounded-card border border-edge bg-panel p-2 text-xs text-dim">
              {preview.text}
            </pre>
          </div>
        )}
      </section>
    </div>
  );
}

function Root({
  label,
  path,
  onOpen,
}: {
  label: string;
  path?: string;
  onOpen?: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="flex items-center gap-2">
      <dt className="w-28 shrink-0 text-faint">{label}</dt>
      <dd className="min-w-0 flex-1 truncate font-mono text-fg" data-tip={path}>
        {path || '…'}
      </dd>
      {onOpen && (
        <Button size="sm" variant="ghost" onClick={onOpen}>
          <ExternalLink size={ICON.xs} />
          {t('apps.reveal')}
        </Button>
      )}
    </div>
  );
}

/**
 * recoveryLine renders what the application's own Host reports about its
 * state root: the pass this process ran when it assembled the
 * application, or the live process that owns the root instead. The
 * wording is the application's, not the workspace card's — an
 * application's state root is an internal path, and "another live
 * process holds this workspace" would name something the user never
 * picked (see the app platform plan, §3.7).
 */
function recoveryLine(
  t: (key: string, opts?: Record<string, unknown>) => string,
  recovery?: gen.AppRecovery,
): string {
  if (!recovery?.at) return t('apps.diag.recoveryIdle');
  if (recovery.holder) {
    return t('apps.diag.recoveryHeld', { holder: recovery.holder });
  }
  if (recovery.recovered > 0) {
    return t('apps.diag.recoveryRecovered', { count: recovery.recovered });
  }
  return t('apps.diag.recoveryClean');
}

// recoveryTone highlights the two states a user has to act on or at
// least know about: turns that came back interrupted, and a state root
// another live process owns (this process ran no recovery for them).
function recoveryTone(recovery?: gen.AppRecovery): string {
  if (!recovery?.at) return 'text-faint';
  return recovery.holder || recovery.recovered > 0 ? 'text-warn' : 'text-dim';
}
