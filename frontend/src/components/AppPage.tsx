import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ArrowLeft, Loader2, Stethoscope } from 'lucide-react';
import { useAppsStore } from '../apps/store';
import { appScope, disposeAppScope, ensureAppScope } from '../apps/host';
import { AppChat } from './AppChat';
import { AppDiagnostics } from './AppDiagnostics';
import { AppGallery, AppIcon } from './AppGallery';
import { AppImportWizard } from './AppImportWizard';
import { AppViewHost } from './AppViewHost';
import { Badge } from './ui/Badge';
import { ICON } from './ui/icon';
import type { LiveAppScope } from '../apps/host';
import type { AppSummary } from '../apps/store';

/** WizardTarget is which wizard is open, if any: an import of a new
 *  package, or the update of one installed application. */
type WizardTarget = { kind: 'install' } | { kind: 'update'; app: AppSummary };

/**
 * AppPage is the applications page: the left column lists what is
 * installed, the main area is either the gallery of cards or one
 * application's own surface.
 *
 * It is global state on purpose (the app platform plan, §4.1): an
 * application's content and state roots do not hang under the active
 * workspace, so switching workspaces neither closes this page nor
 * changes what it shows.
 */
export function AppPage() {
  const { t } = useTranslation();
  const apps = useAppsStore((s) => s.apps);
  const loading = useAppsStore((s) => s.loading);
  const openID = useAppsStore((s) => s.openID);
  const open = useAppsStore((s) => s.open);
  const close = useAppsStore((s) => s.close);
  const load = useAppsStore((s) => s.load);
  // A registry change to the application being shown (an update landing,
  // a disable, a removal) makes the loaded bundle the predecessor of the
  // one installed now: the count re-runs the effect below, which unloads
  // it and loads whatever the registry holds.
  const revision = useAppsStore((s) =>
    openID ? (s.revisions[openID] ?? 0) : 0,
  );
  const [scope, setScope] = useState<LiveAppScope | null>(null);
  const [tab, setTab] = useState('chat');
  const [diagnostics, setDiagnostics] = useState(false);
  const [wizard, setWizard] = useState<WizardTarget | null>(null);

  useEffect(() => {
    void load();
  }, [load]);

  // Opening an application loads its bundle once. The scope is what has
  // its views; an application without a frontend still gets a scope (an
  // empty one), because the page's conversation needs its manifest.
  //
  // The cleanup is the only place that unloads a bundle, so switching
  // applications, stepping back to the gallery, closing the tool page and
  // the registry changing under an open page all leave the same way: the
  // views, the effects and the stylesheet go together.
  useEffect(() => {
    if (!openID) {
      setScope(null);
      setDiagnostics(false);
      return;
    }
    let cancelled = false;
    setTab('chat');
    setScope(appScope(openID) ?? null);
    void ensureAppScope(openID).then((loaded) => {
      if (cancelled) return;
      // Re-read after the load: a bundle's views are registered while
      // apply(ctx) runs, which is before this promise settles.
      setScope(loaded);
      if (!loaded.views.some((v) => v.id === 'chat')) {
        setTab((current) =>
          loaded.views.length > 0 && current === 'chat'
            ? loaded.views[0].id
            : current,
        );
      }
    });
    return () => {
      cancelled = true;
      disposeAppScope(openID);
    };
  }, [openID, revision]);

  // The scope of the application the page is showing, or null while one
  // is loading. A scope belonging to another application is the previous
  // one, on its way out — rendering it under the new application's name
  // would show one application's views in another one's page.
  const live = scope && scope.id === openID ? scope : null;

  const current = useMemo(
    () => apps.find((a) => a.id === openID),
    [apps, openID],
  );

  // Leaving goes through the effect's cleanup, so the gallery and a
  // switch between applications unload a bundle the same way.
  const back = () => close();

  return (
    <div className="flex min-h-0 flex-1">
      <aside className="flex w-56 shrink-0 flex-col border-r border-edge">
        <div className="flex-1 overflow-y-auto p-2">
          {apps.map((app) => (
            <button
              key={app.id}
              data-testid={`app-nav-${app.id}`}
              onClick={() => open(app.id)}
              className={`mb-0.5 flex w-full items-center gap-2 rounded-control px-2 py-1.5 text-left text-xs transition-colors ${
                openID === app.id
                  ? 'bg-panel3 text-fg'
                  : 'text-dim hover:bg-panel2 hover:text-fg'
              }`}
            >
              <AppIcon id={app.id} icon={app.icon} size={ICON.sm} />
              <span className="min-w-0 flex-1 truncate">
                {app.name || app.id}
              </span>
              {!app.enabled && (
                <Badge tone="neutral">{t('apps.state.disabled')}</Badge>
              )}
              {app.error && <Badge tone="err">!</Badge>}
            </button>
          ))}
          {!loading && apps.length === 0 && (
            <p className="px-2 py-3 text-xs text-faint">
              {t('apps.empty.title')}
            </p>
          )}
        </div>
      </aside>
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">
        {!openID || !current ? (
          <div className="min-h-0 flex-1 overflow-y-auto">
            <AppGallery
              onOpen={open}
              onUpdate={(app) => setWizard({ kind: 'update', app })}
              onDiagnostics={(id) => {
                open(id);
                setDiagnostics(true);
              }}
              onImport={() => setWizard({ kind: 'install' })}
            />
          </div>
        ) : (
          <>
            <header className="flex shrink-0 items-center gap-2 border-b border-edge px-4 py-2">
              <button
                onClick={back}
                className="text-dim hover:text-fg"
                data-tip={t('apps.back')}
                data-testid="app-back"
              >
                <ArrowLeft size={ICON.md} />
              </button>
              <AppIcon id={openID} icon={current.icon} size={ICON.md} />
              <h2 className="truncate text-sm font-semibold">
                {current.name || openID}
              </h2>
              <span className="text-xs text-faint">
                {current.version ? `v${current.version}` : ''}
              </span>
              <span className="flex-1" />
              <button
                type="button"
                data-testid="app-diagnostics-toggle"
                onClick={() => setDiagnostics((v) => !v)}
                className={`flex items-center gap-1 rounded-control px-2 py-1 text-xs ${
                  diagnostics ? 'bg-panel3 text-fg' : 'text-dim hover:text-fg'
                }`}
              >
                <Stethoscope size={ICON.sm} />
                {t('apps.diagnostics')}
              </button>
              {live === null && (
                <Loader2 size={ICON.sm} className="animate-spin text-dim" />
              )}
            </header>
            {diagnostics ? (
              <AppDiagnostics appID={openID} />
            ) : live ? (
              <AppViewHost scope={live} tab={tab} onTab={setTab}>
                <AppChat appID={openID} manifest={live.manifest} />
              </AppViewHost>
            ) : (
              <div className="grid flex-1 place-items-center text-dim">
                <Loader2 size={ICON.lg} className="animate-spin" />
              </div>
            )}
          </>
        )}
      </div>
      <AppImportWizard
        open={wizard !== null}
        update={wizard?.kind === 'update' ? wizard.app : null}
        onClose={() => setWizard(null)}
        onDone={(id) => {
          setWizard(null);
          // The install enabled the application, so its page is what the
          // user asked for: open it rather than leaving them on the
          // gallery to find the new card. An update of an application
          // that was already open lands on the same page, which the
          // registry change has already reloaded onto the new bundle.
          open(id);
        }}
      />
    </div>
  );
}
