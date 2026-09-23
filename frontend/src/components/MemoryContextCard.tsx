import { FoldVertical, Layers } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { formatBytes } from '../lib/metricFormat';
import type { MemorySettings } from '../lib/types';
import { ICON } from './ui/icon';
import { SaveBar } from './ui/SaveBar';
import { NumberSetting, SettingRow, ToggleSetting } from './ui/SettingRow';

// The context card of the Memory tab: how much of the conversation the
// model is handed verbatim, and when the older turns are folded into a
// summary.
//
// It used to be a paragraph followed by three unlabelled numbers in the
// tab body, with the replay switch in a card of its own below — three
// shapes for one group of settings, and no unit on any of the numbers.
// They are one card now: each row says what its count counts, the
// derived line spells out the arithmetic the two counts add up to, and
// the save bar closes the card it belongs to.
export function MemoryContextCard({
  settings,
  onChange,
  saved,
  dirty,
  saving,
  error,
  onSave,
}: {
  settings: MemorySettings;
  onChange: (patch: Partial<MemorySettings>) => void;
  saved: boolean;
  /** The form on screen differs from the values the runtime is using. */
  dirty: boolean;
  saving: boolean;
  error: string;
  onSave: () => void;
}) {
  const { t } = useTranslation();
  // Folding keeps the newest messages raw: the raw window plus the
  // preserved ones (see the memory summary provider). Showing the sum is
  // the only way two adjacent counts read as one window.
  const verbatim = settings.max_raw_messages + settings.preserve_recent;
  return (
    <section className="rounded-card border border-edge bg-panel2">
      <div className="px-4 py-3.5">
        <h3 className="flex items-center gap-2 text-title font-semibold">
          <Layers size={ICON.md} className="shrink-0 text-accent" />
          {t('config.memoryContextTitle')}
        </h3>
        <p className="mt-1 max-w-2xl text-xs text-dim">
          {t('config.memoryHint')}
        </p>
      </div>

      <div className="space-y-3 border-t border-edge px-4 py-3.5">
        <SettingRow
          label={t('config.memoryRawWindow')}
          hint={t('config.memoryRawWindowHint')}
        >
          <NumberSetting
            label={t('config.memoryRawWindow')}
            unit={t('config.unitMessages')}
            min={0}
            value={settings.max_raw_messages}
            onChange={(value) => onChange({ max_raw_messages: value })}
          />
        </SettingRow>
        <SettingRow
          label={t('config.memoryPreserveRecent')}
          hint={t('config.memoryPreserveRecentHint')}
        >
          <NumberSetting
            label={t('config.memoryPreserveRecent')}
            unit={t('config.unitMessages')}
            min={0}
            value={settings.preserve_recent}
            onChange={(value) => onChange({ preserve_recent: value })}
          />
        </SettingRow>
        <SettingRow
          label={t('config.memorySummaryBytes')}
          hint={t('config.memorySummaryBytesHint', {
            size: formatBytes(settings.max_summary_bytes),
          })}
        >
          <NumberSetting
            label={t('config.memorySummaryBytes')}
            unit={t('config.unitBytes')}
            min={0}
            step={1024}
            value={settings.max_summary_bytes}
            onChange={(value) => onChange({ max_summary_bytes: value })}
          />
        </SettingRow>
        {/* The note is the card's conclusion, not one more knob, so it is
            set as a footnote under the three rows it adds up: a box in
            this column reads as a fourth field to fill in. */}
        <p className="flex items-start gap-1.5 text-micro text-dim">
          <FoldVertical size={ICON.xs} className="mt-px shrink-0 text-faint" />
          <span>{t('config.memoryFoldNote', { count: verbatim })}</span>
        </p>
      </div>

      <div className="border-t border-edge px-4 py-3">
        <ToggleSetting
          label={t('config.memoryReplay')}
          hint={t('config.memoryReplayHint')}
          checked={settings.replay_full_history}
          onChange={(checked) => onChange({ replay_full_history: checked })}
        />
      </div>

      <SaveBar saved={saved} error={error} saving={saving} onSave={onSave}>
        {/* On a tab with three save bars, the one whose card was edited is
            the one that says so. */}
        {dirty && <span className="text-dim">{t('config.unsaved')}</span>}
      </SaveBar>
    </section>
  );
}
