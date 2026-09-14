import { ChevronDown } from 'lucide-react';
import { Fragment, useEffect, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

import { MenuSelect, type MenuOption } from './MenuSelect';
import type { ProviderAdvanced } from '../lib/types';

// AdvancedSection is the collapsible provider-spec editor on one
// inference instance card. The knobs are the ones flowcraft's drivers
// accept beyond the basic endpoint + models + key flow; which of them
// apply depends on the driver — and, on the OpenAI wire, on the chosen
// API surface — so the panel shows only the fields that driver reads,
// grouped the way its spec documents them. Every field is optional:
// clearing one drops it from the saved spec, so the driver default
// keeps applying (that is what the header hint says).
export function AdvancedSection({
  row,
  driver,
  disabled,
  onUpdate,
}: {
  row: { advanced: ProviderAdvanced; api?: string };
  driver: string;
  // disabled renders the panel read-only: a plugin-owned deployment
  // states its own spec and the host restores it, so editing here would
  // not stick.
  disabled?: boolean;
  onUpdate: (
    key: keyof ProviderAdvanced,
    value: ProviderAdvanced[keyof ProviderAdvanced],
  ) => void;
}) {
  const { t } = useTranslation();
  const adv = row.advanced ?? {};
  const chatSurface = (row.api ?? '').trim() === 'chat';
  const openaiWire = driver === 'openai';
  const anthropic = driver === 'anthropic';
  const bytedance = driver === 'bytedance';
  const minimax = driver === 'minimax';

  const text = (
    key: keyof ProviderAdvanced,
    label: string,
    placeholder?: string,
  ) => (
    <label className="flex flex-col gap-1.5">
      <span className="text-xs text-dim">{label}</span>
      <input
        value={(adv[key] as string | undefined) ?? ''}
        onChange={(e) => onUpdate(key, e.target.value)}
        placeholder={placeholder ?? t('config.advanced.default')}
        disabled={disabled}
        className={inputClass}
      />
    </label>
  );

  // select renders one enum leaf as the page's own dropdown. The value
  // stays the wire token; the option text is the human name for it.
  const select = (
    key: keyof ProviderAdvanced,
    label: string,
    options: MenuOption[],
  ) => (
    <label className="flex flex-col gap-1.5">
      <span className="text-xs text-dim">{label}</span>
      <MenuSelect
        label={label}
        value={(adv[key] as string | undefined) ?? ''}
        options={[
          { value: '', label: t('config.advanced.default') },
          ...options,
        ]}
        placeholder={t('config.advanced.default')}
        onChange={(next) => onUpdate(key, next)}
        disabled={disabled}
      />
    </label>
  );

  // triState edits an optional bool: "" keeps the driver default.
  const triState = (key: keyof ProviderAdvanced, label: string) => {
    const value = adv[key];
    return (
      <label className="flex flex-col gap-1.5">
        <span className="text-xs text-dim">{label}</span>
        <MenuSelect
          label={label}
          value={value === undefined ? '' : String(value)}
          options={[
            { value: '', label: t('config.advanced.default') },
            { value: 'true', label: t('config.advanced.on') },
            { value: 'false', label: t('config.advanced.off') },
          ]}
          placeholder={t('config.advanced.default')}
          onChange={(raw) =>
            onUpdate(key, raw === '' ? ('' as never) : raw === 'true')
          }
          disabled={disabled}
        />
      </label>
    );
  };

  const checkboxRow = (key: keyof ProviderAdvanced, label: string) => (
    <label className="flex items-center gap-2 md:col-span-2">
      <input
        type="checkbox"
        checked={(adv[key] as boolean | undefined) ?? false}
        onChange={(e) => onUpdate(key, e.target.checked)}
        disabled={disabled}
        className="accent-accent"
      />
      <span className="text-xs text-dim">{label}</span>
    </label>
  );

  const mapField = (key: 'query' | 'headers', label: string) => (
    <KeyValueEditor
      label={label}
      addLabel={t('config.advanced.addEntry')}
      keyPlaceholder={t('config.advanced.entryKey')}
      valuePlaceholder={t('config.advanced.entryValue')}
      value={adv[key]}
      disabled={disabled}
      onChange={(next) => onUpdate(key, next as never)}
    />
  );

  // group is one titled block of knobs: the driver's own section
  // (endpoint, auth, transport, ...) with an optional one-line hint.
  const group = (title: string, children: ReactNode, hint?: string) => (
    <section className="flex flex-col gap-2.5">
      <div className="flex items-baseline gap-2">
        <h4 className="text-[0.6923rem] font-medium tracking-wide text-dim/90 uppercase">
          {title}
        </h4>
        {hint !== undefined && (
          <span className="text-[0.6923rem] text-dim/70">{hint}</span>
        )}
      </div>
      <div className="grid grid-cols-1 gap-x-4 gap-y-3 md:grid-cols-2">
        {children}
      </div>
    </section>
  );

  const sections: ReactNode[] = [];
  if (openaiWire) {
    sections.push(
      group(
        t('config.advanced.groupEndpoint'),
        <>
          {select('routing', t('config.advanced.routing'), [
            {
              value: 'azure_deployment',
              label: t('config.advanced.routingAzure'),
            },
          ])}
          {text('organization', t('config.advanced.organization'))}
          {text('project', t('config.advanced.project'))}
          {text('timeout', t('config.advanced.timeout'), '90s')}
        </>,
      ),
      group(
        t('config.advanced.groupAuth'),
        <>
          {select('auth_scheme', t('config.advanced.authScheme'), [
            { value: 'bearer', label: t('config.advanced.authBearer') },
            { value: 'header', label: t('config.advanced.authHeaderOption') },
          ])}
          {text('auth_header', t('config.advanced.authHeader'), 'api-key')}
        </>,
        t('config.advanced.authHint'),
      ),
      group(
        t('config.advanced.groupTransport'),
        <>
          {text('http_retries', t('config.advanced.httpRetries'), '2')}
          {mapField('query', t('config.advanced.query'))}
          {mapField('headers', t('config.advanced.headers'))}
        </>,
      ),
      group(
        t('config.advanced.groupWire'),
        <>
          {select('store', t('config.advanced.store'), [
            { value: 'true', label: t('config.advanced.storeTrue') },
            { value: 'false', label: t('config.advanced.storeFalse') },
            { value: 'omit', label: t('config.advanced.storeOmit') },
          ])}
          {triState(
            'include_reasoning_payload',
            t('config.advanced.reasoningPayload'),
          )}
          {select('reasoning_channel', t('config.advanced.reasoningChannel'), [
            { value: 'summary', label: t('config.advanced.channelSummary') },
            { value: 'text', label: t('config.advanced.channelText') },
          ])}
          {text('reasoning_scope', t('config.advanced.reasoningScope'))}
          {!chatSurface &&
            select('reasoning_summary', t('config.advanced.reasoningSummary'), [
              { value: 'auto', label: t('config.advanced.summaryAuto') },
              { value: 'concise', label: t('config.advanced.summaryConcise') },
              {
                value: 'detailed',
                label: t('config.advanced.summaryDetailed'),
              },
            ])}
          {!chatSurface &&
            select('truncation', t('config.advanced.truncation'), [
              {
                value: 'disabled',
                label: t('config.advanced.truncationFail'),
              },
              { value: 'auto', label: t('config.advanced.truncationDrop') },
            ])}
          {chatSurface &&
            checkboxRow('video_input', t('config.advanced.videoInput'))}
        </>,
      ),
    );
    if (chatSurface) {
      sections.push(
        group(
          t('config.advanced.groupChat'),
          <>
            {triState(
              'chat_include_usage',
              t('config.advanced.chatIncludeUsage'),
            )}
            {triState(
              'chat_include_obfuscation',
              t('config.advanced.chatIncludeObfuscation'),
            )}
          </>,
          t('config.advanced.chatHint'),
        ),
      );
    }
    sections.push(
      group(
        t('config.advanced.groupBody'),
        <>
          {select('metadata_envelope', t('config.advanced.metadata'), [
            {
              value: 'client_metadata',
              label: t('config.advanced.metadataClient'),
            },
            { value: 'metadata', label: t('config.advanced.metadataNative') },
            { value: '-', label: t('config.advanced.metadataOff') },
          ])}
          <KeyValueEditor
            label={t('config.advanced.extraBody')}
            addLabel={t('config.advanced.addEntry')}
            keyPlaceholder={t('config.advanced.entryKey')}
            valuePlaceholder={t('config.advanced.entryValueJson')}
            value={adv.extra_body}
            disabled={disabled}
            onChange={(next) => onUpdate('extra_body', next as never)}
          />
        </>,
      ),
    );
  }
  if (anthropic) {
    sections.push(
      group(
        t('config.advanced.groupWire'),
        <>
          {checkboxRow('video_input', t('config.advanced.videoInput'))}
          {text('reasoning_scope', t('config.advanced.reasoningScope'))}
        </>,
      ),
    );
  }
  if (bytedance) {
    sections.push(
      group(
        t('config.advanced.groupTransport'),
        <>
          {text('region', t('config.advanced.region'))}
          {text('project', t('config.advanced.project'))}
          {text('timeout', t('config.advanced.timeout'), '90s')}
          {text('http_retries', t('config.advanced.httpRetries'), '2')}
          {mapField('query', t('config.advanced.query'))}
          {mapField('headers', t('config.advanced.headers'))}
        </>,
      ),
      group(
        t('config.advanced.groupWire'),
        <>{text('reasoning_scope', t('config.advanced.reasoningScope'))}</>,
      ),
      group(
        t('config.advanced.groupMedia'),
        <>
          {text(
            'video_poll_interval_millis',
            t('config.advanced.videoPollInterval'),
            '5000',
          )}
        </>,
      ),
    );
  }
  if (minimax) {
    sections.push(
      group(
        t('config.advanced.groupMedia'),
        <>
          {text('media_base_url', t('config.advanced.mediaBaseURL'))}
          {text(
            'video_poll_interval_millis',
            t('config.advanced.videoPollInterval'),
            '5000',
          )}
          {text('http_retries', t('config.advanced.httpRetries'), '2')}
        </>,
      ),
    );
  }

  const setCount = countSet(adv);
  return (
    <details
      data-testid="provider-advanced"
      className="group rounded-xl border border-edge bg-panel/30"
    >
      <summary className="flex cursor-pointer list-none items-center gap-2 px-3 py-2 text-xs text-dim hover:text-fg">
        <ChevronDown
          size="0.8571rem"
          className="shrink-0 transition-transform group-open:rotate-180"
        />
        <span className="font-medium">{t('config.advanced.title')}</span>
        {setCount > 0 && (
          <span className="rounded-full bg-panel2 px-1.5 py-0.5 text-[0.6923rem] text-dim">
            {t('config.advanced.setCount', { n: setCount })}
          </span>
        )}
        <span className="ml-auto text-[0.6923rem] text-dim/70">
          {disabled
            ? t('config.advanced.managedHint')
            : t('config.advanced.defaultsHint')}
        </span>
      </summary>
      <div className="flex flex-col gap-3.5 border-t border-edge/60 px-3 pt-3 pb-3.5">
        {sections.length === 0 && (
          <p className="text-xs text-dim/80">{t('config.advanced.noKnobs')}</p>
        )}
        {sections.map((section, index) => (
          <Fragment key={index}>
            {index > 0 && <div className="border-t border-edge/40" />}
            {section}
          </Fragment>
        ))}
      </div>
    </details>
  );
}

const inputClass =
  'h-[1.875rem] w-full rounded-lg border border-edge bg-panel px-2 text-xs text-fg outline-none transition-colors focus:border-accent disabled:opacity-40';

// countSet reports how many provider knobs the row states, so the
// collapsed header can say whether anything is configured at all. A
// stated false counts: it is a deliberate value, not an empty field.
function countSet(adv: ProviderAdvanced): number {
  return Object.values(adv).filter((value) => {
    if (value === undefined || value === null) return false;
    if (typeof value === 'object') return Object.keys(value).length > 0;
    if (typeof value === 'string') return value.trim() !== '';
    return true;
  }).length;
}

// KeyValueEditor edits one string map (query parameters, headers) as
// rows. Rows are local state so a half-typed key keeps its place; the
// committed record drops blank keys and values, so the writer never
// pins an empty entry.
export function KeyValueEditor({
  label,
  value,
  onChange,
  disabled,
  addLabel,
  keyPlaceholder,
  valuePlaceholder,
}: {
  label: string;
  value?: Record<string, string>;
  onChange: (next: Record<string, string> | undefined) => void;
  disabled?: boolean;
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
    <div className="flex flex-col gap-1.5 md:col-span-2">
      <span className="text-xs text-dim">{label}</span>
      {rows.map(([key, entry], idx) => (
        <div key={idx} className="flex items-center gap-1.5">
          <input
            value={key}
            aria-label={`${label} key ${idx + 1}`}
            placeholder={keyPlaceholder}
            disabled={disabled}
            onChange={(e) =>
              commit(
                rows.map((row, i) =>
                  i === idx ? [e.target.value, row[1]] : row,
                ) as [string, string][],
              )
            }
            className="h-[1.875rem] w-2/5 rounded-lg border border-edge bg-panel px-2 font-mono text-xs text-fg outline-none transition-colors focus:border-accent disabled:opacity-40"
          />
          <input
            value={entry}
            aria-label={`${label} value ${idx + 1}`}
            placeholder={valuePlaceholder}
            disabled={disabled}
            onChange={(e) =>
              commit(
                rows.map((row, i) =>
                  i === idx ? [row[0], e.target.value] : row,
                ) as [string, string][],
              )
            }
            className="h-[1.875rem] min-w-0 flex-1 rounded-lg border border-edge bg-panel px-2 font-mono text-xs text-fg outline-none transition-colors focus:border-accent disabled:opacity-40"
          />
          {!disabled && (
            <button
              type="button"
              aria-label={`${label} remove ${idx + 1}`}
              onClick={() => commit(rows.filter((_, i) => i !== idx))}
              className="rounded-md border border-edge px-1.5 py-1 text-xs text-dim transition-colors hover:border-err/60 hover:text-err"
            >
              ×
            </button>
          )}
        </div>
      ))}
      {!disabled && (
        <button
          type="button"
          onClick={() => setRows([...rows, ['', '']])}
          className="self-start rounded-md border border-dashed border-edge px-2 py-1 text-xs text-dim transition-colors hover:border-accent hover:text-fg"
        >
          {addLabel ?? '+ add'}
        </button>
      )}
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
