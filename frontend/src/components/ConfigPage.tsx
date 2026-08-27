import { useEffect, useMemo, useState } from 'react';
import {
  ArrowDown,
  ArrowDownToLine,
  ArrowUp,
  ArrowUpFromLine,
  BarChart3,
  Brain,
  Cpu,
  Database,
  Loader2,
  Palette,
  Plus,
  RefreshCw,
  ScrollText,
  Settings,
  ShieldCheck,
  ShieldPlus,
  Stethoscope,
  Terminal,
  Trash2,
  X,
} from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { api } from '../lib/api';
import { LogViewer } from './LogViewer';
import { useStore } from '../lib/store';
import type {
  CacheClearResult,
  DiagnosticsReport,
  MemorySettings,
  ModelUsageStat,
  PolicyDecision,
  ProviderInstance,
  ProviderView,
  SandboxProbeResult,
  UsagePoint,
} from '../lib/types';
import { UsageChart } from './UsageChart';

// InstanceRow is one editable inference instance in the settings page.
interface RowModel {
  name: string;
  vision: boolean;
  reasoning: string;
  webSearch: boolean;
}

interface InstanceRow {
  id: string; // frontend key
  stableId: string; // persisted identity ("" on newly added rows)
  type: string;
  name: string;
  api: string;
  key: string;
  keySet: boolean;
  keyEnv: boolean;
  models: RowModel[];
  endpoint: string;
  enabled: boolean;
}

type Tab =
  | 'ui'
  | 'inference'
  | 'usage'
  | 'memory'
  | 'permissions'
  | 'logs'
  | 'diagnostics';

