import { usePluginStore } from '../store';
import { ErrorBoundary } from '../../components/ErrorBoundary';

// PluginPanels renders the settingsPanels contribution point for one
// settings surface, optionally scoped to one plugin's detail drawer.
// Known tab values: "general" (General settings), "display"
// (Interface/display settings), "plugins" (plugin detail drawer, the
// default) and "import" (settings Import tab for plugin-provided
// session import UIs).
export function PluginPanels({
  tab,
  pluginId,
}: {
  tab: string;
  pluginId?: string;
}) {
  const panels = usePluginStore((s) => s.panels);
  const visible = panels.filter(
    (p) =>
      (p.tab ?? 'plugins') === tab &&
      (pluginId === undefined || p.pluginId === pluginId),
  );
  if (visible.length === 0) return null;
  return (
    <div className="flex flex-col gap-3">
      {visible.map((panel) => (
        <section
          key={panel.id}
          className="min-w-0 overflow-x-auto rounded-card border border-edge bg-panel2 p-3"
        >
          <h3 className="mb-2 break-words text-xs font-semibold text-dim">
            {panel.title}
          </h3>
          {/* One plugin panel crashing on render used to take the whole
              settings tree with it (React unmounts and rebuilds from the
              nearest boundary). Scoping a boundary per panel keeps the
              failure local and stops the remount churn. */}
          <ErrorBoundary scope={`plugin-panel:${panel.pluginId}`} inline>
            <panel.Component />
          </ErrorBoundary>
        </section>
      ))}
    </div>
  );
}
