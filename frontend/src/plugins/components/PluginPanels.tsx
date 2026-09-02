import { usePluginStore } from '../store';

// PluginPanels renders the settingsPanels contribution point for one
// settings tab, optionally scoped to one plugin's detail drawer.
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
          className="min-w-0 overflow-x-auto rounded-xl border border-edge bg-panel2 p-3"
        >
          <h3 className="mb-2 break-words text-xs font-semibold text-dim">
            {panel.title}
          </h3>
          <panel.Component />
        </section>
      ))}
    </div>
  );
}
