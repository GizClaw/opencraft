import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { ComponentType } from 'react';
import {
  ArrowDown,
  ArrowDownToLine,
  ArrowUp,
  ArrowUpFromLine,
  BarChart3,
  Brain,
  Check,
  ChevronDown,
  Cpu,
  Database,
  Kanban,
  Languages,
  Loader2,
  Minimize2,
  Monitor,
  Moon,
  Palette,
  Plus,
  Power,
  RefreshCw,
  ScrollText,
  Settings,
  ShieldCheck,
  ShieldPlus,
  Stethoscope,
  Sun,
  Terminal,
  Trash2,
  X,
} from 'lucide-react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { api } from '../lib/api';
import { LogViewer } from './LogViewer';
import { KanbanSection } from './KanbanView';
import { useStore } from '../lib/store';
import type {
  CacheClearResult,
  DiagnosticsReport,
  MemorySettings,
  ModelUsageStat,
  ModelTemplate,
  PolicyDecision,
  ProviderInstance,
  ProviderView,
  SandboxProbeResult,
  UIEvent,
  UsagePoint,
} from '../lib/types';
import { UsageChart } from './UsageChart';
import { MCPLogo, MCPSection } from './ToolsPanel';
import { PluginPanels } from '../plugins/components/PluginPanels';
import { EventsOn } from '../../wailsjs/runtime/runtime';

// InstanceRow is one editable inference instance in the settings page.
interface RowModel {
  name: string;
  kind: string;
  inputs: string[];
  outputs: string[];
  reasoning: string;
  reasoningEffortMap: Record<string, string>;
  effortNone: boolean;
  dimensions: boolean;
  webSearch: boolean;
  endpoint: string;
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
  keyKeychain: boolean;
  models: RowModel[];
  endpoint: string;
  enabled: boolean;
  managed: boolean; // deployment owned by a capability plugin
}

type Tab =
  | 'ui'
  | 'inference'
  | 'mcp'
  | 'usage'
  | 'memory'
  | 'permissions'
  | 'logs'
  | 'diagnostics'
  | 'kanban';

// EFFORT_LEVELS is the canonical reasoning effort ladder flowcraft
// exposes; each level maps to a provider-specific wire token.
const EFFORT_LEVELS = ['minimal', 'low', 'medium', 'high', 'xhigh'] as const;

// effortMapComplete reports whether a non-empty effort map defines all
// five canonical levels. flowcraft rejects partial maps.
function effortMapComplete(m: RowModel): boolean {
  return EFFORT_LEVELS.every(
    (level) => (m.reasoningEffortMap[level] ?? '').trim() !== '',
  );
}

// modelFromTemplate lowers one driver built-in template into an
// editable model row (capabilities are prefilled, not locked).
function modelFromTemplate(t: ModelTemplate) {
  return {
    name: t.name,
    kind: t.kind,
    inputs: t.inputs ?? [],
    outputs: t.outputs ?? [],
    reasoning: t.reasoning,
    reasoningEffortMap: t.reasoning_effort_map ?? {},
    effortNone: t.effort_none ?? false,
    dimensions: t.dimensions,
    webSearch: t.web_search,
  };
}

function templateFor(
  templates: Map<string, ModelTemplate[]>,
  type: string,
  name: string,
): ModelTemplate | undefined {
  return templates.get(type)?.find((t) => t.name === name);
}

