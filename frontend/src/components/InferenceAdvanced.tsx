import { ChevronDown } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import type { ProviderAdvanced } from '../lib/types';

// AdvancedSection is the collapsible provider-spec editor on one
// inference instance card. The knobs are the ones flowcraft's drivers
// accept beyond the basic endpoint + models + key flow; which of them
// apply depends on the driver, so the section shows only the fields
// that driver reads. Every field is optional — clearing one removes it
// from the saved spec so the driver default keeps applying.
export function AdvancedSection({
  row,
  driver,
  onUpdate,
}: {
  row: { advanced: ProviderAdvanced };
  driver: string;
  onUpdate: (
    key: keyof ProviderAdvanced,
    value: ProviderAdvanced[keyof ProviderAdvanced],
  ) => void;
}) {
  const { t } = useTranslation();
  const adv = row.advanced ?? {};
  const openaiWire = driver === 'openai';
  const anthropic = driver === 'anthropic';
  const bytedance = driver === 'bytedance';
  const minimax = driver === 'minimax';

  const text = (
    key: keyof ProviderAdvanced,
    label: string,
    placeholder?: string,
  ) => (
    <label className="flex flex-col gap-1">
      <span className="text-xs text-dim">{label}</span>
      <input
        value={(adv[key] as string | undefined) ?? ''}
        onChange={(e) => onUpdate(key, e.target.value)}
        placeholder={placeholder ?? t('config.advanced.default')}
        className="w-full rounded-lg border border-edge bg-panel px-2 py-1 text-xs outline-none focus:border-accent"
      />
    </label>
  );

  // triState edits an optional bool: "" keeps the driver default.
  const triState = (key: keyof ProviderAdvanced, label: string) => {
    const value = adv[key];
    return (
      <label className="flex flex-col gap-1">
        <span className="text-xs text-dim">{label}</span>
        <select
          value={value === undefined ? '' : String(value)}
          onChange={(e) => {
            const raw = e.target.value;
            onUpdate(key, raw === '' ? ('' as never) : raw === 'true');
          }}
          className="w-full rounded-lg border border-edge bg-panel px-2 py-1 text-xs outline-none focus:border-accent"
        >
          <option value="">{t('config.advanced.default')}</option>
          <option value="true">true</option>
          <option value="false">false</option>
        </select>
      </label>
    );
  };

  const choice = (
    key: keyof ProviderAdvanced,
    label: string,
    options: string[],
  ) => (
    <label className="flex flex-col gap-1">
      <span className="text-xs text-dim">{label}</span>
      <select
        value={(adv[key] as string | undefined) ?? ''}
        onChange={(e) => onUpdate(key, e.target.value)}
        className="w-full rounded-lg border border-edge bg-panel px-2 py-1 text-xs outline-none focus:border-accent"
      >
        <option value="">{t('config.advanced.default')}</option>
        {options.map((o) => (
          <option key={o} value={o}>
            {o}
          </option>
        ))}
      </select>
    </label>
  );

  const mapField = (key: 'query' | 'headers', label: string) => (
    <KeyValueEditor
      label={label}
      addLabel={t('config.advanced.addEntry')}
      keyPlaceholder={t('config.advanced.entryKey')}
      valuePlaceholder={t('config.advanced.entryValue')}
      value={adv[key]}
      onChange={(next) => onUpdate(key, next as never)}
    />
  );

  const fields = (
    <>
      {openaiWire && (
        <>
          <label className="flex items-center gap-2 pt-4">
            <input
              type="checkbox"
              checked={adv.video_input ?? false}
              onChange={(e) => onUpdate('video_input', e.target.checked)}
              className="accent-accent"
            />
            <span className="text-xs text-dim">
              {t('config.advanced.videoInput')}
            </span>
          </label>
          {choice('routing', t('config.advanced.routing'), [
            'azure_deployment',
          ])}
          {text('organization', t('config.advanced.organization'))}
          {text('project', t('config.advanced.project'))}
          {text('timeout', t('config.advanced.timeout'), '90s')}
          {choice('auth_scheme', t('config.advanced.authScheme'), [
            'bearer',
            'header',
          ])}
          {text('auth_header', t('config.advanced.authHeader'), 'api-key')}
          {choice('metadata_envelope', t('config.advanced.metadata'), [
            'client_metadata',
            'metadata',
            '-',
          ])}
          {text('http_retries', t('config.advanced.httpRetries'), '2')}
          <label className="flex flex-col gap-1">
            <span className="text-xs text-dim">
              {t('config.advanced.store')}
            </span>
            <select
              value={adv.store ?? ''}
              onChange={(e) => onUpdate('store', e.target.value)}
              className="w-full rounded-lg border border-edge bg-panel px-2 py-1 text-xs outline-none focus:border-accent"
            >
              <option value="">{t('config.advanced.default')}</option>
              <option value="true">true</option>
              <option value="false">false</option>
              <option value="omit">omit</option>
            </select>
          </label>
          {triState(
            'include_reasoning_payload',
            t('config.advanced.reasoningPayload'),
          )}
          {choice('reasoning_channel', t('config.advanced.reasoningChannel'), [
            'summary',
            'text',
          ])}
          {choice('reasoning_summary', t('config.advanced.reasoningSummary'), [
            'auto',
            'concise',
            'detailed',
          ])}
          {text('reasoning_scope', t('config.advanced.reasoningScope'))}
          {choice('truncation', t('config.advanced.truncation'), [
            'disabled',
            'auto',
          ])}
          {triState(
            'chat_include_usage',
            t('config.advanced.chatIncludeUsage'),
          )}
          {triState(
            'chat_include_obfuscation',
            t('config.advanced.chatIncludeObfuscation'),
          )}
          {mapField('query', t('config.advanced.query'))}
          {mapField('headers', t('config.advanced.headers'))}
          <KeyValueEditor
            label={t('config.advanced.extraBody')}
            addLabel={t('config.advanced.addEntry')}
            keyPlaceholder={t('config.advanced.entryKey')}
            valuePlaceholder={t('config.advanced.entryValueJson')}
            value={adv.extra_body}
            onChange={(next) => onUpdate('extra_body', next as never)}
          />
        </>
      )}
      {anthropic && (
        <>
          <label className="flex items-center gap-2 pt-4">
            <input
              type="checkbox"
              checked={adv.video_input ?? false}
              onChange={(e) => onUpdate('video_input', e.target.checked)}
              className="accent-accent"
            />
            <span className="text-xs text-dim">
              {t('config.advanced.videoInput')}
            </span>
          </label>
          {text('reasoning_scope', t('config.advanced.reasoningScope'))}
        </>
      )}
      {bytedance && (
        <>
          {text('region', t('config.advanced.region'))}
          {text('project', t('config.advanced.project'))}
          {text('timeout', t('config.advanced.timeout'), '90s')}
          {text('reasoning_scope', t('config.advanced.reasoningScope'))}
          {mapField('query', t('config.advanced.query'))}
          {mapField('headers', t('config.advanced.headers'))}
        </>
      )}
      {minimax && (
        <>
          {text('media_base_url', t('config.advanced.mediaBaseURL'))}
          {text(
            'video_poll_interval_millis',
            t('config.advanced.videoPollInterval'),
            '5000',
          )}
        </>
      )}
    </>
  );

  return (
    <details className="rounded-lg border border-edge bg-panel/40">
      <summary className="flex cursor-pointer list-none items-center gap-1.5 px-3 py-1.5 text-xs text-dim hover:text-fg">
        <ChevronDown size="0.8571rem" />
        {t('config.advanced.title')}
      </summary>
      <div className="grid grid-cols-1 gap-2 px-3 pt-1 pb-3 md:grid-cols-2">
        {fields}
      </div>
    </details>
  );
}

