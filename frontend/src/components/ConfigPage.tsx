import { useEffect, useMemo, useState } from "react";
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
import type { MCPServer, ProviderView, SetupProvider } from "../lib/types";

interface Row {
  provider: ProviderView;
  key: string;
  keyEnv: boolean;
  model: string;
  endpoint: string;
  vision: boolean;
  reasoning: string;
  webSearch: boolean;
}

type Tab =
  | "ui"
  | "inference"
  | "mcp"
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
  const [rows, setRows] = useState<Row[]>([]);
  const [order, setOrder] = useState<string[]>([]);
  const [defaultModel, setDefaultModel] = useState("");
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [rules, setRules] = useState<string[]>([]);
  const [ruleInput, setRuleInput] = useState("");
  const [skills, setSkills] = useState<{ name: string; description: string; scope: string; path: string }[]>([]);
  const [logs, setLogs] = useState("");
  const [mcpRows, setMCPRows] = useState<MCPRow[]>([]);
  const [mcpError, setMCPError] = useState("");

  useEffect(() => {
    void (async () => {
      try {
        const [providers, state] = await Promise.all([
          api.providers(),
          api.configState(),
        ]);
        setRows(
          providers.map((p) => {
            const cur = state.providers.find((s) => s.id === p.id);
            return {
              provider: p,
              key: cur?.key ?? "",
              keyEnv: cur?.key_env ?? false,
              model: cur?.model || p.default_model,
              endpoint: cur?.endpoint ?? "",
              vision: cur?.vision ?? false,
              reasoning: cur?.reasoning ?? "",
              webSearch: cur?.web_search ?? false,
            };
          }),
        );
        setOrder(state.providers.map((s) => s.id));
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

  useEffect(() => {
    if (tab !== "mcp") return;
    void api
      .mcpConfig()
      .then((servers) =>
        setMCPRows(
          servers.map((s) => ({
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

  const byID = useMemo(
    () => new Map(rows.map((r) => [r.provider.id, r])),
    [rows],
  );
  const selectedRows = order
    .map((id) => byID.get(id))
    .filter((r): r is Row => Boolean(r));

  const toggle = (id: string, on: boolean) => {
    setOrder((prev) =>
      on ? [...prev.filter((x) => x !== id), id] : prev.filter((x) => x !== id),
    );
  };

  const move = (idx: number, dir: -1 | 1) => {
    const target = idx + dir;
    if (target < 0 || target >= selectedRows.length) return;
    setOrder((prev) => {
      const next = [...prev];
      const id = prev[idx];
      next[idx] = prev[target];
      next[target] = id;
      return next;
    });
  };

  const update = (id: string, patch: Partial<Row>) => {
    setRows((prev) =>
      prev.map((r) => (r.provider.id === id ? { ...r, ...patch } : r)),
    );
  };

  const save = async () => {
    setError("");
    if (selectedRows.length === 0) {
      setError(t("setup.selectProvider"));
      return;
    }
    const providers: SetupProvider[] = selectedRows.map((r) => ({
      id: r.provider.id,
      key: r.key,
      key_env: r.keyEnv,
      model: r.model,
      endpoint: r.endpoint,
      vision: r.vision,
      reasoning: r.reasoning,
      web_search: r.webSearch,
    }));
    setSaving(true);
    try {
      await api.saveSetup({ providers });
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
    { id: "agents", label: t("config.tabAgents") },
    { id: "permissions", label: t("config.tabPermissions") },
    { id: "skills", label: t("config.tabSkills") },
    { id: "logs", label: t("config.tabLogs") },
  ];

  return (
    <div className="fixed inset-0 z-50 bg-black/70 grid place-items-center">
      <div className="w-[720px] max-h-[86vh] flex flex-col rounded-2xl border border-edge bg-panel shadow-2xl">
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
              {rows.map((row) => (
                <div
                  key={row.provider.id}
                  className="rounded-xl border border-edge bg-panel2 overflow-hidden"
                >
                  <label className="flex items-center gap-2.5 px-4 py-3 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={order.includes(row.provider.id)}
                      onChange={(e) =>
                        toggle(row.provider.id, e.target.checked)
                      }
                      className="accent-[var(--color-accent)]"
                    />
                    <span className="font-medium text-sm">
                      {row.provider.name}
                    </span>
                    <span className="text-xs text-dim">
                      {row.provider.default_model}
                    </span>
                  </label>
                  {order.includes(row.provider.id) && (
                    <div className="px-4 pb-3 pt-1 space-y-2">
                      <div className="flex items-center gap-2">
                        <input
                          type="password"
                          value={row.key}
                          onChange={(e) =>
                            update(row.provider.id, { key: e.target.value })
                          }
                          disabled={row.keyEnv}
                          placeholder={t("setup.apiKeyPlaceholder", {
                            var: row.provider.env_var,
                          })}
                          className="flex-1 rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent disabled:opacity-40"
                        />
                        <label className="flex items-center gap-1.5 text-xs text-dim whitespace-nowrap">
                          <input
                            type="checkbox"
                            checked={row.keyEnv}
                            onChange={(e) =>
                              update(row.provider.id, {
                                keyEnv: e.target.checked,
                              })
                            }
                            className="accent-[var(--color-accent)]"
                          />
                          {t("setup.envVar", { var: row.provider.env_var })}
                        </label>
                      </div>
                      {row.provider.azure ? (
                        <>
                          <div className="flex gap-2">
                            <input
                              value={row.endpoint}
                              onChange={(e) =>
                                update(row.provider.id, {
                                  endpoint: e.target.value,
                                })
                              }
                              placeholder={t("setup.endpointPlaceholder")}
                              className="flex-1 rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
                            />
                            <input
                              value={row.model}
                              onChange={(e) =>
                                update(row.provider.id, {
                                  model: e.target.value,
                                })
                              }
                              placeholder={t("setup.deploymentName")}
                              className="w-48 rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
                            />
                          </div>
                          <div className="flex items-center gap-4 text-xs text-dim">
                            <label className="flex items-center gap-1.5">
                              <input
                                type="checkbox"
                                checked={row.vision}
                                onChange={(e) =>
                                  update(row.provider.id, {
                                    vision: e.target.checked,
                                  })
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
                                  update(row.provider.id, {
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
                                  update(row.provider.id, {
                                    webSearch: e.target.checked,
                                  })
                                }
                                className="accent-[var(--color-accent)]"
                              />
                              {t("setup.webSearch")}
                            </label>
                          </div>
                        </>
                      ) : (
                        <div className="flex items-center gap-2">
                          <span className="text-xs text-dim">
                            {t("setup.model")}
                          </span>
                          <input
                            value={row.model}
                            onChange={(e) =>
                              update(row.provider.id, { model: e.target.value })
                            }
                            className="w-64 rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
                          />
                        </div>
                      )}
                    </div>
                  )}
                </div>
              ))}

              {selectedRows.length > 1 && (
                <div className="rounded-xl border border-edge bg-panel2 p-3">
                  <div className="text-xs text-dim mb-2">
                    {t("setup.routerPriority")}
                  </div>
                  <div className="space-y-1.5">
                    {selectedRows.map((row, idx) => (
                      <div
                        key={row.provider.id}
                        className="flex items-center gap-2 rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm"
                      >
                        <span className="text-xs text-dim w-5">{idx + 1}</span>
                        <span className="flex-1">{row.provider.name}</span>
                        <span className="text-xs text-dim truncate">
                          {row.model || row.provider.default_model}
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
                          disabled={idx === selectedRows.length - 1}
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
            <div className="space-y-3">
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
                <pre className="rounded-xl border border-edge bg-panel2 p-3 text-xs whitespace-pre-wrap break-all font-mono max-h-[55vh] overflow-y-auto">
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
