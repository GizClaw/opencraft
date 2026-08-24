import { useEffect, useMemo, useRef, useState } from "react";
import {
  ArrowDown,
  ArrowUp,
  Bot,
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
  MCPServer,
  ModelUsageStat,
  ProviderInstance,
  ProviderView,
  UsagePoint,
} from "../lib/types";
import { UsageChart } from "./UsageChart";

// InstanceRow is one editable inference instance in the settings page.
interface InstanceRow {
  id: string; // frontend key
  type: string;
  name: string;
  api: string;
  key: string;
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
  | "mcp"
  | "usage"
  | "agents"
  | "permissions"
  | "skills"
  | "logs";

interface MCPRow {
  id: string;
  name: string;
  transport: string;
  command: string;
  url: string;
  argsText: string;
  envText: string;
}

export function ConfigPage() {
  const configured = useStore((s) => s.configured);
  const closeConfig = useStore((s) => s.closeConfig);
  const agents = useStore((s) => s.agents);
  const refreshAgents = useStore((s) => s.refreshAgents);
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
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [rules, setRules] = useState<string[]>([]);
  const [ruleInput, setRuleInput] = useState("");
  const [skills, setSkills] = useState<{ name: string; description: string; scope: string; path: string }[]>([]);
  const [logs, setLogs] = useState("");
  const logsRef = useRef<HTMLPreElement>(null);
  const [mcpRows, setMCPRows] = useState<MCPRow[]>([]);
  const [mcpError, setMCPError] = useState("");
  const [usageRows, setUsageRows] = useState<ModelUsageStat[]>([]);
  const [usageError, setUsageError] = useState("");
  const [usageModel, setUsageModel] = useState("");
  const [usageGranularity, setUsageGranularity] = useState<"hour" | "day">("hour");
  const [usageRange, setUsageRange] = useState<"24h" | "7d" | "30d" | "all">("7d");
  const [usageSeries, setUsageSeries] = useState<UsagePoint[]>([]);
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
            type: s.type,
            name: s.name ?? "",
            api: s.api ?? "",
            key: s.key ?? "",
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
    void refreshAgents();
  }, [refreshAgents]);

  useEffect(() => {
    if (tab !== "permissions") return;
    void api
      .permissions()
      .then(setRules)
      .catch((err) => setError(String(err)));
  }, [tab]);

  useEffect(() => {
    if (tab !== "skills") return;
    void api
      .skills()
      .then(setSkills)
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
    if (tab !== "mcp") return;
    void api
      .mcpConfig()
      .then((servers) =>
        setMCPRows(
          (servers ?? []).map((s) => ({
            id: newID(),
            name: s.name,
            transport: s.transport,
            command: s.command ?? "",
            url: s.url ?? "",
            argsText: (s.args ?? []).join(", "),
            envText: Object.entries(s.env ?? {})
              .map(([k, v]) => `${k}=${v}`)
              .join("\n"),
          })),
        ),
      )
      .catch((err) => setMCPError(String(err)));
  }, [tab]);

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

  // Load the selected model's time series whenever the model, bucket,
  // tab, or an explicit refresh changes.
  useEffect(() => {
    if (tab !== "usage" || !usageModel) {
      setUsageSeries([]);
      return;
    }
    let cancelled = false;
    setUsageLoading(true);
    const end = Math.ceil(Date.now() / 3_600_000) * 3_600_000;
    const hoursByRange = { "24h": 24, "7d": 24 * 7, "30d": 24 * 30 } as const;
    const start =
      usageRange === "all" ? "" : new Date(end - hoursByRange[usageRange] * 3_600_000).toISOString();
    void api
      .modelUsageSeries(
        usageModel,
        usageGranularity,
        -new Date().getTimezoneOffset(),
        start,
        new Date(end).toISOString(),
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
  }, [tab, usageModel, usageGranularity, usageRange, usageReload]);

  // enabledRows are the instances that participate in the router, in
  // priority order; only they can be reordered.
  const enabledRows = useMemo(() => rows.filter((r) => r.enabled), [rows]);

  const addInstance = (type: string) => {
    const prov = catalog.find((p) => p.id === type);
    setRows((prev) => [
      ...prev,
      {
        id: newID(),
        type,
        name: "",
        api: prov?.api ?? "",
        key: "",
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
      type: r.type,
      name: r.name,
      api: r.api,
      key: r.key,
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

  const deleteAgent = async (name: string) => {
    setError("");
    try {
      await api.unregisterAgent(name);
      setConfirmDelete(null);
      await refreshAgents();
    } catch (err) {
      setError(String(err));
      setConfirmDelete(null);
    }
  };

  const updateMCP = (id: string, patch: Partial<MCPRow>) => {
    setMCPRows((prev) =>
      prev.map((r) => (r.id === id ? { ...r, ...patch } : r)),
    );
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

  const saveMCP = async () => {
    setMCPError("");
    const servers: MCPServer[] = mcpRows.map((r) => {
      const srv: MCPServer = {
        name: r.name.trim(),
        transport: r.transport,
      };
      if (r.transport === "http") {
        srv.url = r.url.trim();
      } else {
        srv.command = r.command.trim();
      }
      const args = r.argsText
        .split(",")
        .map((a) => a.trim())
        .filter(Boolean);
      if (args.length > 0) srv.args = args;
      const env: Record<string, string> = {};
      for (const line of r.envText.split("\n")) {
        const eq = line.indexOf("=");
        if (eq <= 0) continue;
        env[line.slice(0, eq).trim()] = line.slice(eq + 1).trim();
      }
      if (Object.keys(env).length > 0) srv.env = env;
      return srv;
    });
    try {
      await api.saveMCP(servers);
      setMCPError("");
    } catch (err) {
      setMCPError(String(err));
    }
  };

  const tabs: { id: Tab; label: string }[] = [
    { id: "ui", label: t("config.tabUi") },
    { id: "inference", label: t("config.tabInference") },
    { id: "mcp", label: t("config.tabMCP") },
    { id: "usage", label: t("config.tabUsage") },
    { id: "agents", label: t("config.tabAgents") },
    { id: "permissions", label: t("config.tabPermissions") },
    { id: "skills", label: t("config.tabSkills") },
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
                            placeholder={t("setup.apiKeyPlaceholder", {
                              var: prov?.env_var ?? "",
                            })}
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

          {tab === "mcp" && (
            <div className="space-y-3">
              <p className="text-xs text-dim">{t("config.mcpHint")}</p>
              {mcpRows.length === 0 && (
                <p className="text-sm text-dim">{t("config.mcpEmpty")}</p>
              )}
              <div className="space-y-3">
                {mcpRows.map((row) => (
                  <div
                    key={row.id}
                    className="rounded-xl border border-edge bg-panel2 p-3 space-y-2"
                  >
                    <div className="flex items-center gap-2">
                      <input
                        value={row.name}
                        onChange={(e) => updateMCP(row.id, { name: e.target.value })}
                        placeholder={t("config.mcpName")}
                        className="flex-1 rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
                      />
                      <select
                        value={row.transport}
                        onChange={(e) =>
                          updateMCP(row.id, { transport: e.target.value })
                        }
                        className="rounded-lg border border-edge bg-panel px-2 py-1.5 text-sm outline-none"
                      >
                        <option value="stdio">stdio</option>
                        <option value="http">http</option>
                      </select>
                      <button
                        onClick={() =>
                          setMCPRows((prev) => prev.filter((r) => r.id !== row.id))
                        }
                        className="text-dim hover:text-err"
                        title={t("config.mcpRemove")}
                      >
                        <Trash2 size={14} />
                      </button>
                    </div>
                    {row.transport === "stdio" ? (
                      <input
                        value={row.command}
                        onChange={(e) =>
                          updateMCP(row.id, { command: e.target.value })
                        }
                        placeholder={t("config.mcpCommand")}
                        className="w-full rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
                      />
                    ) : (
                      <input
                        value={row.url}
                        onChange={(e) => updateMCP(row.id, { url: e.target.value })}
                        placeholder={t("config.mcpURL")}
                        className="w-full rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
                      />
                    )}
                    <input
                      value={row.argsText}
                      onChange={(e) =>
                        updateMCP(row.id, { argsText: e.target.value })
                      }
                      placeholder={t("config.mcpArgs")}
                      className="w-full rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
                    />
                    <textarea
                      value={row.envText}
                      onChange={(e) =>
                        updateMCP(row.id, { envText: e.target.value })
                      }
                      placeholder={t("config.mcpEnv")}
                      rows={2}
                      className="w-full resize-none rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
                    />
                  </div>
                ))}
              </div>
              <div className="flex items-center gap-2">
                <button
                  onClick={() =>
                    setMCPRows((prev) => [
                      ...prev,
                      {
                        id: newID(),
                        name: "",
                        transport: "stdio",
                        command: "",
                        url: "",
                        argsText: "",
                        envText: "",
                      },
                    ])
                  }
                  className="flex items-center gap-1.5 rounded-lg border border-edge px-3 py-1.5 text-sm text-dim hover:text-fg"
                >
                  <Plus size={14} />
                  {t("config.mcpAdd")}
                </button>
                <button
                  onClick={() => void saveMCP()}
                  className="rounded-lg bg-accent px-4 py-1.5 text-sm text-white hover:opacity-90"
                >
                  {t("setup.saveApply")}
                </button>
                {mcpError && (
                  <span className="text-xs text-err">{mcpError}</span>
                )}
              </div>
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
                        ["24h", "24h"],
                        ["7d", "7d"],
                        ["30d", "30d"],
                        ["all", t("config.usageRangeAll")],
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
                    <span className="w-px h-5 bg-edge" />
                    <div className="flex overflow-hidden rounded-lg border border-edge text-xs">
                      <button
                        onClick={() => setUsageGranularity("hour")}
                        className={`px-3 py-1.5 ${
                          usageGranularity === "hour"
                            ? "bg-accent text-white"
                            : "text-dim hover:text-fg"
                        }`}
                      >
                        {t("config.usageHour")}
                      </button>
                      <button
                        onClick={() => setUsageGranularity("day")}
                        className={`px-3 py-1.5 ${
                          usageGranularity === "day"
                            ? "bg-accent text-white"
                            : "text-dim hover:text-fg"
                        }`}
                      >
                        {t("config.usageDay")}
                      </button>
                    </div>
                    {usageLoading && (
                      <Loader2 size={14} className="animate-spin text-dim" />
                    )}
                  </div>
                  <UsageChart points={usageSeries} granularity={usageGranularity} />
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

          {tab === "agents" && (
            <div className="space-y-3">
              <p className="text-xs text-dim">{t("config.agentsHint")}</p>
              {agents.length === 0 ? (
                <p className="text-sm text-dim">{t("config.agentsEmpty")}</p>
              ) : (
                agents.map((a) => (
                  <div
                    key={a.name}
                    className="flex items-start gap-3 rounded-xl border border-edge bg-panel2 p-3"
                  >
                    <Bot
                      size={16}
                      className="text-accent mt-0.5 shrink-0"
                    />
                    <div className="flex-1 min-w-0">
                      <div className="text-sm font-medium">{a.name}</div>
                      <p className="text-xs text-dim mt-0.5">
                        {a.description}
                      </p>
                    </div>
                    <button
                      onClick={() => setConfirmDelete(a.name)}
                      className="flex items-center gap-1 rounded-lg border border-edge px-2.5 py-1 text-xs text-dim hover:text-err hover:border-err/40"
                    >
                      <Trash2 size={12} />
                      {t("config.agentsDelete")}
                    </button>
                  </div>
                ))
              )}
              {confirmDelete && (
                <div className="rounded-xl border border-err/40 bg-panel2 p-4">
                  <p className="text-sm">
                    {t("config.agentsDeleteConfirm", { name: confirmDelete })}
                  </p>
                  <div className="mt-3 flex gap-2">
                    <button
                      onClick={() => setConfirmDelete(null)}
                      className="rounded-lg border border-edge px-4 py-1.5 text-sm text-dim hover:text-fg"
                    >
                      {t("interact.cancel")}
                    </button>
                    <button
                      onClick={() => void deleteAgent(confirmDelete)}
                      className="rounded-lg bg-err px-4 py-1.5 text-sm text-white hover:opacity-90"
                    >
                      {t("config.agentsDelete")}
                    </button>
                  </div>
                </div>
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

          {tab === "skills" && (
            <div className="space-y-3">
              <p className="text-xs text-dim">{t("config.skillsHint")}</p>
              {skills.length === 0 ? (
                <p className="text-sm text-dim">{t("config.skillsEmpty")}</p>
              ) : (
                skills.map((s) => (
                  <div
                    key={s.name}
                    className="rounded-xl border border-edge bg-panel2 p-3"
                    title={s.path}
                  >
                    <div className="flex items-center gap-2">
                      <span className="text-sm font-medium">{s.name}</span>
                      <span className="rounded bg-panel border border-edge px-1.5 text-xs text-dim">
                        {s.scope}
                      </span>
                    </div>
                    {s.description && (
                      <p className="text-xs text-dim mt-1">{s.description}</p>
                    )}
                  </div>
                ))
              )}
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