export function ConfigPage() {
  const configured = useStore((s) => s.configured);
  const closeConfig = useStore((s) => s.closeConfig);
  const configTab = useStore((s) => s.configTab);
  const toast = useStore((s) => s.toast);
  const theme = useStore((s) => s.theme);
  const setTheme = useStore((s) => s.setTheme);
  const newID = () =>
    `mcp-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
  const { t, i18n } = useTranslation();
  const lang = i18n.resolvedLanguage?.startsWith('zh') ? 'zh' : 'en';
  const [langMenuOpen, setLangMenuOpen] = useState(false);
  // null until the persisted value loads; the row renders once known so
  // the highlighted option never flashes the default.
  const [closeToTray, setCloseToTray] = useState<boolean | null>(null);

  const [tab, setTab] = useState<Tab>(configTab as Tab);

  useEffect(() => {
    let alive = true;
    void api
      .getCloseToTray()
      .then((v) => {
        if (alive) setCloseToTray(v);
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') closeConfig();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [closeConfig]);

  const [rows, setRows] = useState<InstanceRow[]>([]);
  const [catalog, setCatalog] = useState<ProviderView[]>([]);
  const [modelTemplates, setModelTemplates] = useState<
    Map<string, ModelTemplate[]>
  >(new Map());
  const [catalogErrors, setCatalogErrors] = useState<Map<string, string>>(
    new Map(),
  );
  const [modelMenu, setModelMenu] = useState<string | null>(null);
  const [menuRect, setMenuRect] = useState<{
    top: number;
    left: number;
    width: number;
  } | null>(null);
  const modelMenuRef = useRef<HTMLDivElement | null>(null);

  // The catalog dropdown is portaled to document.body so the provider
  // card's overflow-hidden cannot clip it. Scrolling or resizing
  // outside the dropdown closes it rather than leaving a stale
  // position; scrolling inside the dropdown list itself keeps working.
  useEffect(() => {
    if (!modelMenu) return;
    const close = (e: Event) => {
      const target = e.target;
      if (target instanceof Node && modelMenuRef.current?.contains(target)) {
        return;
      }
      setModelMenu(null);
    };
    const closeOnResize = () => setModelMenu(null);
    document.addEventListener('scroll', close, true);
    window.addEventListener('resize', closeOnResize);
    return () => {
      document.removeEventListener('scroll', close, true);
      window.removeEventListener('resize', closeOnResize);
    };
  }, [modelMenu]);
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
  const [runtimePref, setRuntimePref] = useState('external-first');
  const [runtimePrefBusy, setRuntimePrefBusy] = useState(false);
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

  const loadInference = useCallback(async () => {
    try {
      const [providers, state, catalogs] = await Promise.all([
        api.providers(),
        api.configState(),
        api.modelCatalog(),
      ]);
      setCatalog(providers);
      const templates = new Map(
        (catalogs ?? []).map((c) => [c.provider, c.models]),
      );
      const errors = new Map(
        (catalogs ?? [])
          .filter((c) => c.error)
          .map((c) => [c.provider, c.error as string]),
      );
      setModelTemplates(templates);
      setCatalogErrors(errors);
      const byType = new Map(providers.map((p) => [p.id, p]));
      setRows(
        (state.instances ?? []).map((s) => {
          const models = (s.models ?? []).map((m) => ({
            name: m.name ?? '',
            kind: m.kind ?? '',
            inputs: m.inputs ?? [],
            outputs: m.outputs ?? [],
            reasoning: m.reasoning ?? '',
            reasoningEffortMap: m.reasoning_effort_map ?? {},
            effortNone: m.effort_none ?? false,
            dimensions: m.dimensions ?? false,
            webSearch: m.web_search ?? false,
            endpoint: m.endpoint ?? '',
          }));
          const defaultName = byType.get(s.type)?.default_model ?? '';
          const defaultTpl = templateFor(templates, s.type, defaultName);
          return {
            id: newID(),
            stableId: s.stable_id ?? '',
            type: s.type,
            name: s.name ?? '',
            api: s.api ?? '',
            key: s.key ?? '',
            keySet: s.key_set ?? false,
            keyEnv: s.key_env ?? false,
            keyKeychain: s.key_keychain ?? false,
            models:
              models.length > 0
                ? models
                : [
                    defaultTpl
                      ? { ...modelFromTemplate(defaultTpl), endpoint: '' }
                      : {
                          name: defaultName,
                          kind: '',
                          inputs: [],
                          outputs: [],
                          reasoning: '',
                          reasoningEffortMap: {},
                          effortNone: false,
                          dimensions: false,
                          webSearch: false,
                          endpoint: '',
                        },
                  ],
            endpoint: s.endpoint ?? '',
            enabled: s.enabled ?? true,
            managed: s.managed ?? false,
          };
        }),
      );
      setDefaultModel(state.model);
    } catch (err) {
      setError(String(err));
    }
  }, []);

  useEffect(() => {
    void loadInference();
  }, [loadInference]);

  // Refresh the inference config when a plugin upserts/removes a
  // gateway profile (e.g. after SSO login/logout).
  useEffect(() => {
    const off = EventsOn('opencraft:ui', (ev: UIEvent) => {
      if (ev.type === 'inference_changed') void loadInference();
    });
    return off;
  }, [loadInference]);

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
    void Promise.all([api.diagnostics(), api.runtimePreference()])
      .then(([report, preference]) => {
        setDiag(report);
        setRuntimePref(preference || 'external-first');
      })
      .catch((err) => setError(String(err)));
  }, [tab]);

  const saveRuntimePreference = async (next: string) => {
    setRuntimePrefBusy(true);
    try {
      await api.saveRuntimePreference(next);
      setRuntimePref(next);
      toast(t('config.runtimePreferenceSaved'));
      setDiag(await api.diagnostics());
    } catch (err) {
      setError(String(err));
    } finally {
      setRuntimePrefBusy(false);
    }
  };

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
    const defaultTpl = templateFor(
      modelTemplates,
      type,
      prov?.default_model ?? '',
    );
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
        keyKeychain: false,
        models: [
          defaultTpl
            ? { ...modelFromTemplate(defaultTpl), endpoint: '' }
            : {
                name: prov?.default_model ?? '',
                kind: '',
                inputs: [],
                outputs: [],
                reasoning: '',
                reasoningEffortMap: {},
                effortNone: false,
                dimensions: false,
                webSearch: false,
                endpoint: '',
              },
        ],
        endpoint: '',
        enabled: true,
        managed: false,
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

  const applyTemplate = (id: string, idx: number, t: ModelTemplate) => {
    updateModel(id, idx, modelFromTemplate(t));
  };

  const addModel = (id: string) => {
    setRows((prev) =>
      prev.map((r) =>
        r.id === id
          ? {
              ...r,
              models: [
                ...r.models,
                {
                  name: '',
                  kind: '',
                  inputs: [],
                  outputs: [],
                  reasoning: '',
                  reasoningEffortMap: {},
                  effortNone: false,
                  dimensions: false,
                  webSearch: false,
                  endpoint: '',
                },
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

  // moveInstance reorders the router priority: enabled instances form
  // the generate targets in row order, so moving a row up/down changes
  // which model is tried first (and the fallback order).
  const moveInstance = (id: string, dir: -1 | 1) => {
    setRows((prev) => {
      const i = prev.findIndex((r) => r.id === id);
      const j = i + dir;
      if (i < 0 || j < 0 || j >= prev.length) return prev;
      const next = [...prev];
      [next[i], next[j]] = [next[j], next[i]];
      return next;
    });
  };

  const moveModel = (id: string, idx: number, dir: -1 | 1) => {
    setRows((prev) =>
      prev.map((r) => {
        if (r.id !== id) return r;
        const j = idx + dir;
        if (j < 0 || j >= r.models.length) return r;
        const models = [...r.models];
        [models[idx], models[j]] = [models[j], models[idx]];
        return { ...r, models };
      }),
    );
  };

  const save = async () => {
    setError('');
    if (enabledRows.length === 0) {
      setError(t('setup.selectProvider'));
      return;
    }
    for (const r of rows) {
      for (const m of r.models) {
        if (
          m.reasoning !== '' &&
          Object.keys(m.reasoningEffortMap).length > 0 &&
          !effortMapComplete(m)
        ) {
          setError(t('setup.effortMapIncomplete'));
          return;
        }
      }
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
        kind: m.kind,
        inputs: m.inputs,
        outputs: m.outputs,
        reasoning: m.reasoning,
        reasoning_effort_map: m.reasoning === '' ? {} : m.reasoningEffortMap,
        effort_none: m.effortNone,
        dimensions: m.dimensions,
        web_search: m.webSearch,
        endpoint: m.endpoint,
      })),
      endpoint: r.endpoint,
      enabled: r.enabled,
      managed: r.managed,
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

  const fmtBytes = (n: number) => {
    if (n >= 1 << 30) return `${(n / (1 << 30)).toFixed(2)} GB`;
    if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`;
    if (n >= 1 << 10) return `${(n / (1 << 10)).toFixed(0)} KB`;
    return `${n} B`;
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

  const tabs: {
    id: Tab;
    label: string;
    icon: ComponentType<{ className?: string; size?: string | number }>;
  }[] = [
    { id: 'ui', label: t('config.tabUi'), icon: Palette },
    { id: 'inference', label: t('config.tabInference'), icon: Cpu },
    { id: 'mcp', label: t('config.tabMCP'), icon: MCPLogo },
    { id: 'usage', label: t('config.tabUsage'), icon: BarChart3 },
    { id: 'memory', label: t('config.tabMemory'), icon: Database },
    { id: 'permissions', label: t('config.tabPermissions'), icon: ShieldCheck },
    { id: 'logs', label: t('config.tabLogs'), icon: ScrollText },
    { id: 'diagnostics', label: t('config.tabDiagnostics'), icon: Stethoscope },
    { id: 'kanban', label: t('kanban.title'), icon: Kanban },
  ];

  return (
    <div className="fixed bottom-0 top-11 left-0 right-0 z-50 bg-black/70 grid place-items-center">
      <div className="w-[68.5714rem] max-w-[calc(100vw-3.4286rem)] h-[45.7143rem] max-h-[calc(100vh-6.8571rem)] flex flex-col rounded-2xl border border-edge bg-panel shadow-2xl">
        <div className="flex items-center gap-4 px-5 py-4 border-b border-edge">
          <Settings size="1.2857rem" className="text-accent" />
          <h2 className="text-base font-semibold">{t('config.title')}</h2>
          <span className="flex-1" />
          <button
            onClick={closeConfig}
            className="text-dim hover:text-fg"
            aria-label={t('tools.close')}
          >
            <X size="1.2857rem" />
          </button>
        </div>

        <div className="flex min-h-0 flex-1">
          <nav
            className="w-44 shrink-0 space-y-0.5 overflow-y-auto border-r border-edge p-2"
            role="tablist"
            aria-orientation="vertical"
          >
            {tabs.map((tb) => {
              const Icon = tb.icon;
              const active = tab === tb.id;
              return (
                <button
                  key={tb.id}
                  role="tab"
                  aria-selected={active}
                  onClick={() => {
                    setTab(tb.id);
                    setError('');
                  }}
                  className={`flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-sm transition-colors ${
                    active
                      ? 'bg-accent/10 font-medium text-accent'
                      : 'text-dim hover:bg-panel2 hover:text-fg'
                  }`}
                >
                  <Icon size="1.0714rem" className="shrink-0" />
                  <span className="truncate">{tb.label}</span>
                </button>
              );
            })}
          </nav>

          <div className="min-w-0 flex-1 overflow-y-auto px-5 py-4">
            {tab === 'ui' && (
              <div className="space-y-3">
                <PluginPanels tab="ui" />
                <div className="rounded-xl border border-edge bg-panel2 p-4">
                  <div className="flex items-start justify-between gap-4">
                    <div>
                      <div className="flex items-center gap-2 text-sm font-medium">
                        <Languages size="1.0714rem" className="text-accent" />
                        {t('config.uiLanguage')}
                      </div>
                      <p className="mt-1 text-xs text-dim">
                        {t('config.uiLanguageHint')}
                      </p>
                    </div>
                    <div className="relative shrink-0">
                      <button
                        onClick={() => setLangMenuOpen((v) => !v)}
                        className="flex items-center gap-1.5 rounded-lg border border-edge bg-panel px-2.5 py-1.5 text-sm text-fg transition-colors hover:border-accent/50"
                      >
                        <Languages size="0.8571rem" className="text-dim" />
                        {lang === 'zh' ? '中文' : 'English'}
                        <ChevronDown
                          size="0.8571rem"
                          className={`text-dim transition-transform ${
                            langMenuOpen ? 'rotate-180' : ''
                          }`}
                        />
                      </button>
                      {langMenuOpen && (
                        <>
                          <div
                            className="fixed inset-0 z-30"
                            onClick={() => setLangMenuOpen(false)}
                          />
                          <div className="absolute right-0 top-full z-40 mt-1.5 w-40 rounded-xl border border-edge bg-panel p-1 shadow-xl">
                            <button
                              onClick={() => {
                                setLangMenuOpen(false);
                                void i18n.changeLanguage('zh');
                              }}
                              className={`flex w-full items-center justify-between rounded-md px-2 py-1.5 text-left text-sm ${
                                lang === 'zh'
                                  ? 'bg-accent/10 text-accent'
                                  : 'text-dim hover:bg-panel2 hover:text-fg'
                              }`}
                            >
                              <span>中文</span>
                              {lang === 'zh' && <Check size="0.8571rem" />}
                            </button>
                            <button
                              onClick={() => {
                                setLangMenuOpen(false);
                                void i18n.changeLanguage('en');
                              }}
                              className={`flex w-full items-center justify-between rounded-md px-2 py-1.5 text-left text-sm ${
                                lang === 'en'
                                  ? 'bg-accent/10 text-accent'
                                  : 'text-dim hover:bg-panel2 hover:text-fg'
                              }`}
                            >
                              <span>English</span>
                              {lang === 'en' && <Check size="0.8571rem" />}
                            </button>
                          </div>
                        </>
                      )}
                    </div>
                  </div>
                </div>
                <div className="rounded-xl border border-edge bg-panel2 p-4">
                  <div className="flex items-start justify-between gap-4">
                    <div>
                      <div className="flex items-center gap-2 text-sm font-medium">
                        <Palette size="1.0714rem" className="text-accent" />
                        {t('config.uiTheme')}
                      </div>
                      <p className="mt-1 text-xs text-dim">
                        {t('config.uiThemeHint')}
                      </p>
                    </div>
                    <div className="flex shrink-0 overflow-hidden rounded-lg border border-edge text-sm">
                      <button
                        onClick={() => setTheme('dark')}
                        className={`flex items-center gap-1.5 px-3 py-1.5 transition-colors ${
                          theme === 'dark'
                            ? 'bg-accent text-white'
                            : 'text-dim hover:bg-panel hover:text-fg'
                        }`}
                      >
                        <Moon size="0.9286rem" />
                        {t('config.uiThemeDark')}
                      </button>
                      <button
                        onClick={() => setTheme('light')}
                        className={`flex items-center gap-1.5 px-3 py-1.5 transition-colors ${
                          theme === 'light'
                            ? 'bg-accent text-white'
                            : 'text-dim hover:bg-panel hover:text-fg'
                        }`}
                      >
                        <Sun size="0.9286rem" />
                        {t('config.uiThemeLight')}
                      </button>
                      <button
                        onClick={() => setTheme('auto')}
                        className={`flex items-center gap-1.5 px-3 py-1.5 transition-colors ${
                          theme === 'auto'
                            ? 'bg-accent text-white'
                            : 'text-dim hover:bg-panel hover:text-fg'
                        }`}
                      >
                        <Monitor size="0.9286rem" />
                        {t('config.uiThemeAuto')}
                      </button>
                    </div>
                  </div>
                </div>
                {closeToTray !== null && (
                  <div className="rounded-xl border border-edge bg-panel2 p-4">
                    <div className="flex items-start justify-between gap-4">
                      <div>
                        <div className="flex items-center gap-2 text-sm font-medium">
                          <Minimize2 size="1.0714rem" className="text-accent" />
                          {t('config.uiCloseToTray')}
                        </div>
                        <p className="mt-1 text-xs text-dim">
                          {t('config.uiCloseToTrayHint')}
                        </p>
                      </div>
                      <div className="flex shrink-0 overflow-hidden rounded-lg border border-edge text-sm">
                        <button
                          onClick={() => {
                            const next = true;
                            setCloseToTray(next);
                            void api.setCloseToTray(next).catch(() => {
                              setCloseToTray((cur) =>
                                cur === null ? null : !next,
                              );
                              toast(t('config.saveFailed'));
                            });
                          }}
                          className={`flex items-center gap-1.5 px-3 py-1.5 transition-colors ${
                            closeToTray
                              ? 'bg-accent text-white'
                              : 'text-dim hover:bg-panel hover:text-fg'
                          }`}
                        >
                          <Minimize2 size="0.9286rem" />
                          {t('config.uiCloseToTrayHide')}
                        </button>
                        <button
                          onClick={() => {
                            const next = false;
                            setCloseToTray(next);
                            void api.setCloseToTray(next).catch(() => {
                              setCloseToTray((cur) =>
                                cur === null ? null : !next,
                              );
                              toast(t('config.saveFailed'));
                            });
                          }}
                          className={`flex items-center gap-1.5 px-3 py-1.5 transition-colors ${
                            !closeToTray
                              ? 'bg-accent text-white'
                              : 'text-dim hover:bg-panel hover:text-fg'
                          }`}
                        >
                          <Power size="0.9286rem" />
                          {t('config.uiCloseToTrayQuit')}
                        </button>
                      </div>
                    </div>
                  </div>
                )}
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
                    <Plus size="1.0000rem" />
                    {t('config.addInstance')}
                  </button>
                </div>
                {rows.length === 0 && (
                  <p className="text-sm text-dim">
                    {t('config.instancesEmpty')}
                  </p>
                )}
                {rows.map((row, ri) => {
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
                          disabled={row.managed}
                          onChange={(e) =>
                            update(row.id, { enabled: e.target.checked })
                          }
                          className="accent-[var(--color-accent)]"
                          title={
                            row.managed
                              ? t('config.managedBadge')
                              : t('config.instanceEnabled')
                          }
                        />
                        <span className="font-medium text-sm shrink-0">
                          {prov?.name ?? row.type}
                        </span>
                        {row.managed && (
                          <span className="shrink-0 rounded bg-panel px-1.5 py-0.5 text-[0.7143rem] text-dim">
                            {t('config.managedBadge')}
                          </span>
                        )}
                        <input
                          value={row.name}
                          disabled={row.managed}
                          onChange={(e) =>
                            update(row.id, { name: e.target.value })
                          }
                          placeholder={t('config.instanceName')}
                          className="flex-1 min-w-0 rounded-lg border border-edge bg-panel px-2 py-1 text-sm outline-none focus:border-accent"
                        />
                        <button
                          onClick={() => moveInstance(row.id, -1)}
                          disabled={ri === 0}
                          className="text-dim hover:text-fg disabled:opacity-30 shrink-0"
                          title={t('config.moveUp')}
                          aria-label={t('config.moveUp')}
                        >
                          <ArrowUp size="1.0000rem" />
                        </button>
                        <button
                          onClick={() => moveInstance(row.id, 1)}
                          disabled={ri === rows.length - 1}
                          className="text-dim hover:text-fg disabled:opacity-30 shrink-0"
                          title={t('config.moveDown')}
                          aria-label={t('config.moveDown')}
                        >
                          <ArrowDown size="1.0000rem" />
                        </button>
                        {!row.managed && (
                          <button
                            onClick={() =>
                              setRows((prev) =>
                                prev.filter((r) => r.id !== row.id),
                              )
                            }
                            className="text-dim hover:text-err shrink-0"
                            title={t('config.removeInstance')}
                          >
                            <Trash2 size="1.0000rem" />
                          </button>
                        )}
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
                              disabled={row.managed}
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
                                disabled={row.managed}
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
                              {!row.managed && (
                                <button
                                  onClick={() => addModel(row.id)}
                                  className="flex items-center gap-1 text-xs text-dim hover:text-fg"
                                >
                                  <Plus size="0.8571rem" />
                                  {t('config.addModel')}
                                </button>
                              )}
                            </div>
                            {row.models.map((m, mi) => (
                              <div
                                key={mi}
                                className="space-y-2 rounded-lg border border-edge bg-panel p-2.5"
                              >
                                <div className="flex items-center gap-2">
                                  <div className="relative flex-1 min-w-36">
                                    <input
                                      value={m.name}
                                      disabled={row.managed}
                                      onChange={(e) =>
                                        updateModel(row.id, mi, {
                                          name: e.target.value,
                                        })
                                      }
                                      onFocus={(e) => {
                                        const r =
                                          e.currentTarget.getBoundingClientRect();
                                        setMenuRect({
                                          top: r.bottom + 4,
                                          left: r.left,
                                          width: r.width,
                                        });
                                        setModelMenu(`${row.id}:${mi}`);
                                      }}
                                      onBlur={() => setModelMenu(null)}
                                      placeholder={t('setup.model')}
                                      className="w-full rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
                                    />
                                    {modelMenu === `${row.id}:${mi}` &&
                                      !row.managed &&
                                      menuRect &&
                                      createPortal(
                                        <div
                                          ref={modelMenuRef}
                                          style={{
                                            top: menuRect.top,
                                            left: menuRect.left,
                                            width: menuRect.width,
                                          }}
                                          className="fixed z-[100] max-h-56 overflow-y-auto rounded-xl border border-edge bg-panel shadow-xl"
                                        >
                                          {(modelTemplates.get(row.type) ?? [])
                                            .length === 0 ? (
                                            <div className="px-2 py-1.5 text-xs text-dim">
                                              {catalogErrors.get(row.type)
                                                ? t('setup.catalogError')
                                                : t('setup.catalogEmpty')}
                                            </div>
                                          ) : (
                                            (
                                              modelTemplates.get(row.type) ?? []
                                            ).map((tmpl) => (
                                              <button
                                                key={tmpl.name}
                                                type="button"
                                                onMouseDown={(e) =>
                                                  e.preventDefault()
                                                }
                                                onClick={() => {
                                                  applyTemplate(
                                                    row.id,
                                                    mi,
                                                    tmpl,
                                                  );
                                                  setModelMenu(null);
                                                }}
                                                className="flex w-full items-center justify-between gap-2 px-2 py-1.5 text-left text-xs hover:bg-panel2"
                                              >
                                                <span className="truncate font-mono">
                                                  {tmpl.name}
                                                </span>
                                                <span className="shrink-0 text-dim">
                                                  {tmpl.deprecated
                                                    ? `⚠️ ${t('setup.deprecated')}`
                                                    : tmpl.kind}
                                                  {tmpl.deprecated &&
                                                  tmpl.replacement
                                                    ? ` → ${tmpl.replacement}`
                                                    : ''}
                                                </span>
                                              </button>
                                            ))
                                          )}
                                        </div>,
                                        document.body,
                                      )}
                                    {(() => {
                                      const cur = templateFor(
                                        modelTemplates,
                                        row.type,
                                        m.name,
                                      );
                                      return cur ? (
                                        <div className="mt-1 flex flex-wrap items-center gap-2">
                                          {cur.deprecated && (
                                            <span className="text-xs text-amber-600">
                                              ⚠️{' '}
                                              {cur.replacement
                                                ? t('setup.deprecatedHint', {
                                                    replacement:
                                                      cur.replacement,
                                                  })
                                                : t('setup.deprecated')}
                                            </span>
                                          )}
                                          {!row.managed && (
                                            <button
                                              type="button"
                                              onClick={() =>
                                                applyTemplate(row.id, mi, cur)
                                              }
                                              className="text-xs text-dim hover:text-fg"
                                            >
                                              ↺ {t('setup.resetCatalog')}
                                            </button>
                                          )}
                                        </div>
                                      ) : null;
                                    })()}
                                  </div>
                                  <button
                                    onClick={() => moveModel(row.id, mi, -1)}
                                    disabled={mi === 0}
                                    className="shrink-0 text-dim hover:text-fg disabled:opacity-30"
                                    title={t('config.moveUp')}
                                    aria-label={t('config.moveUp')}
                                  >
                                    <ArrowUp size="0.9286rem" />
                                  </button>
                                  <button
                                    onClick={() => moveModel(row.id, mi, 1)}
                                    disabled={mi === row.models.length - 1}
                                    className="shrink-0 text-dim hover:text-fg disabled:opacity-30"
                                    title={t('config.moveDown')}
                                    aria-label={t('config.moveDown')}
                                  >
                                    <ArrowDown size="0.9286rem" />
                                  </button>
                                  {!row.managed && (
                                    <button
                                      onClick={() => removeModel(row.id, mi)}
                                      className="shrink-0 text-dim hover:text-err"
                                      title={t('config.removeModel')}
                                      aria-label={t('config.removeModel')}
                                    >
                                      <X size="1.0000rem" />
                                    </button>
                                  )}
                                </div>
                                <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5 text-xs text-dim">
                                  <span className="flex items-center gap-1.5 whitespace-nowrap">
                                    {t('setup.outputs')}
                                    {['text', 'image', 'audio', 'video'].map(
                                      (kind) => (
                                        <button
                                          key={kind}
                                          type="button"
                                          disabled={row.managed}
                                          onClick={() =>
                                            updateModel(row.id, mi, {
                                              outputs: m.outputs.includes(kind)
                                                ? m.outputs.filter(
                                                    (k) => k !== kind,
                                                  )
                                                : [...m.outputs, kind],
                                            })
                                          }
                                          className={`rounded border px-1.5 py-0.5 ${
                                            m.outputs.includes(kind)
                                              ? 'border-accent bg-panel2 text-fg'
                                              : 'border-edge text-dim hover:text-fg'
                                          }`}
                                        >
                                          {kind}
                                        </button>
                                      ),
                                    )}
                                  </span>
                                  <span className="flex items-center gap-1.5 whitespace-nowrap">
                                    {t('setup.inputs')}
                                    {[
                                      'text',
                                      'image',
                                      'audio',
                                      'video',
                                      'file',
                                      'data',
                                      'tool_call',
                                      'tool_result',
                                    ].map((kind) => (
                                      <button
                                        key={kind}
                                        type="button"
                                        disabled={row.managed}
                                        onClick={() =>
                                          updateModel(row.id, mi, {
                                            inputs: m.inputs.includes(kind)
                                              ? m.inputs.filter(
                                                  (k) => k !== kind,
                                                )
                                              : [...m.inputs, kind],
                                          })
                                        }
                                        className={`rounded border px-1.5 py-0.5 ${
                                          m.inputs.includes(kind)
                                            ? 'border-accent bg-panel2 text-fg'
                                            : 'border-edge text-dim hover:text-fg'
                                        }`}
                                      >
                                        {kind}
                                      </button>
                                    ))}
                                  </span>
                                  <label className="flex items-center gap-1.5 whitespace-nowrap">
                                    {t('setup.reasoning')}
                                    <select
                                      value={m.reasoning}
                                      disabled={row.managed}
                                      onChange={(e) => {
                                        const reasoning = e.target.value;
                                        updateModel(row.id, mi, {
                                          reasoning,
                                          ...(reasoning === ''
                                            ? { reasoningEffortMap: {} }
                                            : {}),
                                        });
                                      }}
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
                                    {t('setup.kind')}
                                    <select
                                      value={m.kind}
                                      disabled={row.managed}
                                      onChange={(e) =>
                                        updateModel(row.id, mi, {
                                          kind: e.target.value,
                                        })
                                      }
                                      className="rounded border border-edge bg-panel px-2 py-1 outline-none"
                                    >
                                      <option value="">auto</option>
                                      <option value="generate">generate</option>
                                      <option value="embed">embed</option>
                                      <option value="image">image</option>
                                      <option value="video">video</option>
                                      <option value="tts">tts</option>
                                    </select>
                                  </label>
                                  {m.kind === 'embed' && (
                                    <label className="flex items-center gap-1.5 whitespace-nowrap">
                                      <input
                                        type="checkbox"
                                        checked={m.dimensions}
                                        disabled={row.managed}
                                        onChange={(e) =>
                                          updateModel(row.id, mi, {
                                            dimensions: e.target.checked,
                                          })
                                        }
                                        className="accent-[var(--color-accent)]"
                                      />
                                      {t('setup.dimensions')}
                                    </label>
                                  )}
                                  {(row.type === 'openai' ||
                                    row.type === 'azure') && (
                                    <label className="flex items-center gap-1.5 whitespace-nowrap">
                                      <input
                                        type="checkbox"
                                        checked={m.effortNone}
                                        disabled={row.managed}
                                        onChange={(e) =>
                                          updateModel(row.id, mi, {
                                            effortNone: e.target.checked,
                                          })
                                        }
                                        className="accent-[var(--color-accent)]"
                                      />
                                      {t('setup.effortNone')}
                                    </label>
                                  )}
                                  {m.reasoning !== '' && (
                                    <span className="flex w-full flex-wrap items-center gap-1.5">
                                      <span className="text-dim">
                                        {t('setup.effortMap')}
                                      </span>
                                      {EFFORT_LEVELS.map((level) => (
                                        <label
                                          key={level}
                                          className="flex items-center gap-1 text-xs"
                                        >
                                          <span className="text-dim">
                                            {level}
                                          </span>
                                          <input
                                            value={
                                              m.reasoningEffortMap[level] ?? ''
                                            }
                                            disabled={row.managed}
                                            placeholder={t(
                                              'setup.effortMapPlaceholder',
                                            )}
                                            onChange={(e) => {
                                              const next = {
                                                ...m.reasoningEffortMap,
                                              };
                                              const value = e.target.value;
                                              if (value.trim() === '') {
                                                delete next[level];
                                              } else {
                                                next[level] = value;
                                              }
                                              updateModel(row.id, mi, {
                                                reasoningEffortMap: next,
                                              });
                                            }}
                                            className="w-20 rounded border border-edge bg-panel px-1.5 py-0.5 text-xs outline-none focus:border-accent"
                                          />
                                        </label>
                                      ))}
                                      <button
                                        type="button"
                                        disabled={row.managed}
                                        onClick={() =>
                                          updateModel(row.id, mi, {
                                            reasoningEffortMap: {},
                                          })
                                        }
                                        className="rounded border border-edge px-1.5 py-0.5 text-xs text-dim hover:text-fg disabled:opacity-50"
                                      >
                                        {t('setup.effortMapClear')}
                                      </button>
                                    </span>
                                  )}
                                  <label className="flex items-center gap-1.5 whitespace-nowrap">
                                    <input
                                      type="checkbox"
                                      checked={m.webSearch}
                                      disabled={row.managed}
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
                                {prov?.model_endpoint && (
                                  <input
                                    value={m.endpoint}
                                    disabled={row.managed}
                                    onChange={(e) =>
                                      updateModel(row.id, mi, {
                                        endpoint: e.target.value,
                                      })
                                    }
                                    placeholder={t('setup.endpoint')}
                                    className="w-full rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
                                  />
                                )}
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
                                disabled={row.keyEnv || row.managed}
                                onChange={(e) =>
                                  update(row.id, { key: e.target.value })
                                }
                                placeholder={
                                  row.keyKeychain && row.key === ''
                                    ? t('config.keychainStored')
                                    : row.keySet && row.key === ''
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
                                  disabled={row.managed}
                                  onChange={(e) =>
                                    update(row.id, { keyEnv: e.target.checked })
                                  }
                                  className="accent-[var(--color-accent)]"
                                />
                                {t('setup.envVar', {
                                  var: prov?.env_var ?? '',
                                })}
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
                          <span className="text-xs text-dim w-5">
                            {idx + 1}
                          </span>
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
                            <ArrowUp size="1.0000rem" />
                          </button>
                          <button
                            onClick={() => move(idx, 1)}
                            disabled={idx === enabledRows.length - 1}
                            className="text-dim hover:text-fg disabled:opacity-30"
                            title={t('config.moveDown')}
                            aria-label={t('config.moveDown')}
                          >
                            <ArrowDown size="1.0000rem" />
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
                      <Loader2 size="0.9286rem" className="animate-spin" />
                    ) : (
                      <RefreshCw size="0.9286rem" />
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
                        className="h-[5.1429rem] animate-pulse rounded-xl bg-panel2"
                      />
                    ))}
                  </div>
                ) : (
                  <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
                    <div className="rounded-xl border border-edge bg-panel2 p-3">
                      <div className="flex items-center gap-1.5 text-xs text-dim">
                        <ArrowDownToLine
                          size="0.9286rem"
                          className="text-accent"
                        />
                        {t('config.usageInput')}
                      </div>
                      <p className="mt-1.5 text-lg font-semibold tabular-nums text-accent">
                        {fmtUsageTokens(usageTotals.input)}
                      </p>
                    </div>
                    <div className="rounded-xl border border-edge bg-panel2 p-3">
                      <div className="flex items-center gap-1.5 text-xs text-dim">
                        <ArrowUpFromLine size="0.9286rem" className="text-ok" />
                        {t('config.usageOutput')}
                      </div>
                      <p className="mt-1.5 text-lg font-semibold tabular-nums text-ok">
                        {fmtUsageTokens(usageTotals.output)}
                      </p>
                    </div>
                    <div className="rounded-xl border border-edge bg-panel2 p-3">
                      <div className="flex items-center gap-1.5 text-xs text-dim">
                        <Database size="0.9286rem" className="text-subagent" />
                        {t('config.usageCache')}
                      </div>
                      <p className="mt-1.5 text-lg font-semibold tabular-nums text-subagent">
                        {fmtUsageTokens(usageTotals.cache)}
                      </p>
                    </div>
                    <div className="rounded-xl border border-edge bg-panel2 p-3">
                      <div className="flex items-center gap-1.5 text-xs text-dim">
                        <Brain size="0.9286rem" className="text-warn" />
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
                        className="max-w-[24.2857rem] rounded-lg border border-edge bg-panel px-2 py-1.5 text-xs font-mono outline-none"
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
                        <Loader2
                          size="1.0000rem"
                          className="animate-spin text-dim"
                        />
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
                      <div className="mt-1.5 text-[0.7143rem] text-dim tabular-nums">
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
                  {memorySaving && (
                    <Loader2 size="1.0000rem" className="animate-spin" />
                  )}
                  {t('setup.saveApply')}
                </button>
              </div>
            )}

            {tab === 'permissions' && (
              <div className="space-y-4">
                <p className="text-xs text-dim">
                  {t('config.permissionsHint')}
                </p>
                <div className="flex items-center gap-2 text-xs text-dim">
                  <ShieldCheck size="1.0000rem" className="text-ok" />
                  {t('config.permissionsCount', { count: rules.length })}
                </div>
                {rules.length === 0 ? (
                  <div className="rounded-xl border border-dashed border-edge px-6 py-10 text-center">
                    <ShieldCheck
                      size="2.0000rem"
                      className="mx-auto mb-2 text-dim/60"
                    />
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
                        <Terminal
                          size="1.0000rem"
                          className="shrink-0 text-dim"
                        />
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
                          <Trash2 size="1.0000rem" />
                        </button>
                      </div>
                    ))}
                  </div>
                )}
                <div className="flex items-center gap-2 rounded-xl border border-edge bg-panel2 p-2">
                  <ShieldPlus
                    size="1.0714rem"
                    className="ml-1 shrink-0 text-dim"
                  />
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
                        {diag.node_version
                          ? ` · node ${diag.node_version}`
                          : ''}
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

                <div className="rounded-lg border border-edge bg-panel2 px-3 py-2">
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <div>
                      <p className="text-xs text-dim">
                        {t('config.runtimePreference')}
                      </p>
                      <p className="mt-1 text-xs text-dim/70">
                        {t('config.runtimePreferenceHint')}
                      </p>
                    </div>
                    <div className="flex items-center gap-2">
                      <select
                        value={runtimePref}
                        onChange={(e) =>
                          void saveRuntimePreference(e.target.value)
                        }
                        disabled={runtimePrefBusy}
                        className="rounded-lg border border-edge bg-panel px-2 py-1.5 text-sm"
                      >
                        <option value="external-first">
                          {t('config.runtimePreferenceExternal')}
                        </option>
                        <option value="bundled-first">
                          {t('config.runtimePreferenceBundled')}
                        </option>
                        <option value="off">
                          {t('config.runtimePreferenceOff')}
                        </option>
                      </select>
                      {runtimePrefBusy && (
                        <Loader2
                          className="animate-spin text-dim"
                          size="0.9rem"
                        />
                      )}
                    </div>
                  </div>
                </div>

                {diag?.toolchains && diag.toolchains.length > 0 && (
                  <div className="rounded-lg border border-edge bg-panel2 px-3 py-2">
                    <p className="text-xs text-dim">
                      {t('config.diagToolchains')}
                    </p>
                    <div className="mt-2 grid grid-cols-2 gap-2">
                      {diag.toolchains.map((tc) => (
                        <div
                          key={tc.tool}
                          className="rounded-lg border border-edge/70 bg-panel px-2 py-1.5"
                        >
                          <p className="font-mono text-sm">{tc.tool}</p>
                          <p
                            className={`mt-0.5 truncate font-mono text-xs ${
                              tc.error ? 'text-err' : 'text-dim'
                            }`}
                            title={tc.error || tc.path || undefined}
                          >
                            {tc.error
                              ? t('config.diagToolMissing')
                              : [
                                  tc.version,
                                  tc.source === 'system'
                                    ? t('config.toolchainSystem')
                                    : tc.source === 'bundled'
                                      ? t('config.toolchainBundled')
                                      : '',
                                ]
                                  .filter(Boolean)
                                  .join(' · ')}
                          </p>
                        </div>
                      ))}
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
                      probe.ok
                        ? 'border-ok/40 text-ok'
                        : 'border-err/40 text-err'
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
                      bytes: fmtBytes(cacheResult.bytes),
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

            {tab === 'mcp' && <MCPSection />}
            {tab === 'kanban' && <KanbanSection />}
          </div>
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
              {saving && <Loader2 size="1.0000rem" className="animate-spin" />}
              {t('setup.saveApply')}
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
