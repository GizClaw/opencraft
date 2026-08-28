import { useTranslation } from 'react-i18next';
import { usePluginStore } from '../store';

// PluginEntries renders the sidebarEntries contribution point in the
// sidebar (Phase 0: a small section listing plugin entries).
export function PluginEntries() {
  const { t } = useTranslation();
  const entries = usePluginStore((s) => s.entries);
  if (entries.length === 0) return null;
  return (
    <section>
      <div className="mb-2 flex items-center justify-between">
        <h3 className="text-xs uppercase tracking-wider text-dim">
          {t('sidebar.plugins')}
        </h3>
      </div>
      <ul className="space-y-1">
        {entries.map((entry) => (
          <li key={entry.id}>
            <button
              onClick={entry.onClick}
              className="w-full rounded-lg px-1.5 py-1 text-left text-sm hover:bg-panel2"
            >
              {entry.title}
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}
