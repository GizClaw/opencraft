import { useTranslation } from 'react-i18next';
import { Keyboard } from 'lucide-react';
import { useStore } from '../lib/store';
import {
  formatCombos,
  GROUP_LABELS,
  KEY_REFERENCES,
  SHORTCUTS,
  type ShortcutGroup,
} from '../lib/keys';
import { Overlay } from './ui/Overlay';
import { ICON } from './ui/icon';

// The shortcut sheet (⌘/) is rendered from the same table the dispatcher
// reads, so it cannot drift from what the keys actually do — the reason
// it exists at all is that a fixed keyboard map with no reference is a
// set of keys nobody can find. Rows the shell does not dispatch (the
// composer's Enter, the ruler's arrows) are documented too: the sheet
// answers "what can I press here?", not "what does the shell bind?".
const GROUPS: ShortcutGroup[] = [
  'global',
  'chat',
  'composer',
  'lists',
  'palette',
];

interface Row {
  id: string;
  label: string;
  keys: string[];
}

function rowsFor(group: ShortcutGroup): Row[] {
  const commands: Row[] = SHORTCUTS.filter((spec) => spec.group === group).map(
    (spec) => ({ id: spec.id, label: spec.label, keys: spec.combos }),
  );
  const references: Row[] = KEY_REFERENCES.filter(
    (reference) => reference.group === group,
  ).map((reference) => ({
    id: `${group}:${reference.label}`,
    label: reference.label,
    keys: reference.keys,
  }));
  return [...commands, ...references];
}

export function ShortcutSheet({ isMac }: { isMac: boolean }) {
  const open = useStore((s) => s.shortcutsOpen);
  const close = useStore((s) => s.closeShortcuts);
  const { t } = useTranslation();

  return (
    <Overlay
      open={open}
      onClose={close}
      ariaLabel={t('shortcuts.title')}
      panelClassName="flex max-h-[75vh] w-[46rem] max-w-full flex-col overflow-hidden rounded-card border border-edge bg-panel shadow-modal"
    >
      <div
        data-testid="shortcut-sheet"
        className="flex min-h-0 flex-col overflow-hidden"
      >
        <div className="flex shrink-0 items-center gap-2 border-b border-edge px-4 py-3">
          <Keyboard size={ICON.sm} className="shrink-0 text-dim" />
          <h2 className="text-sm font-medium text-fg">
            {t('shortcuts.title')}
          </h2>
          <span className="flex-1" />
          <span className="text-micro text-faint">{t('shortcuts.footer')}</span>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3">
          {GROUPS.map((group) => {
            const rows = rowsFor(group);
            if (rows.length === 0) return null;
            return (
              <section key={group} className="mb-4 last:mb-0">
                <h3 className="pb-1 text-micro uppercase tracking-wide text-faint">
                  {t(GROUP_LABELS[group])}
                </h3>
                {rows.map((row) => (
                  <div
                    key={row.id}
                    className="flex items-baseline justify-between gap-6 py-1"
                  >
                    <span className="min-w-0 text-sm text-fg">
                      {t(row.label)}
                    </span>
                    <span className="shrink-0 text-xs text-dim tabular-nums">
                      {formatCombos(row.keys, isMac)}
                    </span>
                  </div>
                ))}
              </section>
            );
          })}
        </div>
      </div>
    </Overlay>
  );
}
