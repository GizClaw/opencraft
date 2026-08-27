import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api } from '../lib/api';
import type { ProjectConfigStatus } from '../lib/types';

// ProjectTrustBanner warns when the current workspace ships a project
// configuration layer that is being skipped because the user has not
// trusted it. Trusting applies the layer (hooks, sandbox policy,
// graph) and rebuilds the runtime.
export const ProjectTrustBanner = () => {
  const { t } = useTranslation();
  const [status, setStatus] = useState<ProjectConfigStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    let alive = true;
    api
      .projectConfigStatus()
      .then((s) => {
        if (alive) setStatus(s);
      })
      .catch(() => {
        // The banner is best-effort; a failure must not block the UI.
      });
    return () => {
      alive = false;
    };
  }, []);

  if (!status || !status.present || status.trusted) {
    return null;
  }

  const trust = async () => {
    setBusy(true);
    setError('');
    try {
      const wd = await api.workspace();
      await api.setProjectTrust(wd, true);
      setStatus({ ...status, trusted: true });
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="project-trust-banner" role="alert">
      <div className="project-trust-copy">
        <strong>{t('projectTrust.title')}</strong>
        <span>{t('projectTrust.body')}</span>
      </div>
      {error && <span className="project-trust-error">{error}</span>}
      <button onClick={() => void trust()} disabled={busy}>
        {busy ? t('projectTrust.working') : t('projectTrust.trust')}
      </button>
    </div>
  );
};
