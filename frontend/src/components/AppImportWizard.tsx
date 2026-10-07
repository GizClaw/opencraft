import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  AlertTriangle,
  FileArchive,
  FolderOpen,
  PackageSearch,
} from 'lucide-react';
import { api } from '../lib/api';
import { useAppsStore } from '../apps/store';
import type { AppSummary } from '../apps/store';
import { AppIcon } from './AppGallery';
import { Button } from './ui/Button';
import { Modal } from './ui/Modal';
import { ICON } from './ui/icon';
import type * as gen from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/models';
import type * as genApps from '../../bindings/github.com/GizClaw/opencraft/internal/capabilities/apps/models';

// The wizard's own view of what it is importing. A directory and a zip
// end up in the same place: a source the registry can read, and the
// answers the preflight gave about it.
interface Candidate {
  /** src is the directory or the zip the user picked. */
  src: string;
  zip: boolean;
  inspection?: genApps.Inspection;
  /** error is the refusal that stops the wizard before the form: a
   *  source the host cannot even read a manifest out of. */
  error?: string;
}

/**
 * AppImportWizard installs or updates one application package.
 *
 * Three steps, and nothing is copied before the last one: pick a source
 * (a directory or a zip), read the preflight's verdict over it, then
 * confirm or edit the identity the manifest will be installed under. The
 * refusals are rows rather than one error string, because the fix is
 * per-row — a reserved key in one layer, a reference that leaves the
 * content root in another — and the wizard is the only place with room
 * to show them all.
 *
 * An update is the same wizard with the identity decided: the package
 * has to declare the id it replaces (the registry refuses anything else),
 * so the form is not shown — what the user is picking is a version, and
 * the version rule itself is the backend's to apply.
 */
