import { useEffect, useMemo, useState } from "react";
import { ArrowDown, ArrowUp, Loader2, X } from "lucide-react";
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

export function Onboarding() {
  const configured = useStore((s) => s.configured);
  const closeOnboarding = useStore((s) => s.closeOnboarding);
  const [rows, setRows] = useState<Row[]>([]);
  const [order, setOrder] = useState<string[]>([]);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    void api.providers().then((providers) => {
      setRows(
        providers.map((p) => ({
          provider: p,
          key: "",
          keyEnv: false,
          model: p.default_model,
          endpoint: "",
          vision: false,
          reasoning: "",
          webSearch: false,
        })),
      );
    });
  }, []);

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
    setRows((prev) => prev.map((r) => (r.provider.id === id ? { ...r, ...patch } : r)));
  };

  const save = async () => {
    setError("");
    if (selectedRows.length === 0) {
      setError("至少选择一个 provider");
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
      // rebuild emits "ready" / "onboarding_required"; close optimistically
      closeOnboarding();
    } catch (err) {
      setError(String(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 bg-black/70 grid place-items-center">
      <div className="w-[680px] max-h-[85vh] flex flex-col rounded-2xl border border-edge bg-panel shadow-2xl">
        <div className="flex items-center justify-between px-5 py-4 border-b border-edge">
          <div>
            <h2 className="text-base font-semibold">推理配置</h2>
            <p className="text-xs text-dim mt-0.5">
              勾选你拥有的 provider，按优先级排序；配置写入
              ~/.opencraft/config/opencraft.yaml
            </p>
          </div>
          {configured && (
            <button onClick={closeOnboarding} className="text-dim hover:text-fg">
              <X size={18} />
            </button>
          )}
        </div>

        <div className="flex-1 overflow-y-auto px-5 py-4 space-y-3">
          {rows.map((row) => (
            <div
              key={row.provider.id}
              className="rounded-xl border border-edge bg-panel2 overflow-hidden"
            >
              <label className="flex items-center gap-2.5 px-4 py-3 cursor-pointer">
                <input
                  type="checkbox"
                  checked={order.includes(row.provider.id)}
                  onChange={(e) => toggle(row.provider.id, e.target.checked)}
                  className="accent-[var(--color-accent)]"
                />
                <span className="font-medium text-sm">{row.provider.name}</span>
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
                      onChange={(e) => update(row.provider.id, { key: e.target.value })}
                      disabled={row.keyEnv}
                      placeholder={`API Key（或使用 $${row.provider.env_var}）`}
                      className="flex-1 rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent disabled:opacity-40"
                    />
                    <label className="flex items-center gap-1.5 text-xs text-dim whitespace-nowrap">
                      <input
                        type="checkbox"
                        checked={row.keyEnv}
                        onChange={(e) => update(row.provider.id, { keyEnv: e.target.checked })}
                        className="accent-[var(--color-accent)]"
                      />
                      环境变量 ${row.provider.env_var}
                    </label>
                  </div>
                  {row.provider.azure ? (
                    <>
                      <div className="flex gap-2">
                        <input
                          value={row.endpoint}
                          onChange={(e) => update(row.provider.id, { endpoint: e.target.value })}
                          placeholder="Endpoint，如 https://xxx.openai.azure.com"
                          className="flex-1 rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
                        />
                        <input
                          value={row.model}
                          onChange={(e) => update(row.provider.id, { model: e.target.value })}
                          placeholder="Deployment 名称"
                          className="w-48 rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
                        />
                      </div>
                      <div className="flex items-center gap-4 text-xs text-dim">
                        <label className="flex items-center gap-1.5">
                          <input
                            type="checkbox"
                            checked={row.vision}
                            onChange={(e) => update(row.provider.id, { vision: e.target.checked })}
                            className="accent-[var(--color-accent)]"
                          />
                          图像输入
                        </label>
                        <label className="flex items-center gap-1.5">
                          推理
                          <select
                            value={row.reasoning}
                            onChange={(e) => update(row.provider.id, { reasoning: e.target.value })}
                            className="rounded border border-edge bg-panel px-2 py-1 outline-none"
                          >
                            <option value="">关闭</option>
                            <option value="always">always</option>
                            <option value="toggle">toggle</option>
                          </select>
                        </label>
                        <label className="flex items-center gap-1.5">
                          <input
                            type="checkbox"
                            checked={row.webSearch}
                            onChange={(e) => update(row.provider.id, { webSearch: e.target.checked })}
                            className="accent-[var(--color-accent)]"
                          />
                          hosted web search
                        </label>
                      </div>
                    </>
                  ) : (
                    <div className="flex items-center gap-2">
                      <span className="text-xs text-dim">模型</span>
                      <input
                        value={row.model}
                        onChange={(e) => update(row.provider.id, { model: e.target.value })}
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
                路由优先级（上方优先，失败自动回退）
              </div>
              <div className="space-y-1.5">
                {selectedRows.map((row, idx) => (
                  <div
                    key={row.provider.id}
                    className="flex items-center gap-2 rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm"
                  >
                    <span className="text-xs text-dim w-5">{idx + 1}</span>
                    <span className="flex-1">{row.provider.name}</span>
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

        <div className="px-5 py-4 border-t border-edge flex items-center gap-3">
          {error && <span className="text-xs text-err flex-1">{error}</span>}
          <span className="flex-1" />
          <button
            onClick={() => void save()}
            disabled={saving}
            className="flex items-center gap-1.5 rounded-lg bg-accent px-5 py-2 text-sm text-white hover:opacity-90 disabled:opacity-50"
          >
            {saving && <Loader2 size={14} className="animate-spin" />}
            保存并应用
          </button>
        </div>
      </div>
    </div>
  );
}
