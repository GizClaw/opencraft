import { usePluginStore } from '../store';

// PluginPanels renders the settingsPanels contribution point for one
// settings tab. Panels without an explicit tab render in the plugins
// tab (backwards compatible with the original placement).
export function PluginPanels({ tab }: { tab: string }) {
  const panels = usePluginStore((s) => s.panels);
  const visible = panels.filter((p) => (p.tab ?? 'plugins') === tab);
  if (visible.length === 0) return null;
  return (
    <div className="flex flex-col gap-3">
      {visible.map((panel) => (
        <section
          key={panel.id}
          className="rounded-xl border border-edge bg-panel2 p-3"
        >
          <h3 className="mb-2 text-xs font-semibold text-dim">{panel.title}</h3>
          <panel.Component />
        </section>
      ))}
    </div>
  );
}
