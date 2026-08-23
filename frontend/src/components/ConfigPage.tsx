import { useEffect, useMemo, useState } from "react";
import {
  ArrowDown,
  ArrowUp,
  Bot,
  Loader2,
  Settings,
  Trash2,
  X,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { api } from "../lib/api";
import { useStore } from "../lib/store";
import type { ProviderView, SetupProvider } from "../lib/types";

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

type Tab = "ui" | "inference" | "agents" | "permissions" | "skills";

export function ConfigPage() {
  const configured = useStore((s) => s.configured);
  const closeConfig = useStore((s) => s.closeConfig);
  const agents = useStore((s) => s.agents);
  const refreshAgents = useStore((s) => s.refreshAgents);
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

  const tabs: { id: Tab; label: string }[] = [
    { id: "ui", label: t("config.tabUi") },
    { id: "inference", label: t("config.tabInference") },
    { id: "agents", label: t("config.tabAgents") },
    { id: "permissions", label: t("config.tabPermissions") },
    { id: "skills", label: t("config.tabSkills") },
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
