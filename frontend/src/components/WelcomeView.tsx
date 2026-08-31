import { FolderOpen, Settings } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useStore } from '../lib/store';

// WelcomeView is the first-run landing screen shown when no workspace
// is selected: the app refuses to silently open a default directory
// (e.g. the user's home), so it asks for an explicit pick instead.
export function WelcomeView() {
  const chooseWorkspace = useStore((s) => s.chooseWorkspace);
  const configured = useStore((s) => s.configured);
  const openConfig = useStore((s) => s.openConfig);
  const { t } = useTranslation();

  return (
    <div className="flex-1 grid place-items-center">
      <div className="text-center space-y-5 max-w-md px-6">
        <div className="mx-auto w-14 h-14 rounded-2xl border border-edge bg-panel2 grid place-items-center">
          <FolderOpen size="1.7143rem" className="text-accent" />
        </div>
        <div className="space-y-2">
          <h2 className="text-lg font-semibold text-fg">
            {t('welcome.title')}
          </h2>
          <p className="text-sm text-dim leading-relaxed">
            {t('welcome.body')}
          </p>
        </div>
        <div className="flex items-center justify-center gap-3">
          <button
            onClick={() => void chooseWorkspace()}
            className="flex items-center gap-2 rounded-lg bg-accent px-4 py-2 text-sm text-white hover:opacity-90 transition-opacity"
          >
            <FolderOpen size="1.0714rem" />
            {t('welcome.chooseWorkspace')}
          </button>
          {!configured && (
            <button
              onClick={() => openConfig()}
              className="flex items-center gap-2 rounded-lg border border-edge px-4 py-2 text-sm text-fg hover:border-accent/50 transition-colors"
            >
              <Settings size="1.0714rem" />
              {t('chat.openSettings')}
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
