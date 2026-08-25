import { useEffect, useMemo, useRef, useState } from "react";
import {
  ArrowDown,
  ArrowUp,
  Loader2,
  Plus,
  Settings,
  Trash2,
  X,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { api } from "../lib/api";
import { useStore } from "../lib/store";
import type {
  ModelUsageStat,
  ProviderInstance,
  ProviderView,
  UsagePoint,
} from "../lib/types";
import { UsageChart } from "./UsageChart";

// InstanceRow is one editable inference instance in the settings page.
interface InstanceRow {
  id: string; // frontend key
  stableId: string; // persisted identity ("" on newly added rows)
  type: string;
  name: string;
  api: string;
  key: string;
  keySet: boolean;
  keyEnv: boolean;
  model: string;
  endpoint: string;
  vision: boolean;
  reasoning: string;
  webSearch: boolean;
  enabled: boolean;
}

type Tab =
  | "ui"
  | "inference"
  | "usage"
  | "permissions"
  | "logs";

export function ConfigPage() {
  const configured = useStore((s) => s.configured);
  const closeConfig = useStore((s) => s.closeConfig);
  const theme = useStore((s) => s.theme);
  const setTheme = useStore((s) => s.setTheme);
  const newID = () => `mcp-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
  const { t, i18n } = useTranslation();
  const lang = i18n.resolvedLanguage?.startsWith("zh") ? "zh" : "en";

  const [tab, setTab] = useState<Tab>("inference");
  const [rows, setRows] = useState<InstanceRow[]>([]);
  const [catalog, setCatalog] = useState<ProviderView[]>([]);
  const [newType, setNewType] = useState("deepseek");
  const [defaultModel, setDefaultModel] = useState("");
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const [rules, setRules] = useState<string[]>([]);
  const [ruleInput, setRuleInput] = useState("");
  const [logs, setLogs] = useState("");
  const logsRef = useRef<HTMLPreElement>(null);
  const [usageRows, setUsageRows] = useState<ModelUsageStat[]>([]);
  const [usageError, setUsageError] = useState("");
  const [usageModel, setUsageModel] = useState("");
  const [usageRange, setUsageRange] = useState<
    "today" | "1d" | "7d" | "14d" | "30d"
  >("7d");
  const [usageSeries, setUsageSeries] = useState<UsagePoint[]>([]);
  const [usageStartMs, setUsageStartMs] = useState(0);
  const [usageEndMs, setUsageEndMs] = useState(0);
  const [usageLoading, setUsageLoading] = useState(false);
  const [usageReload, setUsageReload] = useState(0);

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
          (state.instances ?? []).map((s) => ({
            id: newID(),
            stableId: s.stable_id ?? "",
            type: s.type,
            name: s.name ?? "",
            api: s.api ?? "",
            key: s.key ?? "",
            keySet: s.key_set ?? false,
            keyEnv: s.key_env ?? false,
            model: s.model || byType.get(s.type)?.default_model || "",
            endpoint: s.endpoint ?? "",
            vision: s.vision ?? false,
            reasoning: s.reasoning ?? "",
            webSearch: s.web_search ?? false,
            enabled: s.enabled ?? true,
          })),
        );
        setDefaultModel(state.model);
      } catch (err) {
        setError(String(err));
      }
    })();
  }, []);

  useEffect(() => {
    if (tab !== "permissions") return;
    void api
      .permissions()
      .then(setRules)
      .catch((err) => setError(String(err)));
  }, [tab]);

  useEffect(() => {
    if (tab !== "logs") return;
    void api
      .readLog(300)
      .then(setLogs)
      .catch((err) => setError(String(err)));
  }, [tab]);

  // Pin the log view to the newest lines whenever the content changes
  // (including the first load when the tab opens).
  useEffect(() => {
    if (tab === "logs" && logsRef.current) {
      logsRef.current.scrollTop = logsRef.current.scrollHeight;
    }
  }, [logs, tab]);

  useEffect(() => {
    if (tab !== "usage") return;
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
    preset: "today" | "1d" | "7d" | "14d" | "30d",
  ): { startMs: number; endMs: number } => {
    const endMs = Date.now();
    const DAY = 86_400_000;
    if (preset === "today") {
      const d = new Date(endMs);
      d.setHours(0, 0, 0, 0);
      return { startMs: d.getTime(), endMs };
    }
    if (preset === "1d") {
      return { startMs: endMs - DAY, endMs };
    }
    const days = preset === "7d" ? 7 : preset === "14d" ? 14 : 30;
    const d = new Date(endMs - (days - 1) * DAY);
    d.setHours(0, 0, 0, 0);
    return { startMs: d.getTime(), endMs };
  };

  // Load the selected model's time series whenever the model, range,
  // tab, or an explicit refresh changes. Granularity follows the range
  // duration like cc-switch: <= 24h buckets hourly, longer ranges
  // bucket by local day.
  useEffect(() => {
    if (tab !== "usage" || !usageModel) {
      setUsageSeries([]);
      return;
    }
    let cancelled = false;
    setUsageLoading(true);
    const { startMs, endMs } = resolveUsageRange(usageRange);
    const granularity: "hour" | "day" =
      endMs - startMs <= 24 * 3_600_000 ? "hour" : "day";
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
        stableId: "",
        type,
        name: "",
        api: prov?.api ?? "",
        key: "",
        keySet: false,
        keyEnv: false,
        model: prov?.default_model ?? "",
        endpoint: "",
        vision: false,
        reasoning: "",
        webSearch: false,
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
    setRows((prev) =>
      prev.map((r) => (r.id === id ? { ...r, ...patch } : r)),
    );
  };

  const save = async () => {
    setError("");
    if (enabledRows.length === 0) {
      setError(t("setup.selectProvider"));
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
      model: r.model,
      endpoint: r.endpoint,
      vision: r.vision,
      reasoning: r.reasoning,
      web_search: r.webSearch,
      enabled: r.enabled,
    }));
    setSaving(true);
    try {
      await api.saveInstances({ instances });
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
    if (!iso) return "";
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return "";
    const diff = Date.now() - d.getTime();
    if (diff < 60_000) return "刚刚";
    if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m`;
    if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h`;
    return d.toLocaleDateString();
  };

  const tabs: { id: Tab; label: string }[] = [
    { id: "ui", label: t("config.tabUi") },
    { id: "inference", label: t("config.tabInference") },
    { id: "usage", label: t("config.tabUsage") },
    { id: "permissions", label: t("config.tabPermissions") },
    { id: "logs", label: t("config.tabLogs") },
  ];

  return (
    <div className="fixed inset-x-0 bottom-0 top-11 z-50 bg-black/70 grid place-items-center">
      <div className="w-[720px] h-[620px] flex flex-col rounded-2xl border border-edge bg-panel shadow-2xl">
        <div className="flex items-center gap-4 px-5 py-4 border-b border-edge">
          <Settings size={18} className="text-accent" />
          <h2 className="text-base font-semibold">{t("config.title")}</h2>
          <div className="flex rounded-lg border border-edge overflow-hidden text-sm ml-2">
            {tabs.map((tb) => (
              <button
                key={tb.id}
                onClick={() => {
                  setTab(tb.id);
                  setError("");
                }}
                className={`px-3 py-1 ${
                  tab === tb.id
                    ? "bg-accent text-white"
                    : "text-dim hover:text-fg"
                }`}
              >
                {tb.label}
              </button>
            ))}
          </div>
          <span className="flex-1" />
          {configured && (
            <button
              onClick={closeConfig}
              className="text-dim hover:text-fg"
            >
              <X size={18} />
            </button>
          )}
        </div>

        <div className="flex-1 overflow-y-auto px-5 py-4">
          {tab === "ui" && (
            <div>
              <div className="text-sm mb-3">{t("config.uiLanguage")}</div>
              <div className="flex rounded-lg border border-edge overflow-hidden w-fit text-sm">
                <button
                  onClick={() => void i18n.changeLanguage("zh")}
                  className={`px-3 py-1.5 ${
                    lang === "zh"
                      ? "bg-accent text-white"
                      : "text-dim hover:text-fg"
                  }`}
                >
                  中文
                </button>
                <button
                  onClick={() => void i18n.changeLanguage("en")}
                  className={`px-3 py-1.5 ${
                    lang === "en"
                      ? "bg-accent text-white"
                      : "text-dim hover:text-fg"
                  }`}
                >
                  English
                </button>
              </div>
              <div className="text-sm mb-3 mt-5">{t("config.uiTheme")}</div>
              <div className="flex rounded-lg border border-edge overflow-hidden w-fit text-sm">
                <button
                  onClick={() => setTheme("dark")}
                  className={`px-3 py-1.5 ${
                    theme === "dark"
                      ? "bg-accent text-white"
                      : "text-dim hover:text-fg"
                  }`}
                >
                  {t("config.uiThemeDark")}
                </button>
                <button
                  onClick={() => setTheme("light")}
                  className={`px-3 py-1.5 ${
                    theme === "light"
                      ? "bg-accent text-white"
                      : "text-dim hover:text-fg"
                  }`}
                >
                  {t("config.uiThemeLight")}
                </button>
              </div>
            </div>
          )}

          {tab === "inference" && (
            <div className="space-y-3">
              <p className="text-xs text-dim">
                {defaultModel
                  ? t("config.inferenceCurrent", { model: defaultModel })
                  : t("setup.subtitle")}
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
                  {t("config.addInstance")}
                </button>
              </div>
              {rows.length === 0 && (
                <p className="text-sm text-dim">{t("config.instancesEmpty")}</p>
              )}
              {rows.map((row) => {
                const prov = catalog.find((p) => p.id === row.type);
                return (
                  <div
                    key={row.id}
                    className={`rounded-xl border overflow-hidden ${
                      row.enabled
                        ? "border-edge bg-panel2"
                        : "border-edge/50 bg-panel2/50"
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
                        title={t("config.instanceEnabled")}
                      />
                      <span className="font-medium text-sm shrink-0">
                        {prov?.name ?? row.type}
                      </span>
                      <input
                        value={row.name}
                        onChange={(e) =>
                          update(row.id, { name: e.target.value })
                        }
                        placeholder={t("config.instanceName")}
                        className="flex-1 min-w-0 rounded-lg border border-edge bg-panel px-2 py-1 text-sm outline-none focus:border-accent"
                      />
                      <button
                        onClick={() =>
                          setRows((prev) =>
                            prev.filter((r) => r.id !== row.id),
                          )
                        }
                        className="text-dim hover:text-err shrink-0"
                        title={t("config.removeInstance")}
                      >
                        <Trash2 size={14} />
                      </button>
                    </div>
                    {row.enabled && (
                      <div className="px-4 pb-3 pt-1 space-y-2">
                        <div className="flex gap-2">
                          <input
                            value={row.model}
                            onChange={(e) =>
                              update(row.id, { model: e.target.value })
                            }
                            placeholder={t("setup.model")}
                            className="flex-1 rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
                          />
                          <input
                            value={row.endpoint}
                            onChange={(e) =>
                              update(row.id, { endpoint: e.target.value })
                            }
                            placeholder={t("setup.endpointPlaceholder")}
                            className="w-64 rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
                          />
                        </div>
                        {row.type === "openai" && (
                          <div className="flex items-center gap-2 text-xs text-dim">
                            {t("setup.apiMode")}
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
                        )}
                        <div className="flex items-center gap-2">
                          <input
                            type="password"
                            value={row.key}
                            onChange={(e) =>
                              update(row.id, { key: e.target.value })
                            }
                            disabled={row.keyEnv}
                            placeholder={
                              row.keySet && row.key === ""
                                ? t("setup.apiKeySet")
                                : t("setup.apiKeyPlaceholder", {
                                    var: prov?.env_var ?? "",
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
                            {t("setup.envVar", { var: prov?.env_var ?? "" })}
                          </label>
                        </div>
                        <div className="flex items-center gap-4 text-xs text-dim">
                          <label className="flex items-center gap-1.5">
                            <input
                              type="checkbox"
                              checked={row.vision}
                              onChange={(e) =>
                                update(row.id, { vision: e.target.checked })
                              }
                              className="accent-[var(--color-accent)]"
                            />
                            {t("setup.vision")}
                          </label>
                          <label className="flex items-center gap-1.5">
                            {t("setup.reasoning")}
                            <select
                              value={row.reasoning}
                              onChange={(e) =>
                                update(row.id, {
                                  reasoning: e.target.value,
                                })
                              }
                              className="rounded border border-edge bg-panel px-2 py-1 outline-none"
                            >
                              <option value="">
                                {t("setup.reasoningOff")}
                              </option>
                              <option value="always">always</option>
                              <option value="toggle">toggle</option>
                            </select>
                          </label>
                          <label className="flex items-center gap-1.5">
                            <input
                              type="checkbox"
                              checked={row.webSearch}
                              onChange={(e) =>
                                update(row.id, {
                                  webSearch: e.target.checked,
                                })
                              }
                              className="accent-[var(--color-accent)]"
                            />
                            {t("setup.webSearch")}
                          </label>
                        </div>
                      </div>
                    )}
                  </div>
                );
              })}

              {enabledRows.length > 1 && (
                <div className="rounded-xl border border-edge bg-panel2 p-3">
                  <div className="text-xs text-dim mb-2">
                    {t("setup.routerPriority")}
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
                          {row.name ? ` · ${row.name}` : ""}
                        </span>
                        <span className="text-xs text-dim truncate">
                          {row.model}
                        </span>
                        <button
                          onClick={() => move(idx, -1)}
                          disabled={idx === 0}
                          className="text-dim hover:text-fg disabled:opacity-30"
                        >
                          <ArrowUp size={14} />
                        </button>
                        <button
                          onClick={() => move(idx, 1)}
                          disabled={idx === enabledRows.length - 1}
                          className="text-dim hover:text-fg disabled:opacity-30"
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

          {tab === "usage" && (
            <div className="space-y-3">
              <div className="flex items-center justify-between">
                <p className="text-xs text-dim">{t("config.usageHint")}</p>
                <button
                  onClick={() => {
                    void api
                      .modelUsage()
                      .then(setUsageRows)
                      .catch((err) => setUsageError(String(err)));
                    setUsageReload((n) => n + 1);
                  }}
                  className="text-xs text-dim hover:text-fg"
                >
                  {t("config.logsRefresh")}
                </button>
              </div>
              {usageError && <p className="text-xs text-err">{usageError}</p>}
              {usageRows.length === 0 ? (
                <p className="text-sm text-dim">{t("config.usageEmpty")}</p>
              ) : (
                <>
                  <div className="flex flex-wrap items-center gap-2">
                    <select
                      value={usageModel}
                      onChange={(e) => setUsageModel(e.target.value)}
                      className="max-w-[340px] rounded-lg border border-edge bg-panel px-2 py-1.5 text-xs font-mono outline-none"
                      title={t("config.usageModel")}
                    >
                      {usageRows.map((r) => (
                        <option key={r.model} value={r.model}>
                          {r.model}
                        </option>
                      ))}
                    </select>
                    {(
                      [
                        ["today", t("config.usageRangeToday")],
                        ["1d", "1d"],
                        ["7d", "7d"],
                        ["14d", "14d"],
                        ["30d", "30d"],
                      ] as const
                    ).map(([value, label]) => (
                      <button
                        key={value}
                        onClick={() => setUsageRange(value)}
                        className={`rounded-lg border px-2.5 py-1.5 text-xs ${
                          usageRange === value
                            ? "border-accent bg-accent/15 text-fg"
                            : "border-edge text-dim hover:text-fg"
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
                    title={t("config.usageCacheHitHint")}
                  >
                    <div className="mb-2 flex items-center justify-between text-xs">
                      <span className="text-dim">
                        {t("config.usageCacheHitRate")}
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
                      {fmtUsageTokens(cacheHit.read)} /{" "}
                      {fmtUsageTokens(cacheHit.input)}
                    </div>
                  </div>
                  <UsageChart
                    points={usageSeries}
                    granularity={
                      usageEndMs - usageStartMs <= 24 * 3_600_000
                        ? "hour"
                        : "day"
                    }
                    startMs={usageStartMs}
                    endMs={usageEndMs}
                    rangeLabel={
                      usageRange === "today"
                        ? t("config.usageRangeToday")
                        : usageRange
                    }
                  />
                <div className="rounded-xl border border-edge bg-panel2 overflow-x-auto">
                  <table className="w-full text-xs">
                    <thead>
                      <tr className="text-left text-dim border-b border-edge">
                        <th className="px-3 py-2 font-medium">
                          {t("config.usageModel")}
                        </th>
                        <th className="px-3 py-2 font-medium text-right">
                          {t("config.usageInput")}
                        </th>
                        <th className="px-3 py-2 font-medium text-right">
                          {t("config.usageOutput")}
                        </th>
                        <th className="px-3 py-2 font-medium text-right">
                          {t("config.usageCache")}
                        </th>
                        <th className="px-3 py-2 font-medium text-right">
                          {t("config.usageReasoning")}
                        </th>
                        <th className="px-3 py-2 font-medium text-right">
                          {t("config.usageLatency")}
                        </th>
                        <th className="px-3 py-2 font-medium text-right">
                          {t("config.usageSessions")}
                        </th>
                        <th className="px-3 py-2 font-medium text-right">
                          {t("config.usageUpdated")}
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {usageRows.map((r) => (
                        <tr key={r.model} className="border-b border-edge/50 last:border-0">
                          <td className="px-3 py-2 font-mono">{r.model}</td>
                          <td className="px-3 py-2 text-right tabular-nums">
                            {fmtUsageTokens(r.input_tokens)}
                          </td>
                          <td className="px-3 py-2 text-right tabular-nums">
                            {fmtUsageTokens(r.output_tokens)}
                          </td>
                          <td className="px-3 py-2 text-right tabular-nums">
                            {fmtUsageTokens(r.cache_read_tokens)}
                          </td>
                          <td className="px-3 py-2 text-right tabular-nums">
                            {fmtUsageTokens(r.reasoning_tokens)}
                          </td>
                          <td className="px-3 py-2 text-right tabular-nums">
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

          {tab === "permissions" && (
            <div className="space-y-3">
              <p className="text-xs text-dim">{t("config.permissionsHint")}</p>
              {rules.length === 0 ? (
                <p className="text-sm text-dim">{t("config.permissionsEmpty")}</p>
              ) : (
                rules.map((rule) => (
                  <div
                    key={rule}
                    className="flex items-center gap-2 rounded-lg border border-edge bg-panel2 px-3 py-2"
                  >
                    <code className="flex-1 text-sm font-mono truncate">
                      {rule}
                    </code>
                    <button
                      onClick={() =>
                        void api
                          .denyPermission(rule)
                          .then(() => api.permissions())
                          .then(setRules)
                          .catch((err) => setError(String(err)))
                      }
                      className="text-xs text-dim hover:text-err"
                    >
                      {t("config.permissionsRemove")}
                    </button>
                  </div>
                ))
              )}
              <div className="flex items-center gap-2 pt-2">
                <input
                  value={ruleInput}
                  onChange={(e) => setRuleInput(e.target.value)}
                  placeholder={t("config.permissionsPlaceholder")}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" && ruleInput.trim()) {
                      void api
                        .allowPermission(ruleInput.trim())
                        .then(() => api.permissions())
                        .then(setRules)
                        .then(() => setRuleInput(""))
                        .catch((err) => setError(String(err)));
                    }
                  }}
                  className="flex-1 rounded-lg border border-edge bg-panel2 px-3 py-1.5 text-sm outline-none focus:border-accent"
                />
                <button
                  onClick={() => {
                    if (!ruleInput.trim()) return;
                    void api
                      .allowPermission(ruleInput.trim())
                      .then(() => api.permissions())
                      .then(setRules)
                      .then(() => setRuleInput(""))
                      .catch((err) => setError(String(err)));
                  }}
                  className="rounded-lg bg-accent px-4 py-1.5 text-sm text-white hover:opacity-90"
                >
                  {t("config.permissionsAdd")}
                </button>
              </div>
            </div>
          )}

          {tab === "logs" && (
            <div className="h-full flex flex-col gap-3">
              <div className="flex items-center justify-between">
                <p className="text-xs text-dim">{t("config.logsHint")}</p>
                <button
                  onClick={() =>
                    void api
                      .readLog(300)
                      .then(setLogs)
                      .catch((err) => setError(String(err)))
                  }
                  className="text-xs text-dim hover:text-fg"
                >
                  {t("config.logsRefresh")}
                </button>
              </div>
              {logs ? (
                <pre
                  ref={logsRef}
                  className="flex-1 min-h-0 rounded-xl border border-edge bg-panel2 p-3 text-xs whitespace-pre-wrap break-all font-mono overflow-y-auto"
                >
                  {logs}
                </pre>
              ) : (
                <p className="text-sm text-dim">{t("config.logsEmpty")}</p>
              )}
            </div>
          )}
        </div>

        <div className="px-5 py-4 border-t border-edge flex items-center gap-3">
          {error && <span className="text-xs text-err flex-1">{error}</span>}
          <span className="flex-1" />
          {tab === "inference" && (
            <button
              onClick={() => void save()}
              disabled={saving}
              className="flex items-center gap-1.5 rounded-lg bg-accent px-5 py-2 text-sm text-white hover:opacity-90 disabled:opacity-50"
            >
              {saving && <Loader2 size={14} className="animate-spin" />}
              {t("setup.saveApply")}
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