export function AppImportWizard({
  open,
  update,
  onClose,
  onDone,
}: {
  open: boolean;
  /** update is the installed application an update targets; null means
   *  the wizard is importing a new one. */
  update: AppSummary | null;
  onClose: () => void;
  /** onDone opens the application the wizard just installed or updated. */
  onDone: (id: string) => void;
}) {
  const { t } = useTranslation();
  const load = useAppsStore((s) => s.load);
  const [candidate, setCandidate] = useState<Candidate | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [form, setForm] = useState({ id: '', name: '', icon: '' });
  const [iconTouched, setIconTouched] = useState(false);
  const updating = update !== null;

  // A reopened wizard starts where a fresh one does: the previous
  // package is not still being considered.
  useEffect(() => {
    if (open) return;
    setCandidate(null);
    setError('');
    setForm({ id: '', name: '', icon: '' });
    setIconTouched(false);
  }, [open]);

  const inspect = async (src: string, zip: boolean) => {
    setBusy(true);
    setError('');
    try {
      const inspection = await api.appInspect(src);
      setCandidate({ src, zip, inspection });
      setForm({
        id: inspection.Summary.id,
        name: inspection.Summary.name,
        icon: inspection.Summary.icon ?? '',
      });
      setIconTouched(false);
    } catch (err) {
      // The refusal that arrives as an error is the one about the
      // package itself (no manifest, a manifest the host cannot parse),
      // not about a key inside it.
      setCandidate({ src, zip, error: String(err) });
    } finally {
      setBusy(false);
    }
  };

  const pickFolder = async () => {
    try {
      const path = await api.pickFolder(t('apps.wizard.chooseFolder'));
      if (path) await inspect(path, false);
    } catch (err) {
      setError(String(err));
    }
  };

  const pickZip = async () => {
    try {
      const path = await api.pickFile(t('apps.wizard.chooseZip'), '*.zip');
      if (path) await inspect(path, true);
    } catch (err) {
      setError(String(err));
    }
  };

  const submit = async () => {
    if (!candidate?.inspection) return;
    setBusy(true);
    setError('');
    try {
      let summary: genApps.Summary;
      if (update) {
        summary = candidate.zip
          ? await api.appUpdateZip(update.id, candidate.src)
          : await api.appUpdate(update.id, candidate.src);
      } else {
        const opts: gen.AppInstallOptions = {
          id: form.id.trim(),
          name: form.name.trim(),
        };
        // An icon field the user never touched is left as the manifest
        // wrote it; one they cleared is an install without an icon, which
        // the null says explicitly.
        if (iconTouched) opts.icon = form.icon.trim();
        summary = candidate.zip
          ? await api.appInstallZip(candidate.src, opts)
          : await api.appInstall(candidate.src, opts);
      }
      await load();
      onDone(summary.id);
    } catch (err) {
      setError(String(err));
      // A failed update may still have landed the new version — the
      // registry swapped it, the assembly refused it — so the cards
      // behind the wizard are re-read either way.
      if (updating) await load();
    } finally {
      setBusy(false);
    }
  };

  const refusals = candidate?.inspection?.Refusals ?? [];
  const layers = candidate?.inspection?.Layers ?? [];
  // The one thing the wizard can decide that the registry cannot: an
  // update replaces a named application, so a package for another one is
  // the wrong pick rather than a new install.
  const mismatch =
    updating && !!candidate?.inspection
      ? candidate.inspection.Summary.id !== update.id
      : false;
  const installable =
    !!candidate?.inspection && refusals.length === 0 && !mismatch;

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={
        update
          ? t('apps.wizard.updateTitle', { name: update.name || update.id })
          : t('apps.wizard.title')
      }
      icon={PackageSearch}
      width="42rem"
      footer={
        <div className="flex items-center justify-end gap-2">
          <Button variant="secondary" onClick={onClose}>
            {t('sidebar.cancel')}
          </Button>
          <Button
            disabled={!installable || busy}
            loading={busy}
            onClick={() => void submit()}
            data-testid="app-install-confirm"
          >
            {update ? t('apps.wizard.update') : t('apps.wizard.install')}
          </Button>
        </div>
      }
    >
      <div className="flex flex-col gap-3">
        <div className="flex items-center gap-2">
          <Button
            size="sm"
            variant="secondary"
            loading={busy}
            onClick={() => void pickFolder()}
            data-testid="app-pick-folder"
          >
            <FolderOpen size={ICON.sm} />
            {t('apps.wizard.chooseFolder')}
          </Button>
          <Button
            size="sm"
            variant="secondary"
            loading={busy}
            onClick={() => void pickZip()}
            data-testid="app-pick-zip"
          >
            <FileArchive size={ICON.sm} />
            {t('apps.wizard.chooseZip')}
          </Button>
          {candidate && (
            <span
              className="min-w-0 flex-1 truncate text-xs text-faint"
              data-tip={candidate.src}
            >
              {candidate.src}
            </span>
          )}
        </div>

        {error && (
          <p className="break-words rounded-card border border-err/40 bg-err/5 px-3 py-2 text-xs text-err">
            {error}
          </p>
        )}

        {candidate?.error && (
          <div className="rounded-card border border-err/40 bg-err/5 px-3 py-2">
            <p className="text-xs font-medium text-err">
              {t('apps.wizard.unusable')}
            </p>
            <p className="mt-1 break-words text-xs text-dim">
              {candidate.error}
            </p>
          </div>
        )}

        {mismatch && candidate?.inspection && update && (
          <p
            className="break-words rounded-card border border-err/40 bg-err/5 px-3 py-2 text-xs text-err"
            data-testid="app-update-mismatch"
          >
            {t('apps.wizard.idMismatch', {
              want: update.id,
              got: candidate.inspection.Summary.id,
            })}
          </p>
        )}

        {candidate?.inspection && (
          <>
            <section className="flex items-center gap-2 rounded-card border border-edge bg-panel2 p-3">
              <AppIcon
                id={form.id}
                icon={
                  iconTouched ? form.icon : candidate.inspection.Summary.icon
                }
              />
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-medium">
                  {candidate.inspection.Summary.name}
                </div>
                <div className="truncate text-xs text-faint">
                  {candidate.inspection.Summary.id} ·{' '}
                  {update
                    ? t('apps.wizard.fromTo', {
                        from: update.version || '—',
                        to: candidate.inspection.Summary.version,
                      })
                    : candidate.inspection.Summary.version}
                  {candidate.inspection.Summary.agent
                    ? ` · ${candidate.inspection.Summary.agent}`
                    : ''}
                </div>
              </div>
            </section>

            <section>
              <h4 className="mb-1 text-xs font-semibold text-dim">
                {t('apps.wizard.layers')}
              </h4>
              {layers.length === 0 ? (
                <p className="text-xs text-faint">
                  {t('apps.wizard.noLayers')}
                </p>
              ) : (
                <ol className="ml-4 list-decimal text-xs text-dim">
                  {layers.map((layer) => (
                    <li key={layer} className="truncate">
                      {layer}
                    </li>
                  ))}
                </ol>
              )}
            </section>

            {refusals.length > 0 && (
              <section
                data-testid="app-refusals"
                className="rounded-card border border-err/40 bg-err/5 p-3"
              >
                <h4 className="flex items-center gap-1 text-xs font-semibold text-err">
                  <AlertTriangle size={ICON.xs} />
                  {t('apps.wizard.refused', { count: refusals.length })}
                </h4>
                <ul className="mt-1 flex flex-col gap-1">
                  {refusals.map((refusal, index) => (
                    <li
                      key={`${refusal.Layer}-${refusal.Key}-${index}`}
                      className="text-xs text-dim"
                    >
                      <span className="font-mono text-fg">
                        {refusal.Layer || refusal.Key || '—'}
                      </span>
                      {refusal.Layer && refusal.Key ? (
                        <span className="font-mono text-fg">
                          {' '}
                          · {refusal.Key}
                        </span>
                      ) : null}
                      <span> — {refusal.Reason}</span>
                    </li>
                  ))}
                </ul>
              </section>
            )}

            {/* An update replaces a named application, so its identity is
                the package's: there is nothing to edit, and the id the
                manifest declares is the one the registry checks. */}
            {!updating && (
              <section className="grid gap-2 sm:grid-cols-2">
                <label className="flex flex-col gap-1 text-xs">
                  <span className="text-faint">{t('apps.wizard.id')}</span>
                  <input
                    className="rounded-control border border-edge bg-panel2 px-2 py-1 text-sm"
                    value={form.id}
                    onChange={(e) => setForm({ ...form, id: e.target.value })}
                  />
                </label>
                <label className="flex flex-col gap-1 text-xs">
                  <span className="text-faint">{t('apps.wizard.name')}</span>
                  <input
                    className="rounded-control border border-edge bg-panel2 px-2 py-1 text-sm"
                    value={form.name}
                    onChange={(e) => setForm({ ...form, name: e.target.value })}
                  />
                </label>
                <label className="flex flex-col gap-1 text-xs sm:col-span-2">
                  <span className="text-faint">{t('apps.wizard.icon')}</span>
                  <input
                    className="rounded-control border border-edge bg-panel2 px-2 py-1 text-sm"
                    value={form.icon}
                    onChange={(e) => {
                      setIconTouched(true);
                      setForm({ ...form, icon: e.target.value });
                    }}
                  />
                </label>
              </section>
            )}
          </>
        )}

        {!candidate && (
          <p className="text-xs text-faint">
            {update ? t('apps.wizard.updateHint') : t('apps.wizard.hint')}
          </p>
        )}
      </div>
    </Modal>
  );
}
