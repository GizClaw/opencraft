import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  AppWindow,
  Blocks,
  FolderOpen,
  Loader2,
  Play,
  RefreshCw,
  Square,
  Stethoscope,
  Trash2,
} from 'lucide-react';
import { api } from '../lib/api';
import { useAppsStore } from '../apps/store';
import { Badge } from './ui/Badge';
import { Button } from './ui/Button';
import { ConfirmDialog } from './ui/ConfirmDialog';
import { EmptyState } from './ui/EmptyState';
import { ICON } from './ui/icon';
import type { AppSummary } from '../apps/store';

// AppIcon renders a manifest icon: an emoji/glyph as text, or a path
// inside the content root as a base64 image the host reads back. The
// distinction mirrors the Go side (apps.iconIsPath), because the same
// string is either the icon itself or a reference to one.
const IMAGE_EXT = /\.(png|jpe?g|svg|webp|gif|avif)$/i;

export function appIconIsPath(icon: string): boolean {
  return icon.includes('/') || IMAGE_EXT.test(icon);
}

export function AppIcon({
  id,
  icon,
  size = ICON.lg,
}: {
  id: string;
  icon?: string;
  size?: string;
}) {
  const [url, setUrl] = useState('');
  const isPath = !!icon && appIconIsPath(icon);
  useEffect(() => {
    if (!isPath) return;
    let blobURL = '';
    let cancelled = false;
    void api
      .appAsset(id, icon!)
      .then((asset) => {
        if (cancelled || !asset.data) return;
        const binary = atob(asset.data);
        const bytes = new Uint8Array(binary.length);
        for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
        blobURL = URL.createObjectURL(
          new Blob([bytes], { type: asset.media_type || 'image/png' }),
        );
        setUrl(blobURL);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
      if (blobURL) URL.revokeObjectURL(blobURL);
    };
  }, [id, icon, isPath]);
  if (isPath) {
    return url ? (
      <img
        src={url}
        alt=""
        aria-hidden="true"
        className="shrink-0 rounded-tight object-cover"
        style={{ width: size, height: size }}
      />
    ) : (
      <AppWindow size={size} className="shrink-0 text-dim" />
    );
  }
  if (icon) {
    return (
      <span
        aria-hidden="true"
        className="shrink-0 text-center leading-none"
        style={{ fontSize: size }}
      >
        {icon}
      </span>
    );
  }
  return <AppWindow size={size} className="shrink-0 text-dim" />;
}

// StatusDot is the card's state: a Host serving it, a reload draining,
// an installed but disabled application, or a manifest the host could
// not read.
function StatusDot({ app }: { app: AppSummary }) {
  const status = useAppsStore((s) => s.status[app.id]);
  const { t } = useTranslation();
  if (app.error) {
    return (
      <Badge tone="err" className="gap-1">
        <span className="h-1.5 w-1.5 rounded-full bg-err" />
        {t('apps.state.error')}
      </Badge>
    );
  }
  if (!app.enabled) {
    return (
      <Badge tone="neutral" className="gap-1">
        <span className="h-1.5 w-1.5 rounded-full bg-dim" />
        {t('apps.state.disabled')}
      </Badge>
    );
  }
  if (status?.retiring) {
    return (
      <Badge tone="warn" className="gap-1">
        <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-warn" />
        {t('apps.state.retiring')}
      </Badge>
    );
  }
  if (status?.serving) {
    return (
      <Badge tone="ok" className="gap-1">
        <span className="h-1.5 w-1.5 rounded-full bg-ok" />
        {t('apps.state.running')}
      </Badge>
    );
  }
  return (
    <Badge tone="accent" className="gap-1">
      <span className="h-1.5 w-1.5 rounded-full bg-accent" />
      {t('apps.state.idle')}
    </Badge>
  );
}

/**
 * AppGallery is the page's default surface: one card per installed
 * application with the actions that do not need the page open (enable,
 * disable, reload, reveal the content root, uninstall). Opening a card
 * is what mounts the application's own surface.
 *
 * Everything here is a registry or pool action. Nothing in the gallery
 * runs application code: an application is imported, enabled and
 * assembled without its bundle being evaluated — the bundle runs when
 * its page is opened.
 */
export function AppGallery({
  onOpen,
  onDiagnostics,
  onImport,
}: {
  onOpen: (id: string) => void;
  onDiagnostics: (id: string) => void;
  onImport: () => void;
}) {
  const apps = useAppsStore((s) => s.apps);
  const loading = useAppsStore((s) => s.loading);
  const busy = useAppsStore((s) => s.busy);
  const setEnabled = useAppsStore((s) => s.setEnabled);
  const reloadApp = useAppsStore((s) => s.reloadApp);
  const revealApp = useAppsStore((s) => s.revealApp);
  const uninstall = useAppsStore((s) => s.uninstall);
  const { t } = useTranslation();
  const [confirmRemove, setConfirmRemove] = useState<AppSummary | null>(null);
  const [purge, setPurge] = useState(false);

  if (loading && apps.length === 0) {
    return (
      <div className="grid h-full place-items-center text-dim">
        <Loader2 className="animate-spin" size={ICON.lg} />
      </div>
    );
  }

  if (apps.length === 0) {
    return (
      <EmptyState
        icon={Blocks}
        title={t('apps.empty.title')}
        hint={t('apps.empty.hint')}
      >
        <Button onClick={onImport}>
          <FolderOpen size={ICON.sm} />
          {t('apps.import')}
        </Button>
      </EmptyState>
    );
  }

  const toggle = async (app: AppSummary) => {
    // The scope (if the page ever loaded one) is not this card's to
    // unload: the registry change the call produces does that, and the
    // page that is showing the bundle is what runs the cleanup.
    await setEnabled(app.id, !app.enabled);
  };

  return (
    <div className="flex flex-col gap-3 p-4">
      <div className="flex items-center gap-2">
        <h3 className="text-sm font-semibold">{t('apps.installed')}</h3>
        <Badge tone="neutral">{apps.length}</Badge>
        <span className="flex-1" />
        <Button size="sm" variant="secondary" onClick={onImport}>
          <FolderOpen size={ICON.sm} />
          {t('apps.import')}
        </Button>
      </div>
      <div className="grid grid-cols-[repeat(auto-fill,minmax(18rem,1fr))] gap-3">
        {apps.map((app) => (
          <section
            key={app.id}
            data-testid={`app-card-${app.id}`}
            className="flex flex-col gap-2 rounded-card border border-edge bg-panel2 p-3"
          >
            <div className="flex items-start gap-2">
              <AppIcon id={app.id} icon={app.icon} />
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-medium">
                  {app.name || app.id}
                </div>
                <div className="truncate text-xs text-faint">
                  {app.version ? `v${app.version}` : app.id}
                  {app.builtin ? ` · ${t('apps.builtin')}` : ''}
                </div>
              </div>
              <StatusDot app={app} />
            </div>
            {app.description && (
              <p className="line-clamp-2 text-xs text-dim">{app.description}</p>
            )}
            {app.error && (
              <p className="break-words rounded-tight border border-err/40 bg-err/5 px-2 py-1 text-xs text-err">
                {app.error}
              </p>
            )}
            <div className="flex flex-wrap items-center gap-1.5">
              <Button
                size="sm"
                variant="secondary"
                disabled={!app.enabled || !!app.error}
                onClick={() => onOpen(app.id)}
              >
                <AppWindow size={ICON.sm} />
                {t('apps.open')}
              </Button>
              <Button
                size="sm"
                variant="secondary"
                loading={!!busy[app.id]}
                onClick={() => void toggle(app)}
              >
                {app.enabled ? (
                  <>
                    <Square size={ICON.sm} />
                    {t('apps.disable')}
                  </>
                ) : (
                  <>
                    <Play size={ICON.sm} />
                    {t('apps.enable')}
                  </>
                )}
              </Button>
              <Button
                size="sm"
                variant="ghost"
                disabled={!app.enabled}
                data-tip={t('apps.reload')}
                onClick={() => void reloadApp(app.id)}
              >
                <RefreshCw size={ICON.sm} />
              </Button>
              <Button
                size="sm"
                variant="ghost"
                data-tip={t('apps.reveal')}
                onClick={() => void revealApp(app.id)}
              >
                <FolderOpen size={ICON.sm} />
              </Button>
              <Button
                size="sm"
                variant="ghost"
                data-tip={t('apps.diagnostics')}
                onClick={() => onDiagnostics(app.id)}
              >
                <Stethoscope size={ICON.sm} />
              </Button>
              {!app.builtin && (
                <Button
                  size="sm"
                  variant="ghost"
                  className="text-err"
                  data-tip={t('apps.uninstall')}
                  onClick={() => {
                    setPurge(false);
                    setConfirmRemove(app);
                  }}
                >
                  <Trash2 size={ICON.sm} />
                </Button>
              )}
            </div>
          </section>
        ))}
      </div>
      <ConfirmDialog
        open={confirmRemove !== null}
        tone="danger"
        title={t('apps.removeTitle', { name: confirmRemove?.name ?? '' })}
        body={
          <>
            <p>{t('apps.removeBody')}</p>
            {/* The state root holds the application's conversations and
                its private workspace, so deleting it is a second,
                explicit choice — and the default is to keep it. */}
            <label className="mt-2 flex items-center gap-2 text-xs text-dim">
              <input
                type="checkbox"
                checked={purge}
                onChange={(e) => setPurge(e.target.checked)}
                data-testid="app-purge"
              />
              {t('apps.removePurge')}
            </label>
          </>
        }
        confirmLabel={t('apps.uninstall')}
        onCancel={() => setConfirmRemove(null)}
        onConfirm={() => {
          const app = confirmRemove;
          setConfirmRemove(null);
          if (!app) return;
          void uninstall(app.id, purge);
        }}
      />
    </div>
  );
}
