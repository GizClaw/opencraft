import { usePluginStore } from '../store';

// PluginStatusItems renders the statusBar contribution point inside the
// app's status bar.
export function PluginStatusItems() {
  const items = usePluginStore((s) => s.statusBar);
  if (items.length === 0) return null;
  return (
    <>
      {items.map((item) => (
        <span key={item.id}>
          <item.Component />
        </span>
      ))}
    </>
  );
}