// KeyValueEditor edits one string map (query parameters, headers) as
// rows. Rows are local state so a half-typed key keeps its place; the
// committed record drops blank keys and values, so the writer never
// pins an empty entry.
export function KeyValueEditor({
  label,
  value,
  onChange,
  addLabel,
  keyPlaceholder,
  valuePlaceholder,
}: {
  label: string;
  value?: Record<string, string>;
  onChange: (next: Record<string, string> | undefined) => void;
  addLabel?: string;
  keyPlaceholder?: string;
  valuePlaceholder?: string;
}) {
  const [rows, setRows] = useState<[string, string][]>(() => entriesOf(value));
  // Re-seed when the parent value changes underneath us (loading a
  // saved config, or a managed row being restored).
  const signature = JSON.stringify(value ?? {});
  useEffect(() => {
    setRows(entriesOf(value));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [signature]);

  const commit = (next: [string, string][]) => {
    setRows(next);
    onChange(recordOf(next));
  };

  return (
    <div className="flex flex-col gap-1 md:col-span-2">
      <span className="text-xs text-dim">{label}</span>
      {rows.map(([key, entry], idx) => (
        <div key={idx} className="flex items-center gap-1.5">
          <input
            value={key}
            aria-label={`${label} key ${idx + 1}`}
            placeholder={keyPlaceholder}
            onChange={(e) =>
              commit(
                rows.map((row, i) =>
                  i === idx ? [e.target.value, row[1]] : row,
                ) as [string, string][],
              )
            }
            className="w-1/3 rounded-lg border border-edge bg-panel px-2 py-1 font-mono text-xs outline-none focus:border-accent"
          />
          <input
            value={entry}
            aria-label={`${label} value ${idx + 1}`}
            placeholder={valuePlaceholder}
            onChange={(e) =>
              commit(
                rows.map((row, i) =>
                  i === idx ? [row[0], e.target.value] : row,
                ) as [string, string][],
              )
            }
            className="min-w-0 flex-1 rounded-lg border border-edge bg-panel px-2 py-1 font-mono text-xs outline-none focus:border-accent"
          />
          <button
            type="button"
            aria-label={`${label} remove ${idx + 1}`}
            onClick={() => commit(rows.filter((_, i) => i !== idx))}
            className="rounded-md border border-edge px-1.5 py-0.5 text-xs text-dim hover:text-fg"
          >
            ×
          </button>
        </div>
      ))}
      <button
        type="button"
        onClick={() => setRows([...rows, ['', '']])}
        className="self-start rounded-md border border-edge px-2 py-0.5 text-xs text-dim hover:text-fg"
      >
        {addLabel ?? '+ add'}
      </button>
    </div>
  );
}

function entriesOf(value?: Record<string, string>): [string, string][] {
  if (!value) return [];
  return Object.keys(value)
    .sort()
    .map((key) => [key, value[key]] as [string, string]);
}

function recordOf(
  rows: [string, string][],
): Record<string, string> | undefined {
  const out: Record<string, string> = {};
  for (const [key, value] of rows) {
    const k = key.trim();
    const v = value.trim();
    if (k === '' || v === '') continue;
    out[k] = v;
  }
  return Object.keys(out).length > 0 ? out : undefined;
}