export function ConfigPage() {
  const configured = useStore((s) => s.configured);
  const closeConfig = useStore((s) => s.closeConfig);
  const toast = useStore((s) => s.toast);
  const theme = useStore((s) => s.theme);
  const setTheme = useStore((s) => s.setTheme);
  const newID = () =>
    `mcp-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
  const { t, i18n } = useTranslation();
  const lang = i18n.resolvedLanguage?.startsWith('zh') ? 'zh' : 'en';

  const [tab, setTab] = useState<Tab>('inference');
  const [rows, setRows] = useState<InstanceRow[]>([]);
  const [catalog, setCatalog] = useState<ProviderView[]>([]);
  const [newType, setNewType] = useState('deepseek');
  const [defaultModel, setDefaultModel] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const [rules, setRules] = useState<string[]>([]);
  const [ruleInput, setRuleInput] = useState('');
  const [memory, setMemory] = useState<MemorySettings>({
    max_raw_messages: 36,
    preserve_recent: 4,
    max_summary_bytes: 4096,
    replay_full_history: false,
  });
  const [memorySaving, setMemorySaving] = useState(false);
  const [diag, setDiag] = useState<DiagnosticsReport | null>(null);
  const [probe, setProbe] = useState<SandboxProbeResult | null>(null);
  const [policyInput, setPolicyInput] = useState('');
  const [policy, setPolicy] = useState<PolicyDecision | null>(null);
  const [cacheResult, setCacheResult] = useState<CacheClearResult | null>(null);
  const [diagBusy, setDiagBusy] = useState(false);
  const [usageRows, setUsageRows] = useState<ModelUsageStat[]>([]);
  const [usageError, setUsageError] = useState('');
  const [usageModel, setUsageModel] = useState('');
  const [usageRange, setUsageRange] = useState<
    'today' | '1d' | '7d' | '14d' | '30d'
  >('7d');
  const [usageSeries, setUsageSeries] = useState<UsagePoint[]>([]);
  const [usageStartMs, setUsageStartMs] = useState(0);
  const [usageEndMs, setUsageEndMs] = useState(0);
  const [usageLoading, setUsageLoading] = useState(false);
  const [usageReload, setUsageReload] = useState(0);
  const usageTotals = useMemo(() => {
    const t0 = { input: 0, output: 0, cache: 0, reasoning: 0, sessions: 0 };
    for (const r of usageRows) {
      t0.input += r.input_tokens;
      t0.output += r.output_tokens;
      t0.cache += r.cache_read_tokens;
      t0.reasoning += r.reasoning_tokens;
      t0.sessions += r.sessions;
    }
    return t0;
  }, [usageRows]);

  useEffect(() => {
    void (async () => {
      try {
        const [providers, state] = await Promise.all([
          api.providers(),
          api.configState(),
        ]);
        setCatalog(providers);
        const byType = new Map(providers.map((p) => [p.id, p]));
        setRows(
          (state.instances ?? []).map((s) => {
            const models = (s.models ?? []).map((m) => ({
              name: m.name ?? '',
              vision: m.vision ?? false,
              reasoning: m.reasoning ?? '',
              webSearch: m.web_search ?? false,
            }));
            return {
              id: newID(),
              stableId: s.stable_id ?? '',
              type: s.type,
              name: s.name ?? '',
              api: s.api ?? '',
              key: s.key ?? '',
              keySet: s.key_set ?? false,
              keyEnv: s.key_env ?? false,
              models:
                models.length > 0
                  ? models
                  : [
                      {
                        name: byType.get(s.type)?.default_model ?? '',
                        vision: false,
                        reasoning: '',
                        webSearch: false,
                      },
                    ],
              endpoint: s.endpoint ?? '',
              enabled: s.enabled ?? true,
            };
          }),
        );
        setDefaultModel(state.model);
      } catch (err) {
        setError(String(err));
      }
    })();
  }, []);

  useEffect(() => {
    if (tab !== 'permissions') return;
    void api
      .permissions()
      .then(setRules)
      .catch((err) => setError(String(err)));
  }, [tab]);

  useEffect(() => {
    if (tab !== 'memory') return;
    void api
      .memoryConfig()
      .then(setMemory)
      .catch((err) => setError(String(err)));
  }, [tab]);

  useEffect(() => {
    if (tab !== 'diagnostics') return;
    void api
      .diagnostics()
      .then(setDiag)
      .catch((err) => setError(String(err)));
  }, [tab]);

  const runProbe = async () => {
    setDiagBusy(true);
    try {
      setProbe(await api.runSandboxProbe());
    } catch (err) {
      setError(String(err));
    } finally {
      setDiagBusy(false);
    }
  };

  const checkPolicy = async () => {
    if (!policyInput.trim()) return;
    setDiagBusy(true);
    try {
      setPolicy(await api.evaluateCommandPolicy(policyInput.trim()));
    } catch (err) {
      setError(String(err));
    } finally {
      setDiagBusy(false);
    }
  };

  const clearCaches = async () => {
    setDiagBusy(true);
    try {
      setCacheResult(await api.clearCaches());
    } catch (err) {
      setError(String(err));
    } finally {
      setDiagBusy(false);
    }
  };

  const saveMemory = async () => {
    setMemorySaving(true);
    try {
      await api.saveMemory(memory);
      setError('');
    } catch (err) {
      setError(String(err));
    } finally {
      setMemorySaving(false);
    }
  };

  useEffect(() => {
    if (tab !== 'usage') return;
    void api
      .modelUsage()
      .then(setUsageRows)
      .catch((err) => setUsageError(String(err)));
  }, [tab]);

  // Default the chart to the most-used model once the summary loads.
  useEffect(() => {
    if (!usageModel && usageRows.length > 0) {
      setUsageModel(usageRows[0].model);
    }
  }, [usageRows, usageModel]);

  // Resolve the selected preset into a live [start, end) window the
  // same way cc-switch does: "today" and multi-day presets start at
  // local midnight, "1d" is the rolling 24h, and the end is always now.
  const resolveUsageRange = (
    preset: 'today' | '1d' | '7d' | '14d' | '30d',
  ): { startMs: number; endMs: number } => {
    const endMs = Date.now();
    const DAY = 86_400_000;
    if (preset === 'today') {
      const d = new Date(endMs);
      d.setHours(0, 0, 0, 0);
      return { startMs: d.getTime(), endMs };
    }
    if (preset === '1d') {
      return { startMs: endMs - DAY, endMs };
    }
    const days = preset === '7d' ? 7 : preset === '14d' ? 14 : 30;
    const d = new Date(endMs - (days - 1) * DAY);
    d.setHours(0, 0, 0, 0);
    return { startMs: d.getTime(), endMs };
  };

  // Load the selected model's time series whenever the model, range,
  // tab, or an explicit refresh changes. Granularity follows the range
  // duration like cc-switch: <= 24h buckets hourly, longer ranges
  // bucket by local day.
  useEffect(() => {
    if (tab !== 'usage' || !usageModel) {
      setUsageSeries([]);
      return;
    }
    let cancelled = false;
    setUsageLoading(true);
    const { startMs, endMs } = resolveUsageRange(usageRange);
    const granularity: 'hour' | 'day' =
      endMs - startMs <= 24 * 3_600_000 ? 'hour' : 'day';
    setUsageStartMs(startMs);
    setUsageEndMs(endMs);
    void api
      .modelUsageSeries(
        usageModel,
        granularity,
        -new Date().getTimezoneOffset(),
        new Date(startMs).toISOString(),
        new Date(endMs).toISOString(),
      )
      .then((pts) => {
        if (!cancelled) setUsageSeries(pts);
      })
      .catch((err) => {
        if (!cancelled) setUsageError(String(err));
      })
      .finally(() => {
        if (!cancelled) setUsageLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [tab, usageModel, usageRange, usageReload]);

  // Cache hit rate for the currently selected model + time range,
  // derived from the same series the chart renders. cc-switch's formula
  // (cache_read / fresh_input + cache_write + cache_read) reduces to
  // cache_read / input for input-inclusive providers (DeepSeek/OpenAI).
  const cacheHit = useMemo(() => {
    let input = 0;
    let read = 0;
    for (const p of usageSeries) {
      input += p.input_tokens;
      read += p.cache_read_tokens;
    }
    const rate =
      input > 0 ? Math.min(100, Math.max(0, (read / input) * 100)) : 0;
    return { input, read, rate };
  }, [usageSeries]);

  // enabledRows are the instances that participate in the router, in
  // priority order; only they can be reordered.
  const enabledRows = useMemo(() => rows.filter((r) => r.enabled), [rows]);

  const addInstance = (type: string) => {
    const prov = catalog.find((p) => p.id === type);
    setRows((prev) => [
      ...prev,
      {
        id: newID(),
        stableId: '',
        type,
        name: '',
        api: prov?.api ?? '',
        key: '',
        keySet: false,
        keyEnv: false,
        models: [
          {
            name: prov?.default_model ?? '',
            vision: false,
            reasoning: '',
            webSearch: false,
          },
        ],
        endpoint: '',
        enabled: true,
      },
    ]);
  };

  const move = (idx: number, dir: -1 | 1) => {
    const target = idx + dir;
    if (target < 0 || target >= enabledRows.length) return;
    const a = enabledRows[idx].id;
    const b = enabledRows[target].id;
    setRows((prev) => {
      const next = [...prev];
      const ia = next.findIndex((r) => r.id === a);
      const ib = next.findIndex((r) => r.id === b);
      if (ia < 0 || ib < 0) return prev;
      [next[ia], next[ib]] = [next[ib], next[ia]];
      return next;
    });
  };

  const update = (id: string, patch: Partial<InstanceRow>) => {
    setRows((prev) => prev.map((r) => (r.id === id ? { ...r, ...patch } : r)));
  };

  const updateModel = (id: string, idx: number, patch: Partial<RowModel>) => {
    setRows((prev) =>
      prev.map((r) =>
        r.id === id
          ? {
              ...r,
              models: r.models.map((m, i) =>
                i === idx ? { ...m, ...patch } : m,
              ),
            }
          : r,
      ),
    );
  };

  const addModel = (id: string) => {
    setRows((prev) =>
      prev.map((r) =>
        r.id === id
          ? {
              ...r,
              models: [
                ...r.models,
                { name: '', vision: false, reasoning: '', webSearch: false },
              ],
            }
          : r,
      ),
    );
  };

  const removeModel = (id: string, idx: number) => {
    setRows((prev) =>
      prev.map((r) =>
        r.id === id
          ? { ...r, models: r.models.filter((_, i) => i !== idx) }
          : r,
      ),
    );
  };

  const save = async () => {
    setError('');
    if (enabledRows.length === 0) {
      setError(t('setup.selectProvider'));
      return;
    }
    const instances: ProviderInstance[] = rows.map((r) => ({
      stable_id: r.stableId,
      type: r.type,
      name: r.name,
      api: r.api,
      key: r.key,
      key_set: r.keySet,
      key_env: r.keyEnv,
      models: r.models.map((m) => ({
        name: m.name,
        vision: m.vision,
        reasoning: m.reasoning,
        web_search: m.webSearch,
      })),
      endpoint: r.endpoint,
      enabled: r.enabled,
    }));
    setSaving(true);
    try {
      await api.saveInstances({ instances });
      toast(t('config.saved'));
      closeConfig();
    } catch (err) {
      setError(String(err));
    } finally {
      setSaving(false);
    }
  };

  const fmtUsageTokens = (n: number) => {
    if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`;
    if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
    return String(n);
  };

  const fmtUsageTime = (iso: string) => {
    if (!iso) return '';
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return '';
    const diff = Date.now() - d.getTime();
    if (diff < 60_000) return t('sidebar.justNow');
    if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m`;
    if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h`;
    return d.toLocaleDateString();
  };

  const tabs: { id: Tab; label: string; icon: LucideIcon }[] = [
    { id: 'ui', label: t('config.tabUi'), icon: Palette },
    { id: 'inference', label: t('config.tabInference'), icon: Cpu },
    { id: 'usage', label: t('config.tabUsage'), icon: BarChart3 },
    { id: 'memory', label: t('config.tabMemory'), icon: Database },
    { id: 'permissions', label: t('config.tabPermissions'), icon: ShieldCheck },
    { id: 'logs', label: t('config.tabLogs'), icon: ScrollText },
    { id: 'diagnostics', label: t('config.tabDiagnostics'), icon: Stethoscope },
  ];

  return (
    <div className="fixed inset-x-0 bottom-0 top-11 z-50 bg-black/70 grid place-items-center">
      <div className="w-[720px] h-[620px] flex flex-col rounded-2xl border border-edge bg-panel shadow-2xl">
        <div className="flex items-center gap-4 px-5 py-4 border-b border-edge">
          <Settings size={18} className="text-accent" />
          <h2 className="text-base font-semibold">{t('config.title')}</h2>
          <span className="flex-1" />
          {configured && (
            <button
              onClick={closeConfig}
              className="text-dim hover:text-fg"
              aria-label={t('tools.close')}
            >
              <X size={18} />
            </button>
          )}
        </div>

        <div className="flex items-center gap-1 overflow-x-auto border-b border-edge px-4">
          {tabs.map((tb) => {
            const Icon = tb.icon;
            return (
              <button
                key={tb.id}
                onClick={() => {
                  setTab(tb.id);
                  setError('');
                }}
                className={`flex shrink-0 items-center gap-1.5 rounded-lg px-3 py-1.5 text-sm transition-colors ${
                  tab === tb.id
                    ? 'bg-accent/15 text-accent'
                    : 'text-dim hover:bg-panel2 hover:text-fg'
                }`}
              >
                <Icon size={14} />
                {tb.label}
              </button>
            );
          })}
        </div>

        <div className="flex-1 overflow-y-auto px-5 py-4">
          {tab === 'ui' && (
            <div>
              <div className="text-sm mb-3">{t('config.uiLanguage')}</div>
              <div className="flex rounded-lg border border-edge overflow-hidden w-fit text-sm">
                <button
                  onClick={() => void i18n.changeLanguage('zh')}
                  className={`px-3 py-1.5 ${
                    lang === 'zh'
                      ? 'bg-accent text-white'
                      : 'text-dim hover:text-fg'
                  }`}
                >
                  中文
                </button>
                <button
                  onClick={() => void i18n.changeLanguage('en')}
                  className={`px-3 py-1.5 ${
                    lang === 'en'
                      ? 'bg-accent text-white'
                      : 'text-dim hover:text-fg'
                  }`}
                >
                  English
                </button>
              </div>
              <div className="text-sm mb-3 mt-5">{t('config.uiTheme')}</div>
              <div className="flex rounded-lg border border-edge overflow-hidden w-fit text-sm">
                <button
                  onClick={() => setTheme('dark')}
                  className={`px-3 py-1.5 ${
                    theme === 'dark'
                      ? 'bg-accent text-white'
                      : 'text-dim hover:text-fg'
                  }`}
                >
                  {t('config.uiThemeDark')}
                </button>
                <button
                  onClick={() => setTheme('light')}
                  className={`px-3 py-1.5 ${
                    theme === 'light'
                      ? 'bg-accent text-white'
                      : 'text-dim hover:text-fg'
                  }`}
                >
                  {t('config.uiThemeLight')}
                </button>
              </div>
            </div>
          )}

          {tab === 'inference' && (
            <div className="space-y-3">
              <p className="text-xs text-dim">
                {defaultModel
                  ? t('config.inferenceCurrent', { model: defaultModel })
                  : t('setup.subtitle')}
              </p>
              <div className="flex items-center gap-2">
                <select
                  value={newType}
                  onChange={(e) => setNewType(e.target.value)}
                  className="rounded-lg border border-edge bg-panel px-2 py-1.5 text-sm outline-none"
                >
                  {catalog.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                    </option>
                  ))}
                </select>
                <button
                  onClick={() => addInstance(newType)}
                  className="flex items-center gap-1.5 rounded-lg border border-edge px-3 py-1.5 text-sm text-dim hover:text-fg"
                >
                  <Plus size={14} />
                  {t('config.addInstance')}
                </button>
              </div>
              {rows.length === 0 && (
                <p className="text-sm text-dim">{t('config.instancesEmpty')}</p>
              )}
              {rows.map((row) => {
                const prov = catalog.find((p) => p.id === row.type);
                return (
                  <div
                    key={row.id}
                    className={`rounded-xl border overflow-hidden ${
                      row.enabled
                        ? 'border-edge bg-panel2'
                        : 'border-edge/50 bg-panel2/50'
                    }`}
                  >
                    <div className="flex items-center gap-2.5 px-4 py-2.5">
                      <input
                        type="checkbox"
                        checked={row.enabled}
                        onChange={(e) =>
                          update(row.id, { enabled: e.target.checked })
                        }
                        className="accent-[var(--color-accent)]"
                        title={t('config.instanceEnabled')}
                      />
                      <span className="font-medium text-sm shrink-0">
                        {prov?.name ?? row.type}
                      </span>
                      <input
                        value={row.name}
                        onChange={(e) =>
                          update(row.id, { name: e.target.value })
                        }
                        placeholder={t('config.instanceName')}
                        className="flex-1 min-w-0 rounded-lg border border-edge bg-panel px-2 py-1 text-sm outline-none focus:border-accent"
                      />
                      <button
                        onClick={() =>
                          setRows((prev) => prev.filter((r) => r.id !== row.id))
                        }
                        className="text-dim hover:text-err shrink-0"
                        title={t('config.removeInstance')}
                      >
                        <Trash2 size={14} />
                      </button>
                    </div>
                    {row.enabled && (
                      <div className="px-4 pb-3 pt-1 space-y-2">
                        <div className="space-y-2">
                          <div className="flex items-center justify-between">
                            <span className="text-xs font-medium text-dim">
                              {t('config.inferenceBase')}
                            </span>
                          </div>
                          <input
                            value={row.endpoint}
                            onChange={(e) =>
                              update(row.id, { endpoint: e.target.value })
                            }
                            placeholder={t('setup.endpointPlaceholder')}
                            className="w-full rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
                          />
                          <div className="flex items-center gap-2 text-xs text-dim">
                            {t('setup.apiMode')}
                            <select
                              value={row.api}
                              onChange={(e) =>
                                update(row.id, { api: e.target.value })
                              }
                              className="rounded border border-edge bg-panel px-2 py-1 outline-none"
                            >
                              <option value="responses">responses</option>
                              <option value="chat">chat</option>
                            </select>
                          </div>
                        </div>
                        <div className="space-y-2 pt-1">
                          <div className="flex items-center justify-between">
                            <span className="text-xs font-medium text-dim">
                              {t('setup.models')}
                            </span>
                            <button
                              onClick={() => addModel(row.id)}
                              className="flex items-center gap-1 text-xs text-dim hover:text-fg"
                            >
                              <Plus size={12} />
                              {t('config.addModel')}
                            </button>
                          </div>
                          {row.models.map((m, mi) => (
                            <div
                              key={mi}
                              className="space-y-2 rounded-lg border border-edge bg-panel p-2.5"
                            >
                              <div className="flex items-center gap-2">
                                <input
                                  value={m.name}
                                  onChange={(e) =>
                                    updateModel(row.id, mi, {
                                      name: e.target.value,
                                    })
                                  }
                                  placeholder={t('setup.model')}
                                  className="flex-1 min-w-36 rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
                                />
                                <button
                                  onClick={() => removeModel(row.id, mi)}
                                  className="shrink-0 text-dim hover:text-err"
                                  title={t('config.removeModel')}
                                  aria-label={t('config.removeModel')}
                                >
                                  <X size={14} />
                                </button>
                              </div>
                              <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5 text-xs text-dim">
                                <label className="flex items-center gap-1.5 whitespace-nowrap">
                                  <input
                                    type="checkbox"
                                    checked={m.vision}
                                    onChange={(e) =>
                                      updateModel(row.id, mi, {
                                        vision: e.target.checked,
                                      })
                                    }
                                    className="accent-[var(--color-accent)]"
                                  />
                                  {t('setup.vision')}
                                </label>
                                <label className="flex items-center gap-1.5 whitespace-nowrap">
                                  {t('setup.reasoning')}
                                  <select
                                    value={m.reasoning}
                                    onChange={(e) =>
                                      updateModel(row.id, mi, {
                                        reasoning: e.target.value,
                                      })
                                    }
                                    className="rounded border border-edge bg-panel px-2 py-1 outline-none"
                                  >
                                    <option value="">
                                      {t('setup.reasoningOff')}
                                    </option>
                                    <option value="always">always</option>
                                    <option value="toggle">toggle</option>
                                  </select>
                                </label>
                                <label className="flex items-center gap-1.5 whitespace-nowrap">
                                  <input
                                    type="checkbox"
                                    checked={m.webSearch}
                                    onChange={(e) =>
                                      updateModel(row.id, mi, {
                                        webSearch: e.target.checked,
                                      })
                                    }
                                    className="accent-[var(--color-accent)]"
                                  />
                                  {t('setup.webSearch')}
                                </label>
                              </div>
                            </div>
                          ))}
                        </div>
                        <div className="space-y-2 pt-1">
                          <span className="text-xs font-medium text-dim">
                            {t('config.inferenceKey')}
                          </span>
                          <div className="flex items-center gap-2">
                            <input
                              type="password"
                              value={row.key}
                              onChange={(e) =>
                                update(row.id, { key: e.target.value })
                              }
                              disabled={row.keyEnv}
                              placeholder={
                                row.keySet && row.key === ''
                                  ? t('setup.apiKeySet')
                                  : t('setup.apiKeyPlaceholder', {
                                      var: prov?.env_var ?? '',
                                    })
                              }
                              className="flex-1 rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent disabled:opacity-40"
                            />
                            <label className="flex items-center gap-1.5 text-xs text-dim whitespace-nowrap">
                              <input
                                type="checkbox"
                                checked={row.keyEnv}
                                onChange={(e) =>
                                  update(row.id, { keyEnv: e.target.checked })
                                }
                                className="accent-[var(--color-accent)]"
                              />
                              {t('setup.envVar', { var: prov?.env_var ?? '' })}
                            </label>
                          </div>
                        </div>
                      </div>
                    )}
                  </div>
                );
              })}

              {enabledRows.length > 1 && (
                <div className="rounded-xl border border-edge bg-panel2 p-3">
                  <div className="text-xs text-dim mb-2">
                    {t('setup.routerPriority')}
                  </div>
                  <div className="space-y-1.5">
                    {enabledRows.map((row, idx) => (
                      <div
                        key={row.id}
                        className="flex items-center gap-2 rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm"
                      >
                        <span className="text-xs text-dim w-5">{idx + 1}</span>
                        <span className="flex-1">
                          {catalog.find((p) => p.id === row.type)?.name ??
                            row.type}
                          {row.name ? ` · ${row.name}` : ''}
                        </span>
                        <span className="text-xs text-dim truncate">
                          {row.models
                            .map((m) => m.name)
                            .filter(Boolean)
                            .join(', ')}
                        </span>
                        <button
                          onClick={() => move(idx, -1)}
                          disabled={idx === 0}
                          className="text-dim hover:text-fg disabled:opacity-30"
                          title={t('config.moveUp')}
                          aria-label={t('config.moveUp')}
                        >
                          <ArrowUp size={14} />
                        </button>
                        <button
                          onClick={() => move(idx, 1)}
                          disabled={idx === enabledRows.length - 1}
                          className="text-dim hover:text-fg disabled:opacity-30"
                          title={t('config.moveDown')}
                          aria-label={t('config.moveDown')}
                        >
                          <ArrowDown size={14} />
                        </button>
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </div>
          )}

          {tab === 'usage' && (
            <div className="space-y-3">
              <div className="flex items-center justify-between">
                <p className="text-xs text-dim">{t('config.usageHint')}</p>
                <button
                  onClick={() => {
                    void api
                      .modelUsage()
                      .then(setUsageRows)
                      .catch((err) => setUsageError(String(err)));
                    setUsageReload((n) => n + 1);
                  }}
                  disabled={usageLoading}
                  className="flex items-center gap-1.5 rounded-lg border border-edge px-2.5 py-1.5 text-xs text-dim transition-colors hover:text-fg disabled:opacity-50"
                  aria-label={t('config.logsRefresh')}
                >
                  {usageLoading ? (
                    <Loader2 size={13} className="animate-spin" />
                  ) : (
                    <RefreshCw size={13} />
                  )}
                  {t('config.logsRefresh')}
                </button>
              </div>
              {usageError && <p className="text-xs text-err">{usageError}</p>}
              {usageLoading && usageRows.length === 0 ? (
                <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
                  {[0, 1, 2, 3].map((i) => (
                    <div
                      key={i}
                      className="h-[72px] animate-pulse rounded-xl bg-panel2"
                    />
                  ))}
                </div>
              ) : (
                <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
                  <div className="rounded-xl border border-edge bg-panel2 p-3">
                    <div className="flex items-center gap-1.5 text-xs text-dim">
                      <ArrowDownToLine size={13} className="text-accent" />
                      {t('config.usageInput')}
                    </div>
                    <p className="mt-1.5 text-lg font-semibold tabular-nums text-accent">
                      {fmtUsageTokens(usageTotals.input)}
                    </p>
                  </div>
                  <div className="rounded-xl border border-edge bg-panel2 p-3">
                    <div className="flex items-center gap-1.5 text-xs text-dim">
                      <ArrowUpFromLine size={13} className="text-ok" />
                      {t('config.usageOutput')}
                    </div>
                    <p className="mt-1.5 text-lg font-semibold tabular-nums text-ok">
                      {fmtUsageTokens(usageTotals.output)}
                    </p>
                  </div>
                  <div className="rounded-xl border border-edge bg-panel2 p-3">
                    <div className="flex items-center gap-1.5 text-xs text-dim">
                      <Database size={13} className="text-subagent" />
                      {t('config.usageCache')}
                    </div>
                    <p className="mt-1.5 text-lg font-semibold tabular-nums text-subagent">
                      {fmtUsageTokens(usageTotals.cache)}
                    </p>
                  </div>
                  <div className="rounded-xl border border-edge bg-panel2 p-3">
                    <div className="flex items-center gap-1.5 text-xs text-dim">
                      <Brain size={13} className="text-warn" />
                      {t('config.usageReasoning')}
                    </div>
                    <p className="mt-1.5 text-lg font-semibold tabular-nums text-warn">
                      {fmtUsageTokens(usageTotals.reasoning)}
                    </p>
                  </div>
                </div>
              )}
              {usageRows.length === 0 ? (
                <p className="text-sm text-dim">{t('config.usageEmpty')}</p>
              ) : (
                <>
                  <div className="flex flex-wrap items-center gap-2">
                    <select
                      value={usageModel}
                      onChange={(e) => setUsageModel(e.target.value)}
                      className="max-w-[340px] rounded-lg border border-edge bg-panel px-2 py-1.5 text-xs font-mono outline-none"
                      title={t('config.usageModel')}
                    >
                      {usageRows.map((r) => (
                        <option key={r.model} value={r.model}>
                          {r.model}
                        </option>
                      ))}
                    </select>
                    {(
                      [
                        ['today', t('config.usageRangeToday')],
                        ['1d', '1d'],
                        ['7d', '7d'],
                        ['14d', '14d'],
                        ['30d', '30d'],
                      ] as const
                    ).map(([value, label]) => (
                      <button
                        key={value}
                        onClick={() => setUsageRange(value)}
                        className={`rounded-lg border px-2.5 py-1.5 text-xs ${
                          usageRange === value
                            ? 'border-accent bg-accent/15 text-fg'
                            : 'border-edge text-dim hover:text-fg'
                        }`}
                      >
                        {label}
                      </button>
                    ))}
                    {usageLoading && (
                      <Loader2 size={14} className="animate-spin text-dim" />
                    )}
                  </div>
                  <div
                    className="rounded-xl border border-edge bg-panel2 p-3"
                    title={t('config.usageCacheHitHint')}
                  >
                    <div className="mb-2 flex items-center justify-between text-xs">
                      <span className="text-dim">
                        {t('config.usageCacheHitRate')}
                      </span>
                      <span className="font-bold text-ok tabular-nums">
                        {cacheHit.rate.toFixed(1)}%
                      </span>
                    </div>
                    <div className="h-1.5 overflow-hidden rounded-full bg-panel">
                      <div
                        className="h-full rounded-full bg-ok transition-all duration-300"
                        style={{ width: `${cacheHit.rate}%` }}
                      />
                    </div>
                    <div className="mt-1.5 text-[10px] text-dim tabular-nums">
                      {fmtUsageTokens(cacheHit.read)} /{' '}
                      {fmtUsageTokens(cacheHit.input)}
                    </div>
                  </div>
                  <UsageChart
                    points={usageSeries}
                    granularity={
                      usageEndMs - usageStartMs <= 24 * 3_600_000
                        ? 'hour'
                        : 'day'
                    }
                    startMs={usageStartMs}
                    endMs={usageEndMs}
                    rangeLabel={
                      usageRange === 'today'
                        ? t('config.usageRangeToday')
                        : usageRange
                    }
                  />
                  <div className="rounded-xl border border-edge bg-panel2 overflow-x-auto">
                    <table className="w-full text-xs">
                      <thead>
                        <tr className="text-left text-dim border-b border-edge">
                          <th className="px-3 py-2 font-medium">
                            {t('config.usageModel')}
                          </th>
                          <th className="px-3 py-2 font-medium text-right">
                            {t('config.usageInput')}
                          </th>
                          <th className="px-3 py-2 font-medium text-right">
                            {t('config.usageOutput')}
                          </th>
                          <th className="px-3 py-2 font-medium text-right">
                            {t('config.usageCache')}
                          </th>
                          <th className="px-3 py-2 font-medium text-right">
                            {t('config.usageReasoning')}
                          </th>
                          <th className="px-3 py-2 font-medium text-right">
                            {t('config.usageLatency')}
                          </th>
                          <th className="px-3 py-2 font-medium text-right">
                            {t('config.usageSessions')}
                          </th>
                          <th className="px-3 py-2 font-medium text-right">
                            {t('config.usageUpdated')}
                          </th>
                        </tr>
                      </thead>
                      <tbody>
                        {usageRows.map((r) => (
                          <tr
                            key={r.model}
                            className="border-b border-edge/50 last:border-0 hover:bg-panel2"
                          >
                            <td className="px-3 py-2 font-mono text-fg">
                              {r.model}
                            </td>
                            <td className="px-3 py-2 text-right tabular-nums">
                              {fmtUsageTokens(r.input_tokens)}
                            </td>
                            <td className="px-3 py-2 text-right tabular-nums">
                              {fmtUsageTokens(r.output_tokens)}
                            </td>
                            <td
                              className={`px-3 py-2 text-right tabular-nums ${
                                r.cache_read_tokens === 0 ? 'text-dim/60' : ''
                              }`}
                            >
                              {fmtUsageTokens(r.cache_read_tokens)}
                            </td>
                            <td
                              className={`px-3 py-2 text-right tabular-nums ${
                                r.reasoning_tokens === 0 ? 'text-dim/60' : ''
                              }`}
                            >
                              {fmtUsageTokens(r.reasoning_tokens)}
                            </td>
                            <td
                              className={`px-3 py-2 text-right tabular-nums ${
                                r.latency_ms === 0 ? 'text-dim/60' : ''
                              }`}
                            >
                              {fmtUsageTokens(r.latency_ms)}ms
                            </td>
                            <td className="px-3 py-2 text-right tabular-nums">
                              {r.sessions}
                            </td>
                            <td className="px-3 py-2 text-right text-dim">
                              {fmtUsageTime(r.updated_at)}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </>
              )}
            </div>
          )}

          {tab === 'memory' && (
            <div className="space-y-4">
              <p className="text-xs text-dim">{t('config.memoryHint')}</p>
              <div className="grid grid-cols-3 gap-3">
                <label className="space-y-1.5">
                  <span className="text-xs text-dim">
                    <span title="max_raw_messages">
                      {t('config.memoryRawWindow')}
                    </span>
                  </span>
                  <input
                    type="number"
                    min={0}
                    value={memory.max_raw_messages}
                    onChange={(e) =>
                      setMemory((m) => ({
                        ...m,
                        max_raw_messages: Number(e.target.value) || 0,
                      }))
                    }
                    className="w-full rounded-lg border border-edge bg-panel2 px-3 py-1.5 text-sm outline-none focus:border-accent"
                  />
                </label>
                <label className="space-y-1.5">
                  <span className="text-xs text-dim">
                    <span title="preserve_recent">
                      {t('config.memoryPreserveRecent')}
                    </span>
                  </span>
                  <input
                    type="number"
                    min={0}
                    value={memory.preserve_recent}
                    onChange={(e) =>
                      setMemory((m) => ({
                        ...m,
                        preserve_recent: Number(e.target.value) || 0,
                      }))
                    }
                    className="w-full rounded-lg border border-edge bg-panel2 px-3 py-1.5 text-sm outline-none focus:border-accent"
                  />
                </label>
                <label className="space-y-1.5">
                  <span className="text-xs text-dim">
                    <span title="max_summary_bytes">
                      {t('config.memorySummaryBytes')}
                    </span>
                  </span>
                  <input
                    type="number"
                    min={0}
                    step={1024}
                    value={memory.max_summary_bytes}
                    onChange={(e) =>
                      setMemory((m) => ({
                        ...m,
                        max_summary_bytes: Number(e.target.value) || 0,
                      }))
                    }
                    className="w-full rounded-lg border border-edge bg-panel2 px-3 py-1.5 text-sm outline-none focus:border-accent"
                  />
                </label>
              </div>
              <label className="flex items-center gap-2 rounded-lg border border-edge bg-panel2 px-3 py-2.5">
                <input
                  type="checkbox"
                  checked={memory.replay_full_history}
                  onChange={(e) =>
                    setMemory((m) => ({
                      ...m,
                      replay_full_history: e.target.checked,
                    }))
                  }
                  className="accent-accent"
                />
                <div className="flex-1">
                  <span className="text-sm">{t('config.memoryReplay')}</span>
                  <p className="text-xs text-dim">
                    {t('config.memoryReplayHint')}
                  </p>
                </div>
              </label>
              <button
                onClick={() => void saveMemory()}
                disabled={memorySaving}
                className="flex items-center gap-1.5 rounded-lg bg-accent px-4 py-1.5 text-sm text-white hover:opacity-90 disabled:opacity-40"
              >
                {memorySaving && <Loader2 size={14} className="animate-spin" />}
                {t('setup.saveApply')}
              </button>
            </div>
          )}

          {tab === 'permissions' && (
            <div className="space-y-4">
              <p className="text-xs text-dim">{t('config.permissionsHint')}</p>
              <div className="flex items-center gap-2 text-xs text-dim">
                <ShieldCheck size={14} className="text-ok" />
                {t('config.permissionsCount', { count: rules.length })}
              </div>
              {rules.length === 0 ? (
                <div className="rounded-xl border border-dashed border-edge px-6 py-10 text-center">
                  <ShieldCheck size={28} className="mx-auto mb-2 text-dim/60" />
                  <p className="text-sm text-dim">
                    {t('config.permissionsEmpty')}
                  </p>
                  <p className="mt-1 text-xs text-dim/70">
                    {t('config.permissionsEmptyHint')}
                  </p>
                </div>
              ) : (
                <div className="overflow-hidden rounded-xl border border-edge bg-panel">
                  {rules.map((rule, i) => (
                    <div
                      key={rule}
                      className={`group flex items-center gap-2 px-3 py-2 hover:bg-panel2 ${
                        i > 0 ? 'border-t border-edge/60' : ''
                      }`}
                    >
                      <Terminal size={14} className="shrink-0 text-dim" />
                      <code className="flex-1 truncate font-mono text-sm text-fg">
                        {rule}
                      </code>
                      <button
                        onClick={() =>
                          void api
                            .denyPermission(rule)
                            .then(() => api.permissions())
                            .then(setRules)
                            .then(() =>
                              toast(t('config.permissionsRemoved', { rule })),
                            )
                            .catch((err) => setError(String(err)))
                        }
                        title={t('config.permissionsRemove')}
                        aria-label={t('config.permissionsRemove')}
                        className="shrink-0 rounded p-1 text-dim opacity-0 transition-opacity hover:text-err group-hover:opacity-100"
                      >
                        <Trash2 size={14} />
                      </button>
                    </div>
                  ))}
                </div>
              )}
              <div className="flex items-center gap-2 rounded-xl border border-edge bg-panel2 p-2">
                <ShieldPlus size={15} className="ml-1 shrink-0 text-dim" />
                <input
                  value={ruleInput}
                  onChange={(e) => setRuleInput(e.target.value)}
                  placeholder={t('config.permissionsPlaceholder')}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' && ruleInput.trim()) {
                      void api
                        .allowPermission(ruleInput.trim())
                        .then(() => api.permissions())
                        .then(setRules)
                        .then(() => {
                          const rule = ruleInput.trim();
                          setRuleInput('');
                          toast(t('config.permissionsAdded', { rule }));
                        })
                        .catch((err) => setError(String(err)));
                    }
                  }}
                  className="flex-1 bg-transparent px-2 py-1 text-sm outline-none placeholder:text-dim/60"
                />
                <button
                  onClick={() => {
                    if (!ruleInput.trim()) return;
                    void api
                      .allowPermission(ruleInput.trim())
                      .then(() => api.permissions())
                      .then(setRules)
                      .then(() => {
                        const rule = ruleInput.trim();
                        setRuleInput('');
                        toast(t('config.permissionsAdded', { rule }));
                      })
                      .catch((err) => setError(String(err)));
                  }}
                  className="rounded-lg bg-accent px-4 py-1.5 text-sm text-white hover:opacity-90 disabled:opacity-40"
                  disabled={!ruleInput.trim()}
                >
                  {t('config.permissionsAdd')}
                </button>
              </div>
            </div>
          )}

          {tab === 'logs' && (
            <div className="h-full flex flex-col gap-3">
              <p className="text-xs text-dim">{t('config.logsHint')}</p>
              <LogViewer fetchLogs={() => api.readLog(300)} />
            </div>
          )}

          {tab === 'diagnostics' && (
            <div className="space-y-4">
              <p className="text-xs text-dim">{t('config.diagHint')}</p>
              {diag && (
                <div className="grid grid-cols-2 gap-3 text-sm">
                  <div className="rounded-lg border border-edge bg-panel2 px-3 py-2">
                    <span className="text-xs text-dim">
                      {t('config.diagVersion')}
                    </span>
                    <p className="font-mono">{diag.version}</p>
                  </div>
                  <div className="rounded-lg border border-edge bg-panel2 px-3 py-2">
                    <span className="text-xs text-dim">
                      {t('config.diagPlatform')}
                    </span>
                    <p className="font-mono">
                      {diag.platform}/{diag.arch}
                    </p>
                  </div>
                  <div className="rounded-lg border border-edge bg-panel2 px-3 py-2">
                    <span className="text-xs text-dim">
                      {t('config.diagRuntime')}
                    </span>
                    <p className="font-mono">
                      go {diag.go_version}
                      {diag.node_version ? ` · node ${diag.node_version}` : ''}
                    </p>
                  </div>
                  <div className="rounded-lg border border-edge bg-panel2 px-3 py-2">
                    <span className="text-xs text-dim">
                      {t('config.diagSandbox')}
                    </span>
                    <p
                      className={`font-mono ${
                        diag.sandbox_available ? 'text-ok' : 'text-err'
                      }`}
                    >
                      {diag.sandbox_backend}
                      {diag.sandbox_available ? ' ✓' : ' ✗'}
                    </p>
                  </div>
                  <div className="rounded-lg border border-edge bg-panel2 px-3 py-2">
                    <span className="text-xs text-dim">
                      {t('config.diagConfig')}
                    </span>
                    <p
                      className={`font-mono ${
                        diag.config_valid ? 'text-ok' : 'text-err'
                      }`}
                    >
                      {diag.config_valid
                        ? t('config.diagOk')
                        : diag.config_error || t('config.diagBroken')}
                    </p>
                  </div>
                  <div className="rounded-lg border border-edge bg-panel2 px-3 py-2">
                    <span className="text-xs text-dim">
                      {t('config.diagInference')}
                    </span>
                    <p
                      className={`font-mono ${
                        diag.inference_configured ? 'text-ok' : 'text-warn'
                      }`}
                    >
                      {diag.inference_configured
                        ? t('config.diagConfigured')
                        : t('config.diagMissing')}
                    </p>
                  </div>
                  <div className="rounded-lg border border-edge bg-panel2 px-3 py-2">
                    <span className="text-xs text-dim">
                      {t('config.diagGit')}
                    </span>
                    <p className="font-mono">
                      {diag.git_repo
                        ? diag.git_branch || '(repo)'
                        : t('config.diagNoRepo')}
                    </p>
                  </div>
                  <div className="rounded-lg border border-edge bg-panel2 px-3 py-2">
                    <span className="text-xs text-dim">
                      {t('config.diagSessions')}
                    </span>
                    <p className="font-mono">
                      {diag.session_count} · {diag.active_runs}{' '}
                      {t('config.diagActiveRuns')}
                    </p>
                  </div>
                  <div className="rounded-lg border border-edge bg-panel2 px-3 py-2 col-span-2">
                    <span className="text-xs text-dim">
                      {t('config.diagPaths')}
                    </span>
                    <p className="font-mono text-xs break-all mt-1">
                      {diag.work_dir}
                      <br />
                      {diag.user_dir}
                    </p>
                  </div>
                </div>
              )}

              <div className="flex flex-wrap items-center gap-2 pt-1">
                <button
                  onClick={() => void runProbe()}
                  disabled={diagBusy}
                  className="rounded-lg bg-accent px-4 py-1.5 text-sm text-white hover:opacity-90 disabled:opacity-40"
                >
                  {t('config.diagProbe')}
                </button>
                <button
                  onClick={() => void clearCaches()}
                  disabled={diagBusy}
                  className="rounded-lg border border-edge px-4 py-1.5 text-sm text-dim hover:text-fg disabled:opacity-40"
                >
                  {t('config.diagClearCache')}
                </button>
                <button
                  onClick={() => void api.reload()}
                  className="rounded-lg border border-edge px-4 py-1.5 text-sm text-dim hover:text-fg"
                >
                  {t('config.diagReload')}
                </button>
              </div>
              {probe && (
                <div
                  className={`rounded-lg border px-3 py-2 text-xs ${
                    probe.ok ? 'border-ok/40 text-ok' : 'border-err/40 text-err'
                  }`}
                >
                  {probe.ok
                    ? t('config.diagProbeOk')
                    : t('config.diagProbeFail')}
                  {probe.output && (
                    <pre className="mt-1 font-mono">{probe.output}</pre>
                  )}
                  {probe.error && (
                    <pre className="mt-1 font-mono">{probe.error}</pre>
                  )}
                </div>
              )}
              {cacheResult && (
                <p className="text-xs text-dim">
                  {t('config.diagCacheDone', {
                    bytes: cacheResult.bytes,
                    dirs: cacheResult.dirs.length,
                  })}
                </p>
              )}

              <div className="space-y-2 pt-1">
                <p className="text-xs text-dim">{t('config.diagPolicy')}</p>
                <div className="flex items-center gap-2">
                  <input
                    value={policyInput}
                    onChange={(e) => setPolicyInput(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') void checkPolicy();
                    }}
                    placeholder={t('config.diagPolicyPlaceholder')}
                    className="flex-1 rounded-lg border border-edge bg-panel2 px-3 py-1.5 text-sm outline-none focus:border-accent"
                  />
                  <button
                    onClick={() => void checkPolicy()}
                    disabled={diagBusy}
                    className="rounded-lg bg-accent px-4 py-1.5 text-sm text-white hover:opacity-90 disabled:opacity-40"
                  >
                    {t('config.diagPolicyCheck')}
                  </button>
                </div>
                {policy && (
                  <p
                    className={`text-sm ${
                      policy.allowed ? 'text-ok' : 'text-warn'
                    }`}
                  >
                    {policy.allowed
                      ? t('config.diagPolicyAllowed')
                      : t('config.diagPolicyAsk')}
                  </p>
                )}
              </div>
            </div>
          )}
        </div>

        <div className="px-5 py-4 border-t border-edge flex items-center gap-3">
          {error && <span className="text-xs text-err flex-1">{error}</span>}
          <span className="flex-1" />
          {tab === 'inference' && (
            <button
              onClick={() => void save()}
              disabled={saving}
              className="flex items-center gap-1.5 rounded-lg bg-accent px-5 py-2 text-sm text-white hover:opacity-90 disabled:opacity-50"
            >
              {saving && <Loader2 size={14} className="animate-spin" />}
              {t('setup.saveApply')}
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
