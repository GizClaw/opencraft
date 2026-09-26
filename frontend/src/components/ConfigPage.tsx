import { UIEventChannel, UIEventType } from '../lib/events';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { ComponentType } from 'react';
import {
  ArrowDown,
  ArrowUp,
  BarChart3,
  Check,
  ChevronDown,
  Cpu,
  Database,
  Import,
  ListPlus,
  Loader2,
  Palette,
  Plus,
  RefreshCw,
  Search,
  Settings,
  ShieldCheck,
  ShieldPlus,
  ShieldAlert,
  SlidersHorizontal,
  Sparkles,
  Stethoscope,
  Terminal,
  Trash2,
  Wrench,
  X,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { api } from '../lib/api';
import { formatCompact } from '../lib/compactNumber';
import { cacheHitPercent, formatHitPercent } from '../lib/usageRate';
import { alignUsageWindow } from '../lib/usageWindow';
import { LogViewer } from './LogViewer';
import { MetricsCharts } from './MetricsCharts';
import { PathEnvironmentCard } from './PathEnvironmentCard';
import { ExecPoolCard } from './ExecPoolCard';
import { RecoveryCard } from './RecoveryCard';
import { HeapProfileCard } from './HeapProfileCard';
import { HTTPProbeCard } from './HTTPProbeCard';
import { PerfProbeCard } from './PerfProbeCard';
import { PetBehaviorPanel } from './PetBehaviorPanel';
import { TelemetryExportCard } from './TelemetryExportCard';
import { ToolsSection } from './ToolsSection';
import { MemoryContextCard } from './MemoryContextCard';
import { UserMemoryCard } from './UserMemoryCard';
import { DelegationCard } from './DelegationCard';
import { WebSearchSection } from './WebSearchSection';
import { useStore } from '../lib/store';
import type {
  CacheClearResult,
  DiagnosticsReport,
  InferenceCatalogModel,
  InferenceCatalogTemplate,
  InstanceSpec,
  MemorySettings,
  ModelSpec,
  ModelUsageStat,
  PolicyDecision,
  ProviderView,
  ModelLifecycle,
  ProviderAdvanced,
  RouterPolicy,
  SandboxProbeResult,
  UIEvent,
  UsagePoint,
} from '../lib/types';
import { UsageChart } from './UsageChart';
import { UsageHero } from './UsageHero';
import { UsageModelSelect } from './UsageModelSelect';
import { UsageRangePicker } from './UsageRangePicker';
import { MCPSection } from './ToolsPanel';
import { AdvancedSection } from './InferenceAdvanced';
import { ModelAdvanced } from './ModelAdvanced';
import { PluginPanels } from '../plugins/components/PluginPanels';
import { usePluginStore } from '../plugins/store';
import { Events } from '@wailsio/runtime';
import { SettingsGeneral } from './SettingsGeneral';
import { SettingsDisplay } from './SettingsDisplay';
import { useOverlayLayer } from '../lib/overlay';
import { ICON } from './ui/icon';
import { SaveBar } from './ui/SaveBar';
import { Overlay } from './ui/Overlay';
import { Modal } from './ui/Modal';
import { Badge } from './ui/Badge';
import { Popover } from './ui/Popover';
import { Button, IconButton } from './ui/Button';
import { EmptyState } from './ui/EmptyState';
import { NumberField } from './ui/NumberField';
import { searchSettings } from './settingsIndex';

// InstanceRow is one editable inference instance in the settings page.
interface RowModel {
  name: string;
  kind: string;
  inputs: string[];
  outputs: string[];
  reasoning: string;
  reasoningEffortMap: Record<string, string>;
  webSearch: boolean;
  endpoint: string;
  // '' means "auto": keep the driver catalog value / unknown.
  maxInputTokens: number | '';
  maxOutputTokens: number | '';
  // Discovery metadata: '' status means the model is active.
  lifecycleStatus: string;
  lifecycleReplacementProvider: string;
  lifecycleReplacementName: string;
  lifecycleNotes: string;
  // Driver-specific model leaves, edited as one JSON object.
  specJson: string;
}

type UsagePreset = 'today' | '1d' | '7d' | '14d' | '30d';
type UsageRangeSelection = UsagePreset | 'custom';

interface InstanceRow {
  id: string; // frontend key
  stableId: string; // persisted identity ("" on newly added rows)
  type: string;
  // driver is the flowcraft driver impl of a plugin-declared vendor
  // (a type outside the preset catalog). Managed rows carry it
  // read-only; rows built from the catalog leave it empty.
  driver: string;
  name: string;
  api: string;
  key: string;
  // keyRef is the credential-store account a keychain row references;
  // the settings page round-trips it so an edit keeps the credential.
  keyRef: string;
  keySet: boolean;
  keyEnv: boolean;
  keyKeychain: boolean;
  models: RowModel[];
  endpoint: string;
  // advanced holds the provider spec knobs the advanced section edits;
  // an empty object means "driver defaults".
  advanced: ProviderAdvanced;
  enabled: boolean;
  managed: boolean; // deployment owned by a capability plugin
}

type Tab =
  | 'general'
  | 'display'
  | 'inference'
  | 'tools'
  | 'usage'
  | 'memory'
  | 'permissions'
  | 'diagnostics'
  | 'import';

// AUTO_LIMIT is the row value for "no explicit limit": keep the driver
// catalog value / unknown.
const AUTO_LIMIT: number | '' = '';

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

function limitToRow(v: number | undefined): number | '' {
  return v === undefined || !Number.isFinite(v) || v <= 0 ? '' : v;
}

// sameMemorySettings compares the fold knobs the memory card's save bar
// watches: four scalars, so equality is spelled out instead of
// stringified.
function sameMemorySettings(a: MemorySettings, b: MemorySettings): boolean {
  return (
    a.max_raw_messages === b.max_raw_messages &&
    a.preserve_recent === b.preserve_recent &&
    a.max_summary_bytes === b.max_summary_bytes &&
    a.replay_full_history === b.replay_full_history
  );
}

// modelLifecyclePayload renders one row's discovery metadata, dropping
// the whole block when the model is active (an active model must not
// carry retirement facts).
function modelLifecyclePayload(m: RowModel): ModelLifecycle | undefined {
  const status = m.lifecycleStatus.trim();
  if (status === '') return undefined;
  return {
    status,
    replacement_provider: m.lifecycleReplacementProvider.trim() || undefined,
    replacement_name: m.lifecycleReplacementName.trim() || undefined,
    notes: m.lifecycleNotes.trim() || undefined,
  };
}

// driverFieldsText renders the driver-specific model leaves as the JSON
// object the form edits; an empty bag stays an empty string.
function driverFieldsText(fields: Record<string, unknown> | undefined): string {
  if (fields === undefined || Object.keys(fields).length === 0) return '';
  return JSON.stringify(fields, null, 2);
}

// driverFieldsPayload reads the edited JSON back. Invalid input yields
// undefined: the save is blocked by the form's own validation, and the
// writer validates again.
function driverFieldsPayload(
  text: string,
): Record<string, unknown> | undefined {
  const trimmed = text.trim();
  if (trimmed === '') return undefined;
  try {
    const parsed: unknown = JSON.parse(trimmed);
    if (
      parsed === null ||
      typeof parsed !== 'object' ||
      Array.isArray(parsed)
    ) {
      return undefined;
    }
    return parsed as Record<string, unknown>;
  } catch {
    return undefined;
  }
}

// credentialPayload renders one row's credential the way the form
// states it: the env source when the box is checked, a typed key as a
// literal, an existing keychain reference explicitly, and nothing at all
// when the user did not touch the credential (the host then keeps the
// stored one).
function credentialPayload(
  r: InstanceRow,
): Pick<InstanceSpec, 'key_source' | 'key_ref' | 'key_value'> {
  if (r.keyEnv) return { key_source: 'env' };
  const typed = r.key.trim();
  if (typed !== '') return { key_source: 'literal', key_value: typed };
  if (r.keyKeychain && r.keyRef !== '') {
    return { key_source: 'keychain', key_ref: r.keyRef };
  }
  if (r.keySet) return { key_source: r.keyKeychain ? 'keychain' : 'literal' };
  return {};
}

// emptyModelRow is the row a new instance or model starts from:
// opencraft keeps no model table, so the deployment names the model and
// declares its capabilities.
function emptyModelRow(): RowModel {
  return {
    name: '',
    kind: '',
    inputs: [],
    outputs: [],
    reasoning: '',
    reasoningEffortMap: {},
    webSearch: false,
    endpoint: '',
    maxInputTokens: AUTO_LIMIT,
    maxOutputTokens: AUTO_LIMIT,
    lifecycleStatus: '',
    lifecycleReplacementProvider: '',
    lifecycleReplacementName: '',
    lifecycleNotes: '',
    specJson: '',
  };
}

// rowModelFromSpec maps one wire model declaration onto the editable
// row. The load path and the built-in catalog both carry a ModelSpec, so
// both fill the form through this one mapping.
function rowModelFromSpec(m: ModelSpec): RowModel {
  return {
    name: m.name ?? '',
    kind: m.kind ?? '',
    inputs: m.capabilities?.inputs ?? [],
    outputs: m.capabilities?.outputs ?? [],
    reasoning: m.capabilities?.reasoning?.kind ?? '',
    reasoningEffortMap: m.capabilities?.reasoning?.effort_map ?? {},
    webSearch: m.capabilities?.hosted_web_search ?? false,
    endpoint: m.endpoint ?? '',
    maxInputTokens: limitToRow(m.limits?.max_input_tokens),
    maxOutputTokens: limitToRow(m.limits?.max_output_tokens),
    lifecycleStatus: m.lifecycle?.status ?? '',
    lifecycleReplacementProvider: m.lifecycle?.replacement_provider ?? '',
    lifecycleReplacementName: m.lifecycle?.replacement_name ?? '',
    lifecycleNotes: m.lifecycle?.notes ?? '',
    specJson: driverFieldsText(m.driver_fields),
  };
}

// declaresVideoInput reports whether one model declaration accepts video
// content. Such a model only works on a chat deployment that states
// wire.video_input, which is why picking it may need a provider-level
// change the user has to make themselves.
function declaresVideoInput(m: ModelSpec): boolean {
  return m.capabilities?.inputs?.includes('video') ?? false;
}

// catalogEntryMatches reports whether one built-in model matches the
// list's search box: the model name, its display label, and its vendor
// all match, so "kimi" and "moonshot" both find the Kimi models.
function catalogEntryMatches(
  entry: InferenceCatalogModel,
  query: string,
): boolean {
  const needle = query.trim().toLowerCase();
  if (needle === '') return true;
  return [entry.model.name, entry.label ?? '', entry.vendor ?? ''].some(
    (field) => field.toLowerCase().includes(needle),
  );
}

// templateMatches reports whether one built-in template matches the
// template search box: its label, its vendor, and the models it starts
// with all match.
function templateMatches(
  template: InferenceCatalogTemplate,
  query: string,
): boolean {
  const needle = query.trim().toLowerCase();
  if (needle === '') return true;
  return [
    template.label,
    template.vendor ?? '',
    ...(template.models ?? []).map((m) => m.name),
  ].some((field) => field.toLowerCase().includes(needle));
}

// DiagSection groups the diagnostics tab into scannable blocks. The tab
// used to be one flat column of cards — environment facts, maintenance
// buttons, forms, runtime knobs, logs and charts all at the same visual
// weight, with no headings to say what belonged together. Sections carry
// the ids the settings search jumps to.
function DiagSection({
  id,
  title,
  hint,
  badge,
  children,
}: {
  id?: string;
  title: string;
  hint?: string;
  badge?: string;
  children: React.ReactNode;
}) {
  return (
    <section
      id={id}
      className="scroll-mt-4 space-y-2 border-t border-edge pt-4 first:border-t-0 first:pt-0"
    >
      <div className="space-y-0.5">
        <div className="flex items-center gap-2">
          <h3 className="text-label font-semibold uppercase tracking-wide text-dim">
            {title}
          </h3>
          {badge && <Badge tone="neutral">{badge}</Badge>}
        </div>
        {hint && <p className="text-xs text-dim">{hint}</p>}
      </div>
      {children}
    </section>
  );
}

// DEV_ANCHORS are the diagnostics rows that only render with the DEV
// switch on. The settings search may still target one, so the jump turns
// the switch on rather than scrolling to a row that is not there.
const DEV_ANCHORS = new Set([
  'diag-perfprobe',
  'diag-heap',
  'diag-otlp',
  'diag-httpprobe',
  'diag-execpool',
]);

// DiagToolRow is the visible face of a diagnostics tool that opens in a
// dialog: one line of what it is, and the action that opens it. The tab
// used to inline a 22rem log pane, a chart grid and two editors, which
// buried the facts above them.
function DiagToolRow({
  hint,
  action,
  onAction,
}: {
  hint: string;
  action: string;
  onAction: () => void;
}) {
  return (
    <div className="flex items-center gap-3 rounded-card border border-edge bg-panel2 p-3">
      <p className="min-w-0 flex-1 text-xs text-dim">{hint}</p>
      <Button variant="quiet" size="md" onClick={onAction}>
        {action}
      </Button>
    </div>
  );
}

export function ConfigPage() {
  const configOpen = useStore((s) => s.configOpen);
  const closeConfig = useStore((s) => s.closeConfig);
  const configTab = useStore((s) => s.configTab);
  const openSessionInWorkspace = useStore((s) => s.openSessionInWorkspace);
  const yoloOnly = useStore((s) => s.yoloOnly);
  const toast = useStore((s) => s.toast);
  const newID = () =>
    `mcp-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
  const { t } = useTranslation();

  // 'mcp' merged into the tools tab; a stored value from an older build
  // still lands on a tab that renders the MCP section.
  const [tab, setTab] = useState<Tab>(
    configTab === 'mcp' ? 'tools' : (configTab as Tab),
  );
  // Nav search: a query swaps the tab list for matching destinations, and
  // pendingAnchor is the section the next render scrolls to (the anchor
  // only exists once its tab has rendered).
  const [query, setQuery] = useState('');
  const [pendingAnchor, setPendingAnchor] = useState<string | null>(null);
  const tabRefs = useRef(new Map<string, HTMLButtonElement>());
  const contentRef = useRef<HTMLDivElement | null>(null);
  // The search field owns Escape while it holds text: the key empties the
  // field first, and only an Escape on the empty field closes the page.
  // It gets there by registering a layer of its own, so the shared stack
  // hands the key to it instead of to the page underneath.
  const searchFieldRef = useRef<HTMLDivElement | null>(null);
  useOverlayLayer({
    active: query !== '',
    containerRef: searchFieldRef,
    onDismiss: () => setQuery(''),
    trap: false,
    lock: false,
    restoreFocus: false,
  });
  const importPanelCount = usePluginStore((s) =>
    s.panels.reduce(
      (n, p) => n + ((p.tab ?? 'plugins') === 'import' ? 1 : 0),
      0,
    ),
  );

  const [rows, setRows] = useState<InstanceRow[]>([]);
  // Router retry policy; the targets themselves follow the instance
  // list order, so this is the only router field the page edits.
  const [router, setRouter] = useState<RouterPolicy>({
    max_attempts: 2,
    fallback_on_retry_exhausted: true,
  });
  const [catalog, setCatalog] = useState<ProviderView[]>([]);
  // Built-in catalog: templates prefill a whole instance, models prefill
  // a model row. Neither applies anything on its own (see
  // api.inferenceCatalog); every prefilled value stays editable.
  const [templates, setTemplates] = useState<InferenceCatalogTemplate[]>([]);
  const [modelCatalog, setModelCatalog] = useState<InferenceCatalogModel[]>([]);
  // catalogQuery filters the built-in model list of one row; it resets
  // every time that list is opened.
  const [catalogQuery, setCatalogQuery] = useState('');
  // templateQuery filters the built-in template pills.
  const [templateQuery, setTemplateQuery] = useState('');
  // The field menus (kind / inputs / outputs / reasoning / API mode /
  // model catalog) are anchored popovers. Each one keeps the trigger that
  // opened it, so the shared Popover shell can measure and place the
  // panel — no rectangle bookkeeping here.
  const [fieldMenu, setFieldMenu] = useState<{
    key: string;
    anchor: HTMLElement;
  } | null>(null);

  const openFieldMenu = (
    e: { currentTarget: HTMLButtonElement },
    key: string,
  ) => {
    setFieldMenu({ key, anchor: e.currentTarget });
  };

  const closeFieldMenu = () => setFieldMenu(null);
  // newType is the driver the "add instance" picker will create. It is
  // filled from the loaded driver list so it can never name a provider
  // that no longer exists.
  const [newType, setNewType] = useState('');
  const [defaultModel, setDefaultModel] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const [rules, setRules] = useState<string[]>([]);
  const [ruleInput, setRuleInput] = useState('');
  // Commands the user allowed to leave the sandbox entirely. They are a
  // stronger grant than the allowlist above: a match skips the confined
  // attempt and the approval prompt.
  const [escalatedRules, setEscalatedRules] = useState<string[]>([]);
  const [escalatedInput, setEscalatedInput] = useState('');
  const [memory, setMemory] = useState<MemorySettings>({
    max_raw_messages: 36,
    preserve_recent: 4,
    max_summary_bytes: 4096,
    replay_full_history: false,
  });
  const [memorySaving, setMemorySaving] = useState(false);
  const [memorySaved, setMemorySaved] = useState(false);
  // The values the memory card's save bar compares its drafts against:
  // loaded with the form and updated on every successful save, so the bar
  // cannot claim "unsaved" before the user has touched anything.
  const [memoryBase, setMemoryBase] = useState<MemorySettings | null>(null);
  const [diag, setDiag] = useState<DiagnosticsReport | null>(null);
  const [probe, setProbe] = useState<SandboxProbeResult | null>(null);
  const [policyInput, setPolicyInput] = useState('');
  const [policy, setPolicy] = useState<PolicyDecision | null>(null);
  const [cacheResult, setCacheResult] = useState<CacheClearResult | null>(null);
  const [diagBusy, setDiagBusy] = useState(false);
  // devMode reveals the capture and tuning tools a normal support pass
  // does not need. The dialogs below are the tools whose editors are too
  // big to sit in the column at full size.
  const [devMode, setDevMode] = useState(false);
  const [logOpen, setLogOpen] = useState(false);
  const [metricsOpen, setMetricsOpen] = useState(false);
  const [pathOpen, setPathOpen] = useState(false);
  const [execPoolOpen, setExecPoolOpen] = useState(false);
  const [usageRows, setUsageRows] = useState<ModelUsageStat[]>([]);
  const [usageSessions, setUsageSessions] = useState(0);
  const [usageError, setUsageError] = useState('');
  // The trend starts on the all-models aggregate ('' selects every
  // model): a model renamed in settings keeps its old usage rows under
  // the previous name, so defaulting to the most-used name can plot a
  // model the user no longer runs.
  const [usageModel, setUsageModel] = useState('');
  const [usageGranularity, setUsageGranularity] = useState<'hour' | 'day'>(
    'hour',
  );
  const [usageRange, setUsageRange] = useState<UsageRangeSelection>('7d');
  const [customStartMs, setCustomStartMs] = useState(0);
  const [customEndMs, setCustomEndMs] = useState(0);
  const [usageLiveEnd, setUsageLiveEnd] = useState(true);
  const [usageSnapshotEndMs, setUsageSnapshotEndMs] = useState(0);
  const [usageSeries, setUsageSeries] = useState<UsagePoint[]>([]);
  const [usageStartMs, setUsageStartMs] = useState(0);
  const [usageEndMs, setUsageEndMs] = useState(0);
  const [usageLoading, setUsageLoading] = useState(false);
  const [usageReload, setUsageReload] = useState(0);

  const loadInference = useCallback(async () => {
    try {
      const [providers, state, inferenceCatalog] = await Promise.all([
        api.providers(),
        api.configState(),
        // The built-in catalog is a convenience: a failure to load it
        // must not take the settings page down with it.
        api.inferenceCatalog().catch(() => null),
      ]);
      setCatalog(providers);
      setTemplates(inferenceCatalog?.templates ?? []);
      setModelCatalog(inferenceCatalog?.models ?? []);
      setNewType((prev) =>
        providers.some((p) => p.id === prev) ? prev : (providers[0]?.id ?? ''),
      );
      setRows(
        (state.instances ?? []).map((s) => {
          const models = (s.models ?? []).map(rowModelFromSpec);
          return {
            id: newID(),
            stableId: s.stable_id ?? '',
            type: s.type,
            driver: s.driver ?? '',
            name: s.name ?? '',
            api: s.api ?? '',
            key: '',
            keyRef: s.key_ref ?? '',
            keySet: s.key_set ?? false,
            keyEnv: s.key_env ?? false,
            keyKeychain: s.key_keychain ?? false,
            models: models.length > 0 ? models : [emptyModelRow()],
            endpoint: s.endpoint ?? '',
            advanced: s.advanced ?? {},
            enabled: s.enabled ?? true,
            managed: s.managed ?? false,
          };
        }),
      );
      setRouter(
        state.router ?? {
          max_attempts: 2,
          fallback_on_retry_exhausted: true,
        },
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
    const off = Events.On(UIEventChannel, (e) => {
      const ev = e.data as UIEvent;
      if (ev.type === UIEventType.inferenceChanged) void loadInference();
    });
    return off;
  }, [loadInference]);

  useEffect(() => {
    if (tab !== 'permissions') return;
    void api
      .permissions()
      .then(setRules)
      .catch((err) => setError(String(err)));
    void api
      .escalatedPermissions()
      .then(setEscalatedRules)
      .catch((err) => setError(String(err)));
  }, [tab]);

  useEffect(() => {
    if (tab !== 'memory') return;
    void api
      .memoryConfig()
      .then((next) => {
        setMemory(next);
        setMemoryBase(next);
      })
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

  const repairConfigCompat = async () => {
    setDiagBusy(true);
    try {
      const result = await api.repairConfigCompat();
      // Wails marshals a nil Go slice as null, so read the list
      // defensively: a layer with nothing to repair sends no list.
      const removed = result.removed ?? [];
      if (removed.length === 0) {
        toast(t('config.diagRepairCompatNone'));
      } else {
        toast(
          t('config.diagRepairCompatDone', {
            count: removed.length,
            paths: removed.join(', '),
          }),
        );
      }
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
      setMemorySaved(true);
      setMemoryBase(memory);
    } catch (err) {
      setMemorySaved(false);
      setError(String(err));
    } finally {
      setMemorySaving(false);
    }
  };

  // Any edit invalidates the saved check, so the bar never claims the
  // form on screen is what the runtime is using.
  const editMemory = (patch: Partial<MemorySettings>) => {
    setMemory((m) => ({ ...m, ...patch }));
    setMemorySaved(false);
  };

  useEffect(() => {
    if (tab !== 'usage') return;
    void Promise.all([api.modelUsage(), api.modelUsageSessionCount()])
      .then(([rows, sessions]) => {
        setUsageRows(rows);
        setUsageSessions(sessions);
      })
      .catch((err) => setUsageError(String(err)));
  }, [tab]);

  // Resolve the selected preset into a live [start, end) window the
  // same way cc-switch does: "today" and multi-day presets start at
  // local midnight, "1d" is the rolling 24h, and the end is always now.
  const resolveUsageRange = (
    preset: UsagePreset,
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
  // tab, or an explicit refresh changes. Granularity follows the raw
  // duration like cc-switch: <= 24h buckets hourly, longer ranges
  // bucket by local day. The requested window is then snapped to
  // bucket boundaries (whole hours / local days) with an exclusive
  // end, and the same aligned window drives the backend query and the
  // chart's zero-fill so the two can never disagree.
  useEffect(() => {
    if (tab !== 'usage') {
      setUsageSeries([]);
      return;
    }
    let cancelled = false;
    setUsageLoading(true);
    let rawStartMs: number;
    let rawEndMs: number;
    if (usageRange === 'custom') {
      rawEndMs = usageLiveEnd ? Date.now() : customEndMs || Date.now();
      rawStartMs = customStartMs || rawEndMs - 7 * 86_400_000;
    } else {
      const window = resolveUsageRange(usageRange);
      rawStartMs = window.startMs;
      rawEndMs = window.endMs;
    }
    const granularity: 'hour' | 'day' =
      rawEndMs - rawStartMs <= 24 * 3_600_000 ? 'hour' : 'day';
    const window = alignUsageWindow(rawStartMs, rawEndMs, granularity);
    setUsageGranularity(granularity);
    setUsageStartMs(window.start);
    setUsageEndMs(window.end);
    setUsageSnapshotEndMs(rawEndMs);
    void api
      .modelUsageSeries(
        usageModel,
        granularity,
        -new Date().getTimezoneOffset(),
        new Date(window.start).toISOString(),
        new Date(window.end).toISOString(),
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
  }, [
    tab,
    usageModel,
    usageRange,
    usageReload,
    customStartMs,
    customEndMs,
    usageLiveEnd,
  ]);

  // enabledRows are the instances that participate in the router, in
  // priority order; only they can be reordered.
  const enabledRows = useMemo(() => rows.filter((r) => r.enabled), [rows]);

  const addInstance = (type: string) => {
    // Only a driver the deployment knows can become an instance; an
    // unknown id would be written as an unresolvable provider.
    // The new row goes to the top: the list is the router priority
    // order, and the deployment just added is the one being worked on.
    if (!catalog.some((p) => p.id === type)) return;
    setRows((prev) => [
      {
        id: newID(),
        stableId: '',
        type,
        driver: '',
        name: '',
        api: '',
        key: '',
        keyRef: '',
        keySet: false,
        keyEnv: false,
        keyKeychain: false,
        models: [emptyModelRow()],
        endpoint: '',
        advanced: {},
        enabled: true,
        managed: false,
      },
      ...prev,
    ]);
  };

  // addInstanceFromTemplate prefills one row from the built-in catalog.
  // The row is an ordinary user row from here on: the template fills the
  // provider, the endpoint, and the models, and every field stays
  // editable (including the credential, which no template carries). Like
  // a hand-added row it lands at the top of the priority list.
  const addInstanceFromTemplate = (template: InferenceCatalogTemplate) => {
    if (!catalog.some((p) => p.id === template.type)) return;
    const models = (template.models ?? []).map(rowModelFromSpec);
    setRows((prev) => [
      {
        id: newID(),
        stableId: '',
        type: template.type,
        driver: '',
        name: template.label,
        api: template.api ?? '',
        key: '',
        keyRef: '',
        keySet: false,
        keyEnv: false,
        keyKeychain: false,
        models: models.length > 0 ? models : [emptyModelRow()],
        endpoint: template.endpoint ?? '',
        advanced: template.advanced ?? {},
        enabled: true,
        managed: false,
      },
      ...prev,
    ]);
  };

  // catalogModelsFor lists the built-in models that belong to one
  // provider type, optionally filtered by the list's search box. Several
  // vendors share the OpenAI wire family, so the vendor label is what
  // tells them apart.
  const catalogModelsFor = (type: string, query = '') =>
    modelCatalog.filter(
      (m) => m.type === type && catalogEntryMatches(m, query),
    );

  // applyCatalogModel fills one model row from the catalog. The patch it
  // produces is typed as a model-only patch on purpose: picking a model
  // may never rewrite provider settings (the API surface, the endpoint,
  // the advanced knobs) — those stay the user's, and a model whose
  // declaration depends on one of them warns instead.
  const applyCatalogModel = (
    rowID: string,
    index: number,
    entry: InferenceCatalogModel,
  ) => {
    setRows((prev) =>
      prev.map((r) => {
        if (r.id !== rowID) return r;
        const patch: Pick<InstanceRow, 'models'> = {
          models: r.models.map((m, i) =>
            i === index ? rowModelFromSpec(entry.model) : m,
          ),
        };
        return { ...r, ...patch };
      }),
    );
    const row = rows.find((r) => r.id === rowID);
    if (!row || !declaresVideoInput(entry.model)) return;
    if (row.api !== 'chat') {
      toast(
        t('config.modelNeedsChatForVideo', { model: entry.model.name }),
        'warning',
      );
    } else if (row.advanced.video_input !== true) {
      toast(
        t('config.modelNeedsVideoInput', { model: entry.model.name }),
        'warning',
      );
    }
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

  // updateAdvanced patches one provider-level spec knob; an emptied
  // field is dropped from the object so the writer keeps the driver
  // default instead of pinning an empty value.
  const updateAdvanced = (
    id: string,
    key: keyof ProviderAdvanced,
    value: ProviderAdvanced[keyof ProviderAdvanced],
  ) => {
    setRows((prev) =>
      prev.map((r) => {
        if (r.id !== id) return r;
        const advanced: ProviderAdvanced = { ...r.advanced };
        if (value === '' || value === undefined) {
          delete advanced[key];
        } else {
          advanced[key] = value as never;
        }
        return { ...r, advanced };
      }),
    );
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
              models: [...r.models, emptyModelRow()],
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
    const instances: InstanceSpec[] = rows.map((r) => ({
      stable_id: r.stableId === '' ? undefined : r.stableId,
      type: r.type,
      name: r.name.trim() === '' ? undefined : r.name.trim(),
      driver: r.driver.trim() === '' ? undefined : r.driver.trim(),
      api: r.api === '' ? undefined : r.api,
      endpoint: r.endpoint.trim() === '' ? undefined : r.endpoint.trim(),
      ...credentialPayload(r),
      enabled: r.enabled,
      advanced: r.advanced,
      models: r.models.map((m) => ({
        name: m.name,
        kind: m.kind === '' ? undefined : m.kind,
        capabilities: {
          inputs: m.inputs,
          outputs: m.outputs,
          reasoning:
            m.reasoning === ''
              ? undefined
              : {
                  kind: m.reasoning,
                  effort_map:
                    Object.keys(m.reasoningEffortMap).length === 0
                      ? undefined
                      : m.reasoningEffortMap,
                },
          hosted_web_search: m.webSearch || undefined,
        },
        endpoint: m.endpoint.trim() === '' ? undefined : m.endpoint.trim(),
        limits: {
          max_input_tokens:
            m.maxInputTokens === '' ? undefined : m.maxInputTokens,
          max_output_tokens:
            m.maxOutputTokens === '' ? undefined : m.maxOutputTokens,
        },
        lifecycle: modelLifecyclePayload(m),
        driver_fields: driverFieldsPayload(m.specJson),
      })),
    }));
    setSaving(true);
    try {
      await api.saveInstances({ instances, router });
      toast(t('config.saved'));
      closeConfig();
    } catch (err) {
      setError(String(err));
    } finally {
      setSaving(false);
    }
  };

  const fmtUsageTokens = (n: number) => formatCompact(n);

  // A dash when the row cannot state a ratio at all: nothing measured
  // yet, or cache reads that overrun the prompt total (rows written
  // before the input column became inclusive — see lib/usageRate.ts).
  const usageHitLabel = (input: number, cacheRead: number) => {
    const label = formatHitPercent(cacheHitPercent({ input, cacheRead }));
    return label === undefined ? '—' : `${label}%`;
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

  const fmtClock = (ms: number) => {
    const d = new Date(ms);
    const pad = (n: number) => String(n).padStart(2, '0');
    return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(
      d.getHours(),
    )}:${pad(d.getMinutes())}`;
  };

  const usageRangeLabel = (): string => {
    if (usageRange !== 'custom') {
      if (usageRange === 'today') return t('config.usageRangeToday');
      return usageRange;
    }
    const end =
      usageLiveEnd || customEndMs === 0
        ? fmtClock(usageSnapshotEndMs || Date.now())
        : fmtClock(customEndMs);
    return customStartMs > 0
      ? `${fmtClock(customStartMs)} → ${end}`
      : t('config.usageCustom');
  };

  const applyUsageCustomRange = (
    startMs: number,
    endMs: number,
    liveEnd: boolean,
  ) => {
    setCustomStartMs(startMs);
    setCustomEndMs(endMs);
    setUsageLiveEnd(liveEnd);
    setUsageRange('custom');
  };

  const tabs: {
    id: Tab;
    label: string;
    icon: ComponentType<{ className?: string; size?: string | number }>;
  }[] = [
    { id: 'general', label: t('config.tabGeneral'), icon: SlidersHorizontal },
    { id: 'display', label: t('config.tabDisplay'), icon: Palette },
    { id: 'inference', label: t('config.tabInference'), icon: Cpu },
    { id: 'tools', label: t('config.tabTools'), icon: Wrench },
    { id: 'memory', label: t('config.tabMemory'), icon: Database },
    ...(yoloOnly
      ? []
      : [
          {
            id: 'permissions' as const,
            label: t('config.tabPermissions'),
            icon: ShieldCheck,
          },
        ]),
    { id: 'usage', label: t('config.tabUsage'), icon: BarChart3 },
    { id: 'diagnostics', label: t('config.tabDiagnostics'), icon: Stethoscope },
    { id: 'import', label: t('config.tabImport'), icon: Import },
  ];

  // A tab change clears the search so the nav flips back to the tab list,
  // and records the anchor to scroll to once the destination has rendered.
  const goTo = (id: Tab, anchor?: string) => {
    setTab(id);
    setError('');
    setQuery('');
    setPendingAnchor(anchor ?? null);
  };

  useEffect(() => {
    const content = contentRef.current;
    if (pendingAnchor === null) {
      // A tab-level jump lands at the top: the tab body is only as tall as
      // the destination, and keeping the previous tab's scroll offset would
      // drop the user into the middle of it.
      content?.scrollTo({ top: 0 });
      return;
    }
    // A dev-only row is not rendered until the switch is on; a search jump
    // has to reveal it instead of scrolling to nothing.
    if (DEV_ANCHORS.has(pendingAnchor) && !devMode) {
      setDevMode(true);
      return;
    }
    const node = document.getElementById(`settings-${pendingAnchor}`);
    const reduce = window.matchMedia?.(
      '(prefers-reduced-motion: reduce)',
    ).matches;
    node?.scrollIntoView({
      block: 'start',
      behavior: reduce === true ? 'auto' : 'smooth',
    });
    setPendingAnchor(null);
  }, [pendingAnchor, tab, devMode]);

  // Vertical tablists move with the arrow keys (WAI-ARIA tabs pattern);
  // focus follows selection so the next arrow keeps moving.
  const onTabKeyDown = (event: React.KeyboardEvent, index: number) => {
    const last = tabs.length - 1;
    let next = -1;
    if (event.key === 'ArrowDown') next = index === last ? 0 : index + 1;
    else if (event.key === 'ArrowUp') next = index === 0 ? last : index - 1;
    else if (event.key === 'Home') next = 0;
    else if (event.key === 'End') next = last;
    if (next < 0) return;
    event.preventDefault();
    goTo(tabs[next].id);
    tabRefs.current.get(tabs[next].id)?.focus();
  };

  const availableTabs = new Set(tabs.map((tb) => tb.id));
  const results =
    query.trim() === ''
      ? []
      : searchSettings(query, t).filter((entry) =>
          availableTabs.has(entry.tab as Tab),
        );

  return (
    <Overlay
      open={configOpen}
      onClose={closeConfig}
      ariaLabelledBy="settings-title"
      panelClassName="flex h-[45.7143rem] max-h-[calc(100vh-6.8571rem)] w-[68.5714rem] max-w-[calc(100vw-3.4286rem)] flex-col overflow-hidden rounded-card border border-edge bg-panel shadow-modal"
    >
      <header className="flex h-12 shrink-0 items-center gap-3 border-b border-edge px-4">
        <Settings size={ICON.md} className="text-accent" />
        <h2 id="settings-title" className="text-display font-semibold">
          {t('config.title')}
        </h2>
        <span className="flex-1" />
        <IconButton label={t('tools.close')} onClick={closeConfig}>
          <X size={ICON.md} />
        </IconButton>
      </header>

      <div className="flex min-h-0 flex-1">
        <nav
          className="flex w-60 shrink-0 flex-col border-r border-edge bg-panel"
          role="tablist"
          aria-orientation="vertical"
          aria-label={t('config.title')}
        >
          <div className="p-2 pb-1">
            <div
              ref={searchFieldRef}
              className="flex items-center gap-1.5 rounded-control border border-edge bg-panel2 px-2 focus-within:border-accent"
            >
              <Search size={ICON.xs} className="shrink-0 text-dim" />
              <input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && results.length > 0) {
                    goTo(results[0].tab as Tab, results[0].section);
                  }
                }}
                aria-label={t('config.searchPlaceholder')}
                placeholder={t('config.searchPlaceholder')}
                className="h-7 min-w-0 flex-1 bg-transparent text-xs outline-none"
              />
              <IconButton
                size="sm"
                label={t('config.searchClear')}
                onClick={() => setQuery('')}
              >
                <X size={ICON.xs} />
              </IconButton>
            </div>
          </div>
          <div className="min-h-0 flex-1 space-y-0.5 overflow-y-auto p-2 pt-1">
            {query.trim() === ''
              ? tabs.map((tb, index) => {
                  const Icon = tb.icon;
                  const active = tab === tb.id;
                  return (
                    <button
                      key={tb.id}
                      ref={(node) => {
                        if (node === null) tabRefs.current.delete(tb.id);
                        else tabRefs.current.set(tb.id, node);
                      }}
                      id={`settings-tab-${tb.id}`}
                      role="tab"
                      aria-selected={active}
                      aria-controls="settings-content"
                      tabIndex={active ? 0 : -1}
                      onClick={() => goTo(tb.id)}
                      onKeyDown={(e) => onTabKeyDown(e, index)}
                      className={`flex w-full items-center gap-2 rounded-control px-3 py-2 text-left text-sm transition-colors ${
                        active
                          ? 'bg-accent/15 font-medium text-accent'
                          : 'text-dim hover:bg-panel2 hover:text-fg'
                      }`}
                    >
                      <Icon size={ICON.md} className="shrink-0" />
                      <span className="truncate">{tb.label}</span>
                    </button>
                  );
                })
              : results.map((entry) => {
                  const tabMeta = tabs.find((tb) => tb.id === entry.tab);
                  const Icon = tabMeta?.icon ?? Settings;
                  return (
                    <button
                      key={`${entry.tab}-${entry.section ?? 'tab'}`}
                      onClick={() => goTo(entry.tab as Tab, entry.section)}
                      className="flex w-full items-start gap-2 rounded-control px-3 py-2 text-left transition-colors hover:bg-panel2"
                    >
                      <Icon
                        size={ICON.md}
                        className="mt-0.5 shrink-0 text-faint"
                      />
                      <span className="min-w-0">
                        <span className="block truncate text-sm text-fg">
                          {t(entry.labelKey)}
                        </span>
                        {entry.section !== undefined && (
                          <span className="block truncate text-label text-faint">
                            {tabMeta?.label ?? ''}
                          </span>
                        )}
                      </span>
                    </button>
                  );
                })}
            {query.trim() !== '' && results.length === 0 && (
              <EmptyState
                size="sm"
                icon={Search}
                title={t('config.searchEmpty')}
                hint={t('config.searchHint')}
              />
            )}
          </div>
        </nav>

        <div
          ref={contentRef}
          id="settings-content"
          role="tabpanel"
          aria-labelledby={`settings-tab-${tab}`}
          className="min-w-0 flex-1 overflow-y-auto px-6 py-5"
        >
          {tab === 'general' && <SettingsGeneral />}
          {tab === 'display' && <SettingsDisplay />}
          {tab === 'tools' && (
            <div className="space-y-4">
              <div className="space-y-2">
                <div id="settings-tools-providers" className="scroll-mt-4">
                  <ToolsSection />
                </div>
                <div id="settings-tools-websearch" className="scroll-mt-4">
                  <WebSearchSection />
                </div>
                {/* The delegation policy: limits and curated targets,
                    enforced on every delegate call. */}
                <div id="settings-tools-delegation" className="scroll-mt-4">
                  <DelegationCard />
                </div>
              </div>
              <div id="settings-tools-mcp" className="scroll-mt-4">
                <MCPSection />
              </div>
            </div>
          )}

          {tab === 'inference' && (
            <div
              id="settings-inference-instances"
              className="scroll-mt-4 space-y-3"
            >
              <p className="text-xs text-dim">
                {defaultModel
                  ? t('config.inferenceCurrent', { model: defaultModel })
                  : t('setup.subtitle')}
              </p>
              <div className="flex items-center gap-2">
                <button
                  type="button"
                  data-field="add-driver"
                  aria-label={t('config.driverPicker')}
                  onFocus={(e) => openFieldMenu(e, 'add-driver')}
                  onClick={(e) => {
                    if (fieldMenu?.key === 'add-driver') {
                      setFieldMenu(null);
                    } else {
                      openFieldMenu(e, 'add-driver');
                    }
                  }}
                  className="inline-flex min-w-48 max-w-64 items-center gap-1.5 rounded-control border border-edge bg-panel px-3 py-1.5 text-sm transition-colors outline-none hover:border-accent/60 hover:text-fg focus:border-accent"
                >
                  <span className="min-w-0 flex-1 truncate text-fg">
                    {catalog.find((p) => p.id === newType)?.name ?? ''}
                  </span>
                  <ChevronDown size={ICON.sm} className="shrink-0 text-dim" />
                </button>
                {
                  <Popover
                    open={fieldMenu?.key === 'add-driver'}
                    onClose={closeFieldMenu}
                    anchor={fieldMenu?.anchor ?? null}
                    role="none"
                    align="start"
                    matchWidth
                    panelClassName="min-w-48 rounded-card border border-edge bg-panel py-1 shadow-popover"
                  >
                    {catalog.map((p) => (
                      <button
                        key={p.id}
                        type="button"
                        onMouseDown={(e) => e.preventDefault()}
                        onClick={() => {
                          setNewType(p.id);
                          setFieldMenu(null);
                        }}
                        className={`flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-xs hover:bg-panel2 ${
                          newType === p.id ? 'text-fg' : 'text-dim'
                        }`}
                      >
                        <Check
                          size={ICON.xs}
                          className={`shrink-0 ${
                            newType === p.id ? 'text-accent' : 'invisible'
                          }`}
                        />
                        <span className="truncate">{p.name}</span>
                      </button>
                    ))}
                  </Popover>
                }
                <button
                  onClick={() => addInstance(newType)}
                  className="flex items-center gap-1.5 rounded-control border border-edge px-3 py-1.5 text-sm text-dim hover:text-fg"
                >
                  <Plus size={ICON.sm} />
                  {t('config.addInstance')}
                </button>
              </div>
              {templates.length > 0 && (
                <div className="space-y-2 rounded-card border border-edge/70 bg-panel p-3">
                  <div className="flex items-center justify-between gap-2">
                    <div className="flex items-center gap-1.5 text-xs font-medium text-dim">
                      <Sparkles
                        size={ICON.xs}
                        className="shrink-0 text-accent"
                      />
                      <span>{t('config.templatesGroup')}</span>
                    </div>
                    <input
                      value={templateQuery}
                      onChange={(e) => setTemplateQuery(e.target.value)}
                      placeholder={t('config.templateSearchPlaceholder')}
                      aria-label={t('config.templateSearch')}
                      className="w-44 rounded-control border border-edge bg-panel px-2 py-0.5 text-xs outline-none focus:border-accent"
                    />
                  </div>
                  {templates.filter((template) =>
                    templateMatches(template, templateQuery),
                  ).length === 0 && (
                    <p className="text-xs text-dim">
                      {t('config.templateSearchEmpty')}
                    </p>
                  )}
                  <div className="flex flex-wrap gap-1.5">
                    {templates
                      .filter((template) =>
                        templateMatches(template, templateQuery),
                      )
                      .map((template) => {
                        const models = (template.models ?? [])
                          .map((m) => m.name)
                          .join(', ');
                        return (
                          <button
                            key={template.id}
                            type="button"
                            aria-label={`${template.label}: ${models}`}
                            onClick={() => addInstanceFromTemplate(template)}
                            className="group inline-flex max-w-full items-center gap-1.5 rounded-full border border-edge bg-panel2 py-1 pl-2 pr-2.5 text-xs text-dim transition-colors hover:border-accent/60 hover:bg-accent/10 hover:text-fg focus-visible:border-accent focus-visible:outline-none"
                          >
                            <Plus
                              size={ICON.xs}
                              className="shrink-0 text-accent transition-colors group-hover:text-accent"
                            />
                            <span className="shrink-0 font-medium text-fg">
                              {template.label}
                            </span>
                            <span className="min-w-0 max-w-40 truncate font-mono text-micro text-dim">
                              {models}
                            </span>
                          </button>
                        );
                      })}
                  </div>
                </div>
              )}
              {rows.length === 0 && (
                <p className="text-sm text-dim">{t('config.instancesEmpty')}</p>
              )}
              {rows.map((row, ri) => {
                const prov = catalog.find((p) => p.id === row.type);
                // The effective driver impl: a preset's impl, or the
                // driver a plugin-declared row names.
                const driverImpl = prov?.impl || row.driver;
                return (
                  <div
                    key={row.id}
                    className={`rounded-card border overflow-hidden ${
                      row.enabled
                        ? 'border-edge bg-panel2'
                        : 'border-edge/50 bg-panel2'
                    }`}
                  >
                    <div className="flex items-center gap-2.5 px-4 py-2.5">
                      <input
                        type="checkbox"
                        checked={row.enabled}
                        onChange={(e) =>
                          update(row.id, { enabled: e.target.checked })
                        }
                        data-tip={t('config.instanceEnabled')}
                      />
                      <span className="font-medium text-sm shrink-0">
                        {prov?.name ?? row.type}
                      </span>
                      {row.driver !== '' && (
                        <span
                          className="shrink-0 rounded-tight bg-panel px-1.5 py-0.5 text-micro text-dim"
                          data-tip={t('config.instanceDriver')}
                        >
                          {row.driver}
                        </span>
                      )}
                      {row.managed && (
                        <span className="shrink-0 rounded-tight bg-panel px-1.5 py-0.5 text-micro text-dim">
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
                        className="flex-1 min-w-0 rounded-control border border-edge bg-panel px-2 py-1 text-sm outline-none focus:border-accent"
                      />
                      <button
                        onClick={() => moveInstance(row.id, -1)}
                        disabled={ri === 0}
                        className="text-dim hover:text-fg disabled:opacity-30 shrink-0"
                        data-tip={t('config.moveUp')}
                        aria-label={t('config.moveUp')}
                      >
                        <ArrowUp size={ICON.sm} />
                      </button>
                      <button
                        onClick={() => moveInstance(row.id, 1)}
                        disabled={ri === rows.length - 1}
                        className="text-dim hover:text-fg disabled:opacity-30 shrink-0"
                        data-tip={t('config.moveDown')}
                        aria-label={t('config.moveDown')}
                      >
                        <ArrowDown size={ICON.sm} />
                      </button>
                      {!row.managed && (
                        <button
                          onClick={() =>
                            setRows((prev) =>
                              prev.filter((r) => r.id !== row.id),
                            )
                          }
                          className="text-dim hover:text-err shrink-0"
                          data-tip={t('config.removeInstance')}
                        >
                          <Trash2 size={ICON.sm} />
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
                            className="w-full rounded-control border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
                          />
                          {/* responses | chat is an OpenAI-wire surface;
                                other drivers have no such choice. */}
                          {driverImpl === 'openai' && (
                            <>
                              <div className="flex items-center gap-2 text-xs text-dim">
                                <span className="shrink-0 font-medium">
                                  {t('setup.apiMode')}
                                </span>
                                <button
                                  type="button"
                                  data-field={`${row.id}:api`}
                                  disabled={row.managed}
                                  onFocus={(e) =>
                                    openFieldMenu(e, `${row.id}:api`)
                                  }
                                  onClick={(e) => {
                                    const key = `${row.id}:api`;
                                    if (fieldMenu?.key === key) {
                                      setFieldMenu(null);
                                    } else {
                                      openFieldMenu(e, key);
                                    }
                                  }}
                                  className="inline-flex max-w-56 min-w-0 flex-1 items-center gap-1.5 rounded-control border border-edge bg-panel px-2 py-1 text-xs transition-colors outline-none hover:border-accent/60 hover:text-fg focus:border-accent disabled:opacity-40"
                                >
                                  <span className="min-w-0 flex-1 truncate font-mono text-fg">
                                    {row.api === '' ? 'auto' : row.api}
                                  </span>
                                  <ChevronDown
                                    size={ICON.xs}
                                    className="shrink-0 text-dim"
                                  />
                                </button>
                              </div>
                              {!row.managed && (
                                <Popover
                                  open={fieldMenu?.key === `${row.id}:api`}
                                  onClose={closeFieldMenu}
                                  anchor={fieldMenu?.anchor ?? null}
                                  role="none"
                                  align="start"
                                  matchWidth
                                  panelClassName="rounded-card border border-edge bg-panel py-1 shadow-popover"
                                >
                                  {[
                                    ['responses', 'responses'],
                                    ['chat', 'chat'],
                                  ].map(([value, label]) => (
                                    <button
                                      key={value}
                                      type="button"
                                      onMouseDown={(e) => e.preventDefault()}
                                      onClick={() => {
                                        update(row.id, { api: value });
                                        setFieldMenu(null);
                                      }}
                                      className={`flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-xs hover:bg-panel2 ${
                                        row.api === value
                                          ? 'text-fg'
                                          : 'text-dim'
                                      }`}
                                    >
                                      <Check
                                        size={ICON.xs}
                                        className={`shrink-0 ${
                                          row.api === value
                                            ? 'text-accent'
                                            : 'invisible'
                                        }`}
                                      />
                                      <span className="font-mono">{label}</span>
                                    </button>
                                  ))}
                                </Popover>
                              )}
                            </>
                          )}
                        </div>
                        <AdvancedSection
                          row={row}
                          disabled={row.managed}
                          driver={driverImpl}
                          onUpdate={(key, value) =>
                            updateAdvanced(row.id, key, value)
                          }
                        />
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
                                <Plus size={ICON.xs} />
                                {t('config.addModel')}
                              </button>
                            )}
                          </div>
                          {row.models.map((m, mi) => (
                            <div
                              key={mi}
                              className="space-y-3 rounded-card border border-edge bg-panel p-3"
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
                                    placeholder={t('setup.model')}
                                    className="w-full rounded-control border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
                                  />
                                </div>
                                {!row.managed &&
                                  catalogModelsFor(row.type).length > 0 && (
                                    <button
                                      type="button"
                                      data-field={`${row.id}:${mi}:model`}
                                      aria-label={t('config.modelFromCatalog')}
                                      data-tip={t('config.modelFromCatalog')}
                                      onFocus={(e) => {
                                        setCatalogQuery('');
                                        openFieldMenu(
                                          e,
                                          `${row.id}:${mi}:model`,
                                        );
                                      }}
                                      onClick={(e) => {
                                        const key = `${row.id}:${mi}:model`;
                                        if (fieldMenu?.key === key) {
                                          setFieldMenu(null);
                                        } else {
                                          setCatalogQuery('');
                                          openFieldMenu(e, key);
                                        }
                                      }}
                                      className="shrink-0 rounded-control border border-edge px-1.5 py-1.5 text-dim transition-colors outline-none hover:border-accent/60 hover:text-fg focus:border-accent"
                                    >
                                      <ListPlus size={ICON.sm} />
                                    </button>
                                  )}
                                {!row.managed && (
                                  <Popover
                                    open={
                                      fieldMenu?.key === `${row.id}:${mi}:model`
                                    }
                                    onClose={closeFieldMenu}
                                    anchor={fieldMenu?.anchor ?? null}
                                    role="none"
                                    align="end"
                                    maxHeight={288}
                                    panelClassName="min-w-[16rem] rounded-card border border-edge bg-panel py-1 shadow-popover"
                                  >
                                    <div className="sticky top-0 z-[var(--oc-z-raised)] bg-panel px-2 pb-1 pt-1">
                                      <input
                                        autoFocus
                                        value={catalogQuery}
                                        onChange={(e) =>
                                          setCatalogQuery(e.target.value)
                                        }
                                        placeholder={t(
                                          'config.modelSearchPlaceholder',
                                        )}
                                        aria-label={t('config.modelSearch')}
                                        className="w-full rounded-control border border-edge bg-panel px-2 py-1 text-xs outline-none focus:border-accent"
                                      />
                                    </div>
                                    {catalogModelsFor(
                                      row.type,
                                      catalogQuery,
                                    ).map((entry) => (
                                      <button
                                        key={entry.id}
                                        type="button"
                                        onMouseDown={(e) => e.preventDefault()}
                                        onClick={() => {
                                          applyCatalogModel(row.id, mi, entry);
                                          setFieldMenu(null);
                                        }}
                                        className="flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-xs text-dim hover:bg-panel2 hover:text-fg"
                                      >
                                        <span className="min-w-0 flex-1 truncate font-mono">
                                          {entry.model.name}
                                        </span>
                                        <span className="shrink-0 truncate text-micro text-dim">
                                          {entry.label ?? entry.vendor ?? ''}
                                        </span>
                                      </button>
                                    ))}
                                    {catalogModelsFor(row.type, catalogQuery)
                                      .length === 0 && (
                                      <p className="px-2.5 py-1.5 text-xs text-dim">
                                        {t('config.modelSearchEmpty')}
                                      </p>
                                    )}
                                  </Popover>
                                )}
                                <button
                                  onClick={() => moveModel(row.id, mi, -1)}
                                  disabled={mi === 0}
                                  className="shrink-0 text-dim hover:text-fg disabled:opacity-30"
                                  data-tip={t('config.moveUp')}
                                  aria-label={t('config.moveUp')}
                                >
                                  <ArrowUp size={ICON.sm} />
                                </button>
                                <button
                                  onClick={() => moveModel(row.id, mi, 1)}
                                  disabled={mi === row.models.length - 1}
                                  className="shrink-0 text-dim hover:text-fg disabled:opacity-30"
                                  data-tip={t('config.moveDown')}
                                  aria-label={t('config.moveDown')}
                                >
                                  <ArrowDown size={ICON.sm} />
                                </button>
                                {!row.managed && (
                                  <button
                                    onClick={() => removeModel(row.id, mi)}
                                    className="shrink-0 text-dim hover:text-err"
                                    data-tip={t('config.removeModel')}
                                    aria-label={t('config.removeModel')}
                                  >
                                    <X size={ICON.sm} />
                                  </button>
                                )}
                              </div>
                              <div className="space-y-3 text-xs">
                                <div className="grid grid-cols-1 gap-x-4 gap-y-2.5 sm:grid-cols-2 xl:grid-cols-4">
                                  <div className="min-w-0 space-y-1">
                                    <span className="block text-xs font-medium text-dim">
                                      {t('setup.outputs')}
                                    </span>
                                    <button
                                      type="button"
                                      data-field={`${row.id}:${mi}:outputs`}
                                      disabled={row.managed}
                                      onFocus={(e) =>
                                        openFieldMenu(
                                          e,
                                          `${row.id}:${mi}:outputs`,
                                        )
                                      }
                                      onClick={(e) => {
                                        const key = `${row.id}:${mi}:outputs`;
                                        if (fieldMenu?.key === key) {
                                          setFieldMenu(null);
                                        } else {
                                          openFieldMenu(e, key);
                                        }
                                      }}
                                      className="flex min-w-0 w-full items-center gap-1.5 rounded-control border border-edge bg-panel px-2 py-1.5 text-xs transition-colors outline-none hover:border-accent/60 hover:text-fg focus:border-accent disabled:opacity-40"
                                    >
                                      <span className="min-w-0 flex-1 truncate font-mono text-fg">
                                        {m.outputs.length > 0
                                          ? m.outputs.join(', ')
                                          : '—'}
                                      </span>
                                      <ChevronDown
                                        size={ICON.xs}
                                        className="shrink-0 text-dim"
                                      />
                                    </button>
                                  </div>
                                  <div className="min-w-0 space-y-1">
                                    <span className="block text-xs font-medium text-dim">
                                      {t('setup.inputs')}
                                    </span>
                                    <button
                                      type="button"
                                      data-field={`${row.id}:${mi}:inputs`}
                                      disabled={row.managed}
                                      onFocus={(e) =>
                                        openFieldMenu(
                                          e,
                                          `${row.id}:${mi}:inputs`,
                                        )
                                      }
                                      onClick={(e) => {
                                        const key = `${row.id}:${mi}:inputs`;
                                        if (fieldMenu?.key === key) {
                                          setFieldMenu(null);
                                        } else {
                                          openFieldMenu(e, key);
                                        }
                                      }}
                                      className="flex min-w-0 w-full items-center gap-1.5 rounded-control border border-edge bg-panel px-2 py-1.5 text-xs transition-colors outline-none hover:border-accent/60 hover:text-fg focus:border-accent disabled:opacity-40"
                                    >
                                      <span className="min-w-0 flex-1 truncate font-mono text-fg">
                                        {m.inputs.length > 0
                                          ? m.inputs.join(', ')
                                          : '—'}
                                      </span>
                                      <ChevronDown
                                        size={ICON.xs}
                                        className="shrink-0 text-dim"
                                      />
                                    </button>
                                  </div>
                                  <div className="min-w-0 space-y-1">
                                    <span className="block text-xs font-medium text-dim">
                                      {t('setup.kind')}
                                    </span>
                                    <button
                                      type="button"
                                      data-field={`${row.id}:${mi}:kind`}
                                      disabled={row.managed}
                                      onFocus={(e) =>
                                        openFieldMenu(e, `${row.id}:${mi}:kind`)
                                      }
                                      onClick={(e) => {
                                        const key = `${row.id}:${mi}:kind`;
                                        if (fieldMenu?.key === key) {
                                          setFieldMenu(null);
                                        } else {
                                          openFieldMenu(e, key);
                                        }
                                      }}
                                      className="flex min-w-0 w-full items-center gap-1.5 rounded-control border border-edge bg-panel px-2 py-1.5 text-xs transition-colors outline-none hover:border-accent/60 hover:text-fg focus:border-accent disabled:opacity-40"
                                    >
                                      <span className="min-w-0 flex-1 truncate text-fg">
                                        {m.kind === '' ? 'auto' : m.kind}
                                      </span>
                                      <ChevronDown
                                        size={ICON.xs}
                                        className="shrink-0 text-dim"
                                      />
                                    </button>
                                  </div>
                                  <div className="min-w-0 space-y-1">
                                    <span className="block text-xs font-medium text-dim">
                                      {t('setup.reasoning')}
                                    </span>
                                    <button
                                      type="button"
                                      data-field={`${row.id}:${mi}:reasoning`}
                                      disabled={row.managed}
                                      onFocus={(e) =>
                                        openFieldMenu(
                                          e,
                                          `${row.id}:${mi}:reasoning`,
                                        )
                                      }
                                      onClick={(e) => {
                                        const key = `${row.id}:${mi}:reasoning`;
                                        if (fieldMenu?.key === key) {
                                          setFieldMenu(null);
                                        } else {
                                          openFieldMenu(e, key);
                                        }
                                      }}
                                      className="flex min-w-0 w-full items-center gap-1.5 rounded-control border border-edge bg-panel px-2 py-1.5 text-xs transition-colors outline-none hover:border-accent/60 hover:text-fg focus:border-accent disabled:opacity-40"
                                    >
                                      <span className="min-w-0 flex-1 truncate text-fg">
                                        {m.reasoning === ''
                                          ? t('setup.reasoningOff')
                                          : m.reasoning}
                                      </span>
                                      <ChevronDown
                                        size={ICON.xs}
                                        className="shrink-0 text-dim"
                                      />
                                    </button>
                                  </div>
                                </div>
                                {m.reasoning !== '' && (
                                  <div className="rounded-card border border-edge bg-panel2 px-3 py-2.5">
                                    <div className="mb-2 flex items-center justify-between gap-2">
                                      <span className="text-label font-semibold tracking-wider text-dim uppercase">
                                        {t('setup.effortMap')}
                                      </span>
                                      <button
                                        type="button"
                                        disabled={row.managed}
                                        onClick={() =>
                                          updateModel(row.id, mi, {
                                            reasoningEffortMap: {},
                                          })
                                        }
                                        className="rounded-control border border-edge px-2 py-0.5 text-xs text-dim transition-colors hover:text-fg disabled:opacity-50"
                                      >
                                        {t('setup.effortMapClear')}
                                      </button>
                                    </div>
                                    <div className="grid grid-cols-1 gap-x-8 gap-y-1.5 sm:grid-cols-2 lg:grid-cols-3">
                                      {EFFORT_LEVELS.map((level) => (
                                        <label
                                          key={level}
                                          className="flex items-center gap-1.5"
                                        >
                                          <span className="w-12 shrink-0 text-right font-mono text-dim">
                                            {level}
                                          </span>
                                          <span className="shrink-0 text-dim">
                                            →
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
                                            className="min-w-0 flex-1 rounded-control border border-edge bg-panel px-1.5 py-1 text-xs outline-none focus:border-accent"
                                          />
                                        </label>
                                      ))}
                                    </div>
                                  </div>
                                )}
                                <div className="flex flex-wrap items-center gap-x-5 gap-y-2 border-t border-edge pt-2.5">
                                  <label
                                    className="flex items-center gap-1.5 whitespace-nowrap text-dim hover:text-fg"
                                    data-tip={t('setup.webSearchHint')}
                                  >
                                    <input
                                      type="checkbox"
                                      checked={m.webSearch}
                                      disabled={row.managed}
                                      onChange={(e) =>
                                        updateModel(row.id, mi, {
                                          webSearch: e.target.checked,
                                        })
                                      }
                                    />
                                    {t('setup.webSearch')}
                                  </label>
                                  {m.webSearch && (
                                    <span className="max-w-96 text-micro text-dim">
                                      {t('setup.webSearchHint')}
                                    </span>
                                  )}
                                  <label className="flex items-center gap-2 whitespace-nowrap">
                                    <span className="font-medium text-dim">
                                      {t('setup.maxInputTokens')}
                                    </span>
                                    <NumberField
                                      allowEmpty
                                      size="sm"
                                      min={1}
                                      step={1000}
                                      value={m.maxInputTokens}
                                      disabled={row.managed}
                                      placeholder={t('setup.maxInputAuto')}
                                      data-tip={t('setup.maxInputHint')}
                                      label={t('setup.maxInputTokens')}
                                      onChange={(next) =>
                                        updateModel(row.id, mi, {
                                          maxInputTokens: next,
                                        })
                                      }
                                      className="w-40"
                                    />
                                  </label>
                                </div>
                              </div>
                              {!row.managed && (
                                <Popover
                                  open={
                                    fieldMenu !== null &&
                                    (fieldMenu.key ===
                                      `${row.id}:${mi}:outputs` ||
                                      fieldMenu.key ===
                                        `${row.id}:${mi}:inputs`)
                                  }
                                  onClose={closeFieldMenu}
                                  anchor={fieldMenu?.anchor ?? null}
                                  role="none"
                                  align="start"
                                  matchWidth
                                  maxHeight={224}
                                  panelClassName="rounded-card border border-edge bg-panel py-1 shadow-popover"
                                >
                                  {(fieldMenu?.key === `${row.id}:${mi}:outputs`
                                    ? ['text', 'image', 'audio', 'video']
                                    : [
                                        'text',
                                        'image',
                                        'audio',
                                        'video',
                                        'file',
                                        'data',
                                        'tool_call',
                                        'tool_result',
                                      ]
                                  ).map((opt) => {
                                    const active =
                                      fieldMenu?.key ===
                                      `${row.id}:${mi}:outputs`
                                        ? m.outputs.includes(opt)
                                        : m.inputs.includes(opt);
                                    return (
                                      <button
                                        key={opt}
                                        type="button"
                                        onMouseDown={(e) => e.preventDefault()}
                                        onClick={() => {
                                          if (
                                            fieldMenu?.key ===
                                            `${row.id}:${mi}:outputs`
                                          ) {
                                            updateModel(row.id, mi, {
                                              outputs: active
                                                ? m.outputs.filter(
                                                    (k) => k !== opt,
                                                  )
                                                : [...m.outputs, opt],
                                            });
                                          } else {
                                            updateModel(row.id, mi, {
                                              inputs: active
                                                ? m.inputs.filter(
                                                    (k) => k !== opt,
                                                  )
                                                : [...m.inputs, opt],
                                            });
                                          }
                                        }}
                                        className={`flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-xs hover:bg-panel2 ${
                                          active ? 'text-fg' : 'text-dim'
                                        }`}
                                      >
                                        <Check
                                          size={ICON.xs}
                                          className={`shrink-0 ${
                                            active ? 'text-accent' : 'invisible'
                                          }`}
                                        />
                                        <span className="font-mono">{opt}</span>
                                      </button>
                                    );
                                  })}
                                </Popover>
                              )}
                              {!row.managed && (
                                <Popover
                                  open={
                                    fieldMenu?.key === `${row.id}:${mi}:kind`
                                  }
                                  onClose={closeFieldMenu}
                                  anchor={fieldMenu?.anchor ?? null}
                                  role="none"
                                  align="start"
                                  matchWidth
                                  panelClassName="rounded-card border border-edge bg-panel py-1 shadow-popover"
                                >
                                  {[
                                    ['', 'auto'],
                                    ['generate', 'generate'],
                                    ['image', 'image'],
                                    ['video', 'video'],
                                    ['tts', 'tts'],
                                  ].map(([value, label]) => (
                                    <button
                                      key={value}
                                      type="button"
                                      onMouseDown={(e) => e.preventDefault()}
                                      onClick={() => {
                                        updateModel(row.id, mi, {
                                          kind: value,
                                        });
                                        setFieldMenu(null);
                                      }}
                                      className={`flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-xs hover:bg-panel2 ${
                                        m.kind === value
                                          ? 'text-fg'
                                          : 'text-dim'
                                      }`}
                                    >
                                      <Check
                                        size={ICON.xs}
                                        className={`shrink-0 ${
                                          m.kind === value
                                            ? 'text-accent'
                                            : 'invisible'
                                        }`}
                                      />
                                      <span className="font-mono">{label}</span>
                                    </button>
                                  ))}
                                </Popover>
                              )}
                              {!row.managed && (
                                <Popover
                                  open={
                                    fieldMenu?.key ===
                                    `${row.id}:${mi}:reasoning`
                                  }
                                  onClose={closeFieldMenu}
                                  anchor={fieldMenu?.anchor ?? null}
                                  role="none"
                                  align="start"
                                  matchWidth
                                  panelClassName="rounded-card border border-edge bg-panel py-1 shadow-popover"
                                >
                                  {[
                                    ['', t('setup.reasoningOff')],
                                    ['always', 'always'],
                                    ['toggle', 'toggle'],
                                  ].map(([value, label]) => (
                                    <button
                                      key={value}
                                      type="button"
                                      onMouseDown={(e) => e.preventDefault()}
                                      onClick={() => {
                                        updateModel(row.id, mi, {
                                          reasoning: value,
                                          ...(value === ''
                                            ? { reasoningEffortMap: {} }
                                            : {}),
                                        });
                                        setFieldMenu(null);
                                      }}
                                      className={`flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-xs hover:bg-panel2 ${
                                        m.reasoning === value
                                          ? 'text-fg'
                                          : 'text-dim'
                                      }`}
                                    >
                                      <Check
                                        size={ICON.xs}
                                        className={`shrink-0 ${
                                          m.reasoning === value
                                            ? 'text-accent'
                                            : 'invisible'
                                        }`}
                                      />
                                      <span
                                        className={
                                          value === '' ? undefined : 'font-mono'
                                        }
                                      >
                                        {label}
                                      </span>
                                    </button>
                                  ))}
                                </Popover>
                              )}
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
                                  className="w-full rounded-control border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
                                />
                              )}
                              <ModelAdvanced
                                value={m}
                                disabled={row.managed}
                                onUpdate={(patch) =>
                                  updateModel(row.id, mi, patch)
                                }
                              />
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
                              className="flex-1 rounded-control border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent disabled:opacity-40"
                            />
                            <label className="flex items-center gap-1.5 text-xs text-dim whitespace-nowrap">
                              <input
                                type="checkbox"
                                checked={row.keyEnv}
                                disabled={row.managed}
                                onChange={(e) =>
                                  update(row.id, { keyEnv: e.target.checked })
                                }
                              />
                              {t('setup.envVar', {
                                var: prov?.env_var ?? '',
                              })}
                            </label>
                          </div>
                          {/* Changing a key (or moving to the env var)
                                puts this instance on a different
                                credential, and provider prompt caches are
                                scoped to it: the next request re-reads the
                                whole conversation at undiscounted input
                                price. Saying so here costs one line; the
                                user would otherwise read it as a bug. */}
                          <p className="text-micro leading-relaxed text-faint">
                            {t('config.inferenceKeyCacheHint')}
                          </p>
                        </div>
                      </div>
                    )}
                  </div>
                );
              })}

              <div
                id="settings-inference-router"
                className="scroll-mt-4 rounded-card border border-edge bg-panel2 p-3"
              >
                <div className="text-xs text-dim mb-2">
                  {t('config.router.title')}
                </div>
                <div className="grid grid-cols-1 gap-2 md:grid-cols-2">
                  <label className="flex flex-col gap-1">
                    <span className="text-xs text-dim">
                      {t('config.router.maxAttempts')}
                    </span>
                    <NumberField
                      min={1}
                      value={router.max_attempts}
                      onChange={(next) => {
                        if (next === '') return;
                        setRouter({ ...router, max_attempts: next });
                      }}
                      className="w-full"
                    />
                  </label>
                  <label className="flex items-center gap-2 pt-4">
                    <input
                      type="checkbox"
                      checked={router.fallback_on_retry_exhausted}
                      onChange={(e) =>
                        setRouter({
                          ...router,
                          fallback_on_retry_exhausted: e.target.checked,
                        })
                      }
                    />
                    <span className="text-xs text-dim">
                      {t('config.router.fallback')}
                    </span>
                  </label>
                </div>
              </div>

              {enabledRows.length > 1 && (
                <div className="rounded-card border border-edge bg-panel2 p-3">
                  <div className="text-xs text-dim mb-2">
                    {t('setup.routerPriority')}
                  </div>
                  <div className="space-y-1.5">
                    {enabledRows.map((row, idx) => (
                      <div
                        key={row.id}
                        className="flex items-center gap-2 rounded-control border border-edge bg-panel px-3 py-1.5 text-sm"
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
                          data-tip={t('config.moveUp')}
                          aria-label={t('config.moveUp')}
                        >
                          <ArrowUp size={ICON.sm} />
                        </button>
                        <button
                          onClick={() => move(idx, 1)}
                          disabled={idx === enabledRows.length - 1}
                          className="text-dim hover:text-fg disabled:opacity-30"
                          data-tip={t('config.moveDown')}
                          aria-label={t('config.moveDown')}
                        >
                          <ArrowDown size={ICON.sm} />
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
                    void Promise.all([
                      api.modelUsage(),
                      api.modelUsageSessionCount(),
                    ])
                      .then(([rows, sessions]) => {
                        setUsageRows(rows);
                        setUsageSessions(sessions);
                      })
                      .catch((err) => setUsageError(String(err)));
                    setUsageReload((n) => n + 1);
                  }}
                  disabled={usageLoading}
                  className="flex items-center gap-1.5 rounded-control border border-edge px-2.5 py-1.5 text-xs text-dim transition-colors hover:text-fg disabled:opacity-50"
                  aria-label={t('config.logsRefresh')}
                >
                  {usageLoading ? (
                    <Loader2 size={ICON.sm} className="animate-spin" />
                  ) : (
                    <RefreshCw size={ICON.sm} />
                  )}
                  {t('config.logsRefresh')}
                </button>
              </div>
              {usageError && <p className="text-xs text-err">{usageError}</p>}
              {usageLoading && usageRows.length === 0 ? (
                <div className="h-72 animate-pulse rounded-card border border-edge/70 bg-panel" />
              ) : usageRows.length > 0 ? (
                <UsageHero rows={usageRows} sessions={usageSessions} />
              ) : null}
              {usageRows.length === 0 ? (
                <p className="text-sm text-dim">{t('config.usageEmpty')}</p>
              ) : (
                <>
                  <div className="flex flex-wrap items-center gap-2">
                    <UsageModelSelect
                      value={usageModel}
                      options={[
                        {
                          value: '',
                          label: t('config.usageAllModels'),
                        },
                        ...usageRows.map((r) => ({
                          value: r.model,
                          label: r.model,
                        })),
                      ]}
                      onChange={setUsageModel}
                      title={t('config.usageModel')}
                    />
                    <div className="flex items-center rounded-control border border-edge bg-panel p-1">
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
                          className={`h-7 rounded-control px-2.5 text-xs transition-colors ${
                            usageRange === value
                              ? 'bg-panel2 text-accent shadow-raised'
                              : 'text-dim hover:text-fg'
                          }`}
                        >
                          {label}
                        </button>
                      ))}
                    </div>
                    <UsageRangePicker
                      active={usageRange === 'custom'}
                      startMs={customStartMs}
                      endMs={customEndMs}
                      liveEnd={usageLiveEnd}
                      onApply={applyUsageCustomRange}
                    />
                    {usageLoading && (
                      <Loader2
                        size={ICON.sm}
                        className="animate-spin text-dim"
                      />
                    )}
                  </div>
                  <UsageChart
                    points={usageSeries}
                    granularity={usageGranularity}
                    startMs={usageStartMs}
                    endMs={usageEndMs}
                    rangeLabel={usageRangeLabel()}
                    allModels={usageModel === ''}
                  />
                  <div className="overflow-x-auto rounded-card border border-edge bg-panel shadow-raised">
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
                          <th
                            className="px-3 py-2 font-medium text-right"
                            data-tip={t('config.usageCacheHitHint')}
                          >
                            {t('config.usageCacheHitRate')}
                          </th>
                          <th className="px-3 py-2 font-medium text-right">
                            {t('config.usageCacheWrite')}
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
                            className="border-b border-edge/40 last:border-0 hover:bg-panel"
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
                                r.cache_read_tokens === 0 ? 'text-faint' : ''
                              }`}
                            >
                              {fmtUsageTokens(r.cache_read_tokens)}
                            </td>
                            <td className="px-3 py-2 text-right tabular-nums">
                              {usageHitLabel(
                                r.input_tokens,
                                r.cache_read_tokens,
                              )}
                            </td>
                            <td className="px-3 py-2 text-right tabular-nums">
                              {fmtUsageTokens(r.cache_write_tokens)}
                            </td>
                            <td
                              className={`px-3 py-2 text-right tabular-nums ${
                                r.reasoning_tokens === 0 ? 'text-faint' : ''
                              }`}
                            >
                              {fmtUsageTokens(r.reasoning_tokens)}
                            </td>
                            <td
                              className="px-3 py-2 text-right tabular-nums"
                              data-tip={
                                r.calls === 0 && r.latency_ms > 0
                                  ? t('config.usageLatencyLegacyHint')
                                  : undefined
                              }
                            >
                              {r.calls > 0
                                ? `${fmtUsageTokens(
                                    Math.round(r.latency_ms / r.calls),
                                  )}ms`
                                : r.latency_ms > 0
                                  ? `${fmtUsageTokens(r.latency_ms)}ms`
                                  : '—'}
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
              {/* The settings-search anchor (`memory-context`): how much
                  of the conversation reaches the model verbatim, and when
                  the older turns fold into a summary. */}
              <div id="settings-memory-context" className="scroll-mt-4">
                <MemoryContextCard
                  settings={memory}
                  onChange={editMemory}
                  saved={memorySaved}
                  dirty={
                    memoryBase !== null &&
                    !sameMemorySettings(memory, memoryBase)
                  }
                  saving={memorySaving}
                  error={error}
                  onSave={() => void saveMemory()}
                />
              </div>

              {/* User-level long-term memory and the pending review queue.
                  The first id is the settings-search anchor (`memory-facts`); the
                  queue carries its own (`memory-suggestions`). */}
              <div id="settings-memory-facts" className="scroll-mt-4">
                <UserMemoryCard
                  onOpenConversation={(conversationID, workspacePath) => {
                    // A fact or suggestion carries the workspace its turn
                    // ran in: jumping there has to switch workspace first,
                    // which is exactly what openSessionInWorkspace does.
                    void openSessionInWorkspace(
                      conversationID,
                      workspacePath ?? '',
                    )
                      .then(() => closeConfig())
                      .catch((err) => setError(String(err)));
                  }}
                />
              </div>
            </div>
          )}

          {tab === 'permissions' && (
            <div className="space-y-4">
              <p className="text-xs text-dim">{t('config.permissionsHint')}</p>
              <div className="flex items-center gap-2 text-xs text-dim">
                <ShieldCheck size={ICON.sm} className="text-ok" />
                {t('config.permissionsCount', { count: rules.length })}
              </div>
              {rules.length === 0 ? (
                <div className="rounded-card border border-dashed border-edge px-6 py-10 text-center">
                  <ShieldCheck
                    size={ICON.hero}
                    className="mx-auto mb-2 text-faint"
                  />
                  <p className="text-sm text-dim">
                    {t('config.permissionsEmpty')}
                  </p>
                  <p className="mt-1 text-xs text-faint">
                    {t('config.permissionsEmptyHint')}
                  </p>
                </div>
              ) : (
                <div className="overflow-hidden rounded-card border border-edge bg-panel">
                  {rules.map((rule, i) => (
                    <div
                      key={rule}
                      className={`group flex items-center gap-2 px-3 py-2 hover:bg-panel2 ${
                        i > 0 ? 'border-t border-edge/60' : ''
                      }`}
                    >
                      <Terminal size={ICON.sm} className="shrink-0 text-dim" />
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
                        data-tip={t('config.permissionsRemove')}
                        aria-label={t('config.permissionsRemove')}
                        className="shrink-0 rounded-tight p-1 text-dim opacity-0 transition-opacity hover:text-err group-hover:opacity-100"
                      >
                        <Trash2 size={ICON.sm} />
                      </button>
                    </div>
                  ))}
                </div>
              )}
              <div className="flex items-center gap-2 rounded-card border border-edge bg-panel2 p-2">
                <ShieldPlus size={ICON.md} className="ml-1 shrink-0 text-dim" />
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
                  className="flex-1 bg-transparent px-2 py-1 text-sm outline-none placeholder:text-faint"
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
                  className="rounded-control bg-accent px-4 py-1.5 text-sm text-white hover:opacity-90 disabled:opacity-40"
                  disabled={!ruleInput.trim()}
                >
                  {t('config.permissionsAdd')}
                </button>
              </div>

              {/* Escalation rules: commands allowed to leave the
                    sandbox entirely. Strictly stronger than the
                    allowlist above, so they get their own bordered
                    section instead of being mixed into that list. */}
              <div className="space-y-3 rounded-card border border-warn/40 bg-warn/5 p-3">
                <div className="flex items-start gap-2 text-xs text-dim">
                  <ShieldAlert
                    size={ICON.sm}
                    className="mt-0.5 shrink-0 text-warn"
                  />
                  <span>{t('config.escalatedHint')}</span>
                </div>
                {escalatedRules.length > 0 && (
                  <div className="overflow-hidden rounded-control border border-edge bg-panel">
                    {escalatedRules.map((rule, i) => (
                      <div
                        key={rule}
                        className={`group flex items-center gap-2 px-3 py-2 hover:bg-panel2 ${
                          i > 0 ? 'border-t border-edge/60' : ''
                        }`}
                      >
                        <ShieldAlert
                          size={ICON.sm}
                          className="shrink-0 text-warn"
                        />
                        <code className="flex-1 truncate font-mono text-sm text-fg">
                          {rule}
                        </code>
                        <button
                          onClick={() =>
                            void api
                              .denyEscalatedPermission(rule)
                              .then(() => api.escalatedPermissions())
                              .then(setEscalatedRules)
                              .then(() =>
                                toast(t('config.escalatedRemoved', { rule })),
                              )
                              .catch((err) => setError(String(err)))
                          }
                          data-tip={t('config.permissionsRemove')}
                          aria-label={t('config.permissionsRemove')}
                          className="shrink-0 rounded-tight p-1 text-dim opacity-0 transition-opacity hover:text-err group-hover:opacity-100"
                        >
                          <Trash2 size={ICON.sm} />
                        </button>
                      </div>
                    ))}
                  </div>
                )}
                <div className="flex items-center gap-2 rounded-card border border-edge bg-panel2 p-2">
                  <ShieldAlert
                    size={ICON.md}
                    className="ml-1 shrink-0 text-dim"
                  />
                  <input
                    value={escalatedInput}
                    onChange={(e) => setEscalatedInput(e.target.value)}
                    placeholder={t('config.permissionsPlaceholder')}
                    onKeyDown={(e) => {
                      if (e.key !== 'Enter' || !escalatedInput.trim()) return;
                      const rule = escalatedInput.trim();
                      void api
                        .allowEscalatedPermission(rule)
                        .then(() => api.escalatedPermissions())
                        .then(setEscalatedRules)
                        .then(() => {
                          setEscalatedInput('');
                          toast(t('config.escalatedAdded', { rule }));
                        })
                        .catch((err) => setError(String(err)));
                    }}
                    className="flex-1 bg-transparent px-2 py-1 text-sm outline-none placeholder:text-faint"
                  />
                  <button
                    onClick={() => {
                      if (!escalatedInput.trim()) return;
                      const rule = escalatedInput.trim();
                      void api
                        .allowEscalatedPermission(rule)
                        .then(() => api.escalatedPermissions())
                        .then(setEscalatedRules)
                        .then(() => {
                          setEscalatedInput('');
                          toast(t('config.escalatedAdded', { rule }));
                        })
                        .catch((err) => setError(String(err)));
                    }}
                    className="rounded-control bg-accent px-4 py-1.5 text-sm text-white hover:opacity-90 disabled:opacity-40"
                    disabled={!escalatedInput.trim()}
                  >
                    {t('config.permissionsAdd')}
                  </button>
                </div>
              </div>
            </div>
          )}

          {tab === 'diagnostics' && (
            <div className="space-y-5">
              <div className="flex items-start justify-between gap-4">
                <p className="text-xs text-dim">{t('config.diagHint')}</p>
                <label className="flex shrink-0 cursor-pointer items-center gap-1.5 text-xs text-dim">
                  <input
                    type="checkbox"
                    checked={devMode}
                    onChange={(e) => setDevMode(e.target.checked)}
                  />
                  {t('config.diagDevMode')}
                </label>
              </div>
              <DiagSection
                id="settings-diag-runtime"
                title={t('config.diagSecOverview')}
              >
                {diag && (
                  <div className="grid grid-cols-1 gap-3 text-sm sm:grid-cols-2">
                    <div className="rounded-card border border-edge bg-panel2 px-3 py-2">
                      <span className="text-xs text-dim">
                        {t('config.diagVersion')}
                      </span>
                      <p className="font-mono">{diag.version}</p>
                    </div>
                    <div className="rounded-card border border-edge bg-panel2 px-3 py-2">
                      <span className="text-xs text-dim">
                        {t('config.diagPlatform')}
                      </span>
                      <p className="font-mono">
                        {diag.platform}/{diag.arch}
                      </p>
                    </div>
                    <div className="rounded-card border border-edge bg-panel2 px-3 py-2">
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
                    <div className="rounded-card border border-edge bg-panel2 px-3 py-2">
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
                      {diag.sandbox_available_reason && (
                        <p className="text-xs text-dim">
                          {diag.sandbox_available_reason}
                        </p>
                      )}
                    </div>
                    <div className="rounded-card border border-edge bg-panel2 px-3 py-2">
                      <span className="text-xs text-dim">
                        {t('config.diagShell')}
                      </span>
                      <p className="font-mono">{diag.exec_shell}</p>
                    </div>
                    <div className="rounded-card border border-edge bg-panel2 px-3 py-2">
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
                    <div className="rounded-card border border-edge bg-panel2 px-3 py-2">
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
                    <div className="rounded-card border border-edge bg-panel2 px-3 py-2">
                      <span className="text-xs text-dim">
                        {t('config.diagGit')}
                      </span>
                      <p className="font-mono">
                        {diag.git_repo
                          ? diag.git_branch || '(repo)'
                          : t('config.diagNoRepo')}
                      </p>
                    </div>
                    <div className="rounded-card border border-edge bg-panel2 px-3 py-2">
                      <span className="text-xs text-dim">
                        {t('config.diagSessions')}
                      </span>
                      <p className="font-mono">
                        {Number.isFinite(diag.session_count) &&
                        Number.isFinite(diag.active_runs) ? (
                          <>
                            {t('config.diagSessionCount', {
                              count: diag.session_count,
                            })}{' '}
                            ·{' '}
                            {t('config.diagActiveRuns', {
                              count: diag.active_runs,
                            })}
                          </>
                        ) : (
                          // A status without the counters (an older backend,
                          // a partial probe) must not print the raw plural
                          // keys, which is what t() returns for a missing
                          // count.
                          '—'
                        )}
                      </p>
                    </div>
                    <div className="rounded-card border border-edge bg-panel2 px-3 py-2">
                      <span className="text-xs text-dim">
                        {t('config.diagPaths')}
                      </span>
                      <p className="mt-1 break-all font-mono text-xs">
                        {diag.work_dir}
                        <br />
                        {diag.user_dir}
                      </p>
                      {/* The two roots behind this window. A dev profile
                          shares the config dir and app home with the
                          installed app and owns another state root, so
                          "which instance am I looking at" is answered
                          here instead of by guessing from the paths. */}
                      {diag.data_dir && (
                        <p className="mt-1 text-micro text-faint">
                          {t('config.diagProfile')}:{' '}
                          {diag.profile || t('config.diagProfileDefault')}
                        </p>
                      )}
                      {diag.data_dir && (
                        <p className="mt-0.5 break-all font-mono text-micro text-faint">
                          {t('config.diagStateRoot')}: {diag.data_dir}
                          <br />
                          {t('config.diagAppHome')}: {diag.app_home}
                        </p>
                      )}
                    </div>
                  </div>
                )}
                <div className="flex flex-wrap items-center gap-2 pt-1">
                  <Button
                    variant="primary"
                    size="md"
                    onClick={() => void runProbe()}
                    disabled={diagBusy}
                  >
                    {t('config.diagProbe')}
                  </Button>
                  <Button
                    variant="quiet"
                    size="md"
                    onClick={() => void clearCaches()}
                    disabled={diagBusy}
                  >
                    {t('config.diagClearCache')}
                  </Button>
                  <Button
                    variant="quiet"
                    size="md"
                    onClick={() =>
                      void api.reload().catch((err) => toast(String(err)))
                    }
                  >
                    {t('config.diagReload')}
                  </Button>
                  <Button
                    variant="quiet"
                    size="md"
                    onClick={() => void repairConfigCompat()}
                    disabled={diagBusy}
                  >
                    {t('config.diagRepairCompat')}
                  </Button>
                </div>
                {probe && (
                  <div
                    className={`rounded-control border px-3 py-2 text-xs ${
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
                      count: cacheResult.dirs.length,
                    })}
                  </p>
                )}
              </DiagSection>

              <DiagSection
                id="settings-diag-recovery"
                title={t('config.diagRecoveryTitle')}
                hint={t('config.diagRecoveryHint')}
              >
                <RecoveryCard />
              </DiagSection>

              <DiagSection
                id="settings-diag-policy"
                title={t('config.diagSecPolicy')}
                hint={t('config.diagPolicy')}
              >
                <div className="rounded-card border border-edge bg-panel2 p-3">
                  <div className="flex items-center gap-2">
                    <input
                      value={policyInput}
                      onChange={(e) => setPolicyInput(e.target.value)}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter') void checkPolicy();
                      }}
                      placeholder={t('config.diagPolicyPlaceholder')}
                      className="flex-1 rounded-control border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
                    />
                    <Button
                      variant="primary"
                      size="md"
                      onClick={() => void checkPolicy()}
                      disabled={diagBusy}
                    >
                      {t('config.diagPolicyCheck')}
                    </Button>
                  </div>
                  {policy && (
                    <p
                      className={`mt-2 text-sm ${
                        policy.allowed ? 'text-ok' : 'text-warn'
                      }`}
                    >
                      {policy.allowed
                        ? t('config.diagPolicyAllowed')
                        : t('config.diagPolicyAsk')}
                    </p>
                  )}
                </div>
              </DiagSection>

              <DiagSection
                id="settings-diag-logs"
                title={t('config.secDiagLogs')}
              >
                <DiagToolRow
                  hint={t('config.logsHint')}
                  action={t('config.diagOpenLogs')}
                  onAction={() => setLogOpen(true)}
                />
              </DiagSection>

              <DiagSection
                id="settings-diag-metrics"
                title={t('config.metricsTitle')}
              >
                <DiagToolRow
                  hint={t('config.metricsHint')}
                  action={t('config.diagOpenMetrics')}
                  onAction={() => setMetricsOpen(true)}
                />
              </DiagSection>

              <DiagSection
                id="settings-diag-path"
                title={t('config.diagPathTitle')}
              >
                <DiagToolRow
                  hint={t('config.diagPathHint')}
                  action={t('config.diagEditPath')}
                  onAction={() => setPathOpen(true)}
                />
              </DiagSection>

              <DiagSection
                id="settings-diag-pet"
                title={t('config.diagSecPet')}
                hint={t('config.diagSecPetHint')}
              >
                <PetBehaviorPanel />
              </DiagSection>

              {devMode ? (
                <>
                  <DiagSection
                    id="settings-diag-perfprobe"
                    title={t('config.diagPerfProbeTitle')}
                    badge={t('config.diagDevMode')}
                  >
                    <PerfProbeCard showTitle={false} />
                  </DiagSection>
                  <DiagSection
                    id="settings-diag-heap"
                    title={t('config.heapProfileTitle')}
                    badge={t('config.diagDevMode')}
                  >
                    <HeapProfileCard showTitle={false} />
                  </DiagSection>
                  <DiagSection
                    id="settings-diag-otlp"
                    title={t('config.diagTelemetryTitle')}
                    badge={t('config.diagDevMode')}
                  >
                    <TelemetryExportCard showTitle={false} />
                  </DiagSection>
                  <DiagSection
                    id="settings-diag-httpprobe"
                    title={t('config.diagHttpProbeTitle')}
                    badge={t('config.diagDevMode')}
                  >
                    <HTTPProbeCard showTitle={false} />
                  </DiagSection>
                  <DiagSection
                    id="settings-diag-execpool"
                    title={t('config.diagExecPoolTitle')}
                    badge={t('config.diagDevMode')}
                  >
                    <DiagToolRow
                      hint={t('config.diagExecPoolHint')}
                      action={t('config.diagEditExecPool')}
                      onAction={() => setExecPoolOpen(true)}
                    />
                  </DiagSection>
                </>
              ) : (
                <p className="border-t border-edge pt-4 text-xs text-dim">
                  {t('config.diagDevHidden')}
                </p>
              )}
            </div>
          )}

          {tab === 'import' && (
            <div className="space-y-3">
              <p className="text-xs text-dim">{t('config.importIntro')}</p>
              <PluginPanels tab="import" />
              {importPanelCount === 0 && (
                <div className="rounded-card border border-dashed border-edge bg-panel2 p-8 text-center">
                  <Import size={ICON.lg} className="mx-auto mb-2 text-dim" />
                  <p className="text-xs text-dim">{t('config.importEmpty')}</p>
                </div>
              )}
            </div>
          )}
        </div>
      </div>

      {tab === 'inference' ? (
        <div className="shrink-0 border-t border-edge bg-panel px-6 py-3">
          <SaveBar
            bare
            error={error}
            saving={saving}
            onSave={() => void save()}
          />
        </div>
      ) : (
        error &&
        tab !== 'memory' && (
          <div className="shrink-0 border-t border-edge bg-panel px-6 py-3">
            <span className="text-xs text-err">{error}</span>
          </div>
        )
      )}

      {/* The diagnostics tools whose editors or panes are too large for the
          column. Each one keeps the card it always had; the dialog is the
          only thing that changed. They portal out of the settings panel —
          it clips its own overflow — and the charts dialog does not
          animate: recharts measures its container every frame, so a
          transformed panel reads as a scale change and dispatches one per
          frame (React's maximum-update-depth error with a full grid). */}
      <Modal
        open={logOpen}
        onClose={() => setLogOpen(false)}
        title={t('config.secDiagLogs')}
        width="56rem"
        portal
      >
        <div className="h-[26rem]">
          <LogViewer fetchLogs={() => api.readLog(300)} />
        </div>
      </Modal>
      <Modal
        open={metricsOpen}
        onClose={() => setMetricsOpen(false)}
        title={t('config.metricsTitle')}
        width="52rem"
        portal
        motion={false}
      >
        <MetricsCharts showHeading={false} />
      </Modal>
      <Modal
        open={pathOpen}
        onClose={() => setPathOpen(false)}
        title={t('config.diagPathTitle')}
        width="44rem"
        portal
      >
        <PathEnvironmentCard />
      </Modal>
      <Modal
        open={execPoolOpen}
        onClose={() => setExecPoolOpen(false)}
        title={t('config.diagExecPoolTitle')}
        width="44rem"
        portal
      >
        <ExecPoolCard />
      </Modal>
    </Overlay>
  );
}
