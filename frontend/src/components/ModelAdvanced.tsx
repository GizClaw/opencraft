import { ChevronDown } from 'lucide-react';
import { useTranslation } from 'react-i18next';

// ModelAdvancedValue is the model-row state this section edits:
// discovery metadata plus the driver-specific leaves opencraft does not
// model itself.
export interface ModelAdvancedValue {
  lifecycleStatus: string;
  lifecycleReplacementProvider: string;
  lifecycleReplacementName: string;
  lifecycleNotes: string;
  specJson: string;
}

// ModelAdvanced is the collapsible per-model block. Lifecycle is
// discovery metadata the settings page understands; the spec object is
// whatever driver-specific facts the model declares (ByteDance's
// resolution cap and Seedance parameter matrix, MiniMax's wire-model
// alias and video surface).
export function ModelAdvanced({
  value,
  disabled,
  onUpdate,
}: {
  value: ModelAdvancedValue;
  disabled?: boolean;
  onUpdate: (patch: Partial<ModelAdvancedValue>) => void;
}) {
  const { t } = useTranslation();
  const specError = specJsonError(value.specJson);

  return (
    <details className="rounded-lg border border-edge bg-panel/40">
      <summary className="flex cursor-pointer list-none items-center gap-1.5 px-3 py-1.5 text-xs text-dim hover:text-fg">
        <ChevronDown size="0.8571rem" />
        {t('config.modelAdvanced.title')}
      </summary>
      <div className="grid grid-cols-1 gap-2 px-3 pt-1 pb-3 md:grid-cols-2">
        <label className="flex flex-col gap-1">
          <span className="text-xs text-dim">
            {t('config.modelAdvanced.lifecycle')}
          </span>
          <select
            value={value.lifecycleStatus}
            disabled={disabled}
            onChange={(e) => onUpdate({ lifecycleStatus: e.target.value })}
            className="w-full rounded-lg border border-edge bg-panel px-2 py-1 text-xs outline-none focus:border-accent disabled:opacity-40"
          >
            <option value="">{t('config.modelAdvanced.active')}</option>
            <option value="deprecated">
              {t('config.modelAdvanced.deprecated')}
            </option>
            <option value="retired">{t('config.modelAdvanced.retired')}</option>
          </select>
        </label>
        {value.lifecycleStatus !== '' && (
          <>
            <label className="flex flex-col gap-1">
              <span className="text-xs text-dim">
                {t('config.modelAdvanced.replacementProvider')}
              </span>
              <input
                value={value.lifecycleReplacementProvider}
                disabled={disabled}
                onChange={(e) =>
                  onUpdate({ lifecycleReplacementProvider: e.target.value })
                }
                placeholder="openai"
                className="w-full rounded-lg border border-edge bg-panel px-2 py-1 text-xs outline-none focus:border-accent disabled:opacity-40"
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-xs text-dim">
                {t('config.modelAdvanced.replacementModel')}
              </span>
              <input
                value={value.lifecycleReplacementName}
                disabled={disabled}
                onChange={(e) =>
                  onUpdate({ lifecycleReplacementName: e.target.value })
                }
                className="w-full rounded-lg border border-edge bg-panel px-2 py-1 text-xs outline-none focus:border-accent disabled:opacity-40"
              />
            </label>
            <label className="flex flex-col gap-1 md:col-span-2">
              <span className="text-xs text-dim">
                {t('config.modelAdvanced.notes')}
              </span>
              <input
                value={value.lifecycleNotes}
                disabled={disabled}
                onChange={(e) => onUpdate({ lifecycleNotes: e.target.value })}
                className="w-full rounded-lg border border-edge bg-panel px-2 py-1 text-xs outline-none focus:border-accent disabled:opacity-40"
              />
            </label>
          </>
        )}
        <label className="flex flex-col gap-1 md:col-span-2">
          <span className="text-xs text-dim">
            {t('config.modelAdvanced.driverFields')}
          </span>
          <textarea
            value={value.specJson}
            disabled={disabled}
            spellCheck={false}
            rows={2}
            placeholder={t('config.modelAdvanced.driverFieldsPlaceholder')}
            onChange={(e) => onUpdate({ specJson: e.target.value })}
            className="w-full rounded-lg border border-edge bg-panel px-2 py-1 font-mono text-xs outline-none focus:border-accent disabled:opacity-40"
          />
          {specError && (
            <span className="text-xs text-err">
              {t('config.modelAdvanced.driverFieldsInvalid')}
            </span>
          )}
        </label>
      </div>
    </details>
  );
}

// specJsonError reports whether the edited JSON is a usable object. An
// empty field means "no driver-specific facts".
export function specJsonError(text: string): boolean {
  const trimmed = text.trim();
  if (trimmed === '') return false;
  try {
    const parsed: unknown = JSON.parse(trimmed);
    return (
      parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)
    );
  } catch {
    return true;
  }
}
