import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Check,
  ChevronDown,
  ChevronRight,
  Globe,
  Loader2,
  Search,
  X,
} from 'lucide-react';
import { api } from '../lib/api';
import { useStore } from '../lib/store';
import type {
  WebSearchEndpoints,
  WebSearchState,
  WebSearchTestResult,
} from '../lib/types';

// The web search item of the Tools tab. Like the generation tools it is
// one list item that opens a dialog; the dialog configures the
// provider-independent web_search tool. The keyless Parallel/Exa
// hosted MCP endpoints work with no configuration, while Tavily/Brave
// keys (stored in the credential store, never in the config file)
// raise the quota.

const inputClass =
  'w-full rounded-lg border border-edge bg-panel px-2.5 py-1.5 text-xs text-fg ' +
  'outline-none transition-colors hover:border-accent/50 focus:border-accent ' +
  'disabled:opacity-40';

const PROVIDER_LABELS: Record<string, string> = {
  exa: 'Exa MCP',
  parallel: 'Parallel MCP',
  tavily: 'Tavily',
  brave: 'Brave',
};

export function WebSearchSection() {
  const { t } = useTranslation();
  const toast = useStore((s) => s.toast);
  const [state, setState] = useState<WebSearchState | null>(null);
  const [open, setOpen] = useState(false);
  const [enabled, setEnabled] = useState(true);
  const [provider, setProvider] = useState('auto');
  const [maxResults, setMaxResults] = useState(8);
  const [timeout, setTimeoutValue] = useState('15s');
  const [endpoints, setEndpoints] = useState<WebSearchEndpoints>({});
  const [keys, setKeys] = useState<Record<string, string>>({});
  const [clearKeys, setClearKeys] = useState<string[]>([]);
  const [advanced, setAdvanced] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState('');
  const [testQuery, setTestQuery] = useState('');
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<WebSearchTestResult | null>(
    null,
  );
  const [testError, setTestError] = useState('');

  const apply = (next: WebSearchState) => {
    setState(next);
    setEnabled(next.enabled);
    setProvider(next.provider);
    setMaxResults(next.max_results);
    setTimeoutValue(next.timeout);
    setEndpoints(next.endpoints ?? {});
    setKeys({});
    setClearKeys([]);
  };

  useEffect(() => {
    let live = true;
    void api
      .webSearchConfig()
      .then((next) => {
        if (live) apply(next);
      })
      .catch((err) => {
        if (live) setError(String(err));
      });
    return () => {
      live = false;
    };
  }, []);

  const selected = state?.providers.find((p) => p.id === provider);
  const showKeyField =
    provider !== 'auto' && (selected ? !selected.keyless || advanced : false);

  const providerLabel = (id: string): string =>
    id === 'auto'
      ? t('config.webSearchProviderAuto')
      : (PROVIDER_LABELS[id] ?? id);

  const save = async () => {
    setSaving(true);
    setError('');
    try {
      await api.saveWebSearch({
        enabled,
        provider,
        max_results: maxResults,
        timeout,
        endpoints,
        keys,
        clearKeys,
      });
      apply(await api.webSearchConfig());
      setSaved(true);
      toast(t('config.webSearchSaved'), 'info');
      setOpen(false);
    } catch (err) {
      setError(String(err));
    } finally {
      setSaving(false);
    }
  };

  const runTest = async () => {
    if (!testQuery.trim() || provider === 'auto') return;
    setTesting(true);
    setTestError('');
    setTestResult(null);
    try {
      setTestResult(
        await api.testWebSearch({
          provider,
          query: testQuery.trim(),
          apiKey: keys[provider] ?? '',
          endpoint: endpoints[provider as keyof WebSearchEndpoints] ?? '',
        }),
      );
    } catch (err) {
      setTestError(String(err));
    } finally {
      setTesting(false);
    }
  };

  if (state === null && error === '') {
    return (
      <div className="h-16 animate-pulse rounded-xl border border-edge/70 bg-panel/70" />
    );
  }

  return (
    <>
      <ul className="flex flex-col gap-2">
        <li className="[content-visibility:auto] [contain-intrinsic-size:auto_4.5rem] rounded-xl border border-edge bg-panel2 p-3 transition-colors hover:border-accent/40">
          <button
            type="button"
            onClick={() => {
              setSaved(false);
              setOpen(true);
            }}
            className="flex w-full min-w-0 items-center gap-2 text-left"
          >
            <Globe size="0.9286rem" className="shrink-0 text-accent" />
            <span className="min-w-0 flex-1">
              <span className="flex flex-wrap items-center gap-1.5">
                <span className="min-w-0 truncate text-sm font-semibold">
                  {t('config.webSearchTitle')}
                </span>
                <span className="shrink-0 rounded border border-edge bg-panel px-1.5 py-0.5 text-[0.7143rem] text-dim">
                  {providerLabel(state?.provider ?? provider)}
                </span>
                {state && !state.enabled && (
                  <span className="shrink-0 rounded border border-edge bg-panel px-1.5 py-0.5 text-[0.7143rem] text-dim">
                    {t('config.toolsOff')}
                  </span>
                )}
              </span>
              <span className="mt-1 block truncate text-xs text-dim">
                {t('config.webSearchHint')}
              </span>
            </span>
            <ChevronRight size="1.0000rem" className="shrink-0 text-dim" />
          </button>
        </li>
      </ul>

      {open && (
        <div
          className="fixed inset-0 z-[60] grid place-items-center bg-black/60 p-6"
          onClick={() => setOpen(false)}
        >
          <div
            className="flex max-h-[calc(100vh-2rem)] w-[38rem] max-w-full flex-col rounded-2xl border border-edge bg-panel shadow-2xl"
            role="dialog"
            aria-modal="true"
            aria-label={t('config.webSearchTitle')}
            onClick={(e) => e.stopPropagation()}
          >
            <div className="flex shrink-0 items-center justify-between gap-3 border-b border-edge px-4 py-3">
              <div className="flex min-w-0 items-center gap-2">
                <Globe size="1.0714rem" className="shrink-0 text-accent" />
                <h3 className="min-w-0 truncate text-sm font-semibold">
                  {t('config.webSearchTitle')}
                </h3>
              </div>
              <button
                type="button"
                onClick={() => setOpen(false)}
                aria-label={t('tools.close')}
                className="shrink-0 rounded-lg p-1 text-dim hover:bg-panel2 hover:text-fg"
              >
                <X size="1.0000rem" />
              </button>
            </div>

            <div className="min-h-0 flex-1 space-y-3 overflow-y-auto px-4 py-3">
              <p className="text-xs text-dim">{t('config.webSearchHint')}</p>

              <label className="flex items-center gap-2 text-xs text-dim">
                <input
                  type="checkbox"
                  checked={enabled}
                  onChange={(e) => {
                    setEnabled(e.target.checked);
                    setSaved(false);
                  }}
                  className="accent-[var(--color-accent)]"
                />
                {t('config.webSearchEnabled')}
              </label>

              <div className="space-y-1.5">
                <p className="text-xs text-dim">
                  {t('config.webSearchProvider')}
                </p>
                <div className="flex flex-wrap items-center gap-1.5">
                  <button
                    type="button"
                    onClick={() => {
                      setProvider('auto');
                      setTestResult(null);
                      setTestError('');
                    }}
                    className={`rounded-lg border px-2.5 py-1 text-xs transition-colors ${
                      provider === 'auto'
                        ? 'border-accent/60 bg-accent/10 text-accent'
                        : 'border-edge text-dim hover:border-accent/40 hover:text-fg'
                    }`}
                  >
                    {t('config.webSearchProviderAuto')}
                  </button>
                  {(state?.providers ?? []).map((p) => (
                    <button
                      key={p.id}
                      type="button"
                      onClick={() => {
                        setProvider(p.id);
                        setTestResult(null);
                        setTestError('');
                      }}
                      className={`rounded-lg border px-2.5 py-1 text-xs transition-colors ${
                        provider === p.id
                          ? 'border-accent/60 bg-accent/10 text-accent'
                          : 'border-edge text-dim hover:border-accent/40 hover:text-fg'
                      }`}
                    >
                      {providerLabel(p.id)}
                      <span className="ml-1.5 text-[0.7143rem] opacity-70">
                        {p.keyless
                          ? t('config.webSearchKeyless')
                          : t('config.webSearchRequiresKey')}
                      </span>
                    </button>
                  ))}
                </div>
              </div>

              {showKeyField && (
                <div className="space-y-1.5">
                  <label className="text-xs text-dim">
                    {t('config.webSearchKeyLabel')}
                  </label>
                  <div className="flex items-center gap-2">
                    <input
                      type="password"
                      autoComplete="off"
                      disabled={!state?.secrets_available}
                      placeholder={t('config.webSearchKeyPlaceholder')}
                      value={keys[provider] ?? ''}
                      onChange={(e) => {
                        setKeys({ ...keys, [provider]: e.target.value });
                        setSaved(false);
                      }}
                      className={inputClass}
                    />
                    {selected?.key_set && (
                      <button
                        type="button"
                        onClick={() => {
                          setClearKeys([...new Set([...clearKeys, provider])]);
                          setKeys({ ...keys, [provider]: '' });
                        }}
                        className="shrink-0 rounded-lg border border-edge px-2.5 py-1 text-xs text-dim hover:border-err/40 hover:text-err"
                      >
                        {t('config.webSearchClearKey')}
                      </button>
                    )}
                  </div>
                  <p className="text-[0.7143rem] text-dim">
                    {selected?.key_set
                      ? t('config.webSearchKeySet')
                      : t('config.webSearchKeyMissing')}
                  </p>
                </div>
              )}

              <div className="flex flex-wrap items-end gap-3">
                <label className="flex items-center gap-2 text-xs text-dim">
                  {t('config.webSearchMaxResults')}
                  <input
                    type="number"
                    min={1}
                    max={10}
                    value={maxResults}
                    onChange={(e) => {
                      setMaxResults(Number(e.target.value));
                      setSaved(false);
                    }}
                    className="w-16 rounded-lg border border-edge bg-panel px-2 py-1 text-xs text-fg outline-none focus:border-accent"
                  />
                </label>
                <button
                  type="button"
                  onClick={() => setAdvanced(!advanced)}
                  className="flex items-center gap-1 text-xs text-dim hover:text-fg"
                >
                  {advanced ? (
                    <ChevronDown size="0.8571rem" />
                  ) : (
                    <ChevronRight size="0.8571rem" />
                  )}
                  {t('config.webSearchAdvanced')}
                </button>
              </div>

              {advanced && (
                <div className="space-y-2 rounded-lg border border-edge/70 bg-panel/40 p-2.5">
                  <label className="flex items-center gap-2 text-xs text-dim">
                    <span className="w-32 shrink-0">
                      {t('config.webSearchTimeout')}
                    </span>
                    <input
                      value={timeout}
                      onChange={(e) => {
                        setTimeoutValue(e.target.value);
                        setSaved(false);
                      }}
                      className={inputClass}
                    />
                  </label>
                  {provider !== 'auto' && (
                    <label className="flex items-center gap-2 text-xs text-dim">
                      <span className="w-32 shrink-0">
                        {t('config.webSearchEndpoint')}
                      </span>
                      <input
                        placeholder={
                          selected?.endpoint ??
                          t('config.webSearchEndpointPlaceholder')
                        }
                        value={
                          (endpoints[provider as keyof WebSearchEndpoints] as
                            string | undefined) ?? ''
                        }
                        onChange={(e) => {
                          setEndpoints({
                            ...endpoints,
                            [provider]: e.target.value,
                          });
                          setSaved(false);
                        }}
                        className={inputClass}
                      />
                    </label>
                  )}
                  <p className="text-[0.7143rem] text-dim">
                    {t('config.webSearchPrivacy')}
                  </p>
                </div>
              )}

              <div className="space-y-2 rounded-lg border border-edge/70 bg-panel/40 p-2.5">
                <div className="flex flex-wrap items-center gap-2">
                  <input
                    value={testQuery}
                    onChange={(e) => setTestQuery(e.target.value)}
                    placeholder={t('config.webSearchTestPlaceholder')}
                    className={`${inputClass} max-w-64`}
                  />
                  <button
                    type="button"
                    disabled={
                      testing || provider === 'auto' || !testQuery.trim()
                    }
                    onClick={() => void runTest()}
                    className="flex items-center gap-1.5 rounded-lg border border-edge px-3 py-1.5 text-xs text-dim hover:border-accent/40 hover:text-fg disabled:opacity-40"
                  >
                    {testing ? (
                      <Loader2 size="0.8571rem" className="animate-spin" />
                    ) : (
                      <Search size="0.8571rem" />
                    )}
                    {t('config.webSearchTest')}
                  </button>
                </div>
                {provider === 'auto' && (
                  <p className="text-[0.7143rem] text-dim">
                    {t('config.webSearchTestPickProvider')}
                  </p>
                )}
                {testError && <p className="text-xs text-err">{testError}</p>}
                {testResult && (
                  <div className="space-y-1">
                    <p className="text-[0.7143rem] text-dim">
                      {testResult.provider} · {testResult.results.length}{' '}
                      {t('tool.hits')}
                    </p>
                    {testResult.results.map((hit) => (
                      <div key={hit.url} className="min-w-0">
                        <a
                          href={hit.url}
                          target="_blank"
                          rel="noreferrer"
                          className="block truncate text-xs text-accent hover:underline"
                        >
                          {hit.title || hit.url}
                        </a>
                        {hit.snippet && (
                          <p className="truncate text-[0.7143rem] text-dim">
                            {hit.snippet}
                          </p>
                        )}
                      </div>
                    ))}
                    {testResult.results.length === 0 && (
                      <p className="text-xs text-dim">
                        {t('config.webSearchTestEmpty')}
                      </p>
                    )}
                  </div>
                )}
              </div>
            </div>

            <div className="flex shrink-0 items-center justify-between gap-3 border-t border-edge px-4 py-3">
              <span className="flex min-w-0 items-center gap-2 text-xs">
                {saved && (
                  <span className="inline-flex items-center gap-1 text-ok">
                    <Check size="0.8571rem" />
                    {t('config.toolsSaved')}
                  </span>
                )}
                {error !== '' && (
                  <span className="min-w-0 truncate text-err" title={error}>
                    {error}
                  </span>
                )}
              </span>
              <button
                type="button"
                onClick={() => void save()}
                disabled={saving}
                className="flex shrink-0 items-center gap-1.5 rounded-lg bg-accent px-4 py-1.5 text-sm text-white hover:opacity-90 disabled:opacity-40"
              >
                {saving && (
                  <Loader2 size="1.0000rem" className="animate-spin" />
                )}
                {t('setup.saveApply')}
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  );
}
