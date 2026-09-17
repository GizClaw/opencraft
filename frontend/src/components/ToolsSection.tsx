import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2, Wrench } from 'lucide-react';
import { api } from '../lib/api';
import type {
  ToolOptionFieldView,
  ToolOptionInstanceView,
  ToolOptionsRequest,
  ToolOptionsState,
  ToolOptionsToolView,
} from '../lib/types';

// The tools tab: provider-specific settings for the generation tools.
// The model-facing tools keep the common parameters; the knobs a driver
// models beyond that live here, per deployment.

type ToolValues = Record<string, Record<string, unknown>>;
type ToolKey = 'image' | 'video';

// fieldLabelKey maps a dotted field path onto its translation key.
function fieldLabelKey(name: string): string {
  return `config.toolField.${name.replace(/\./g, '_')}`;
}

// initialValues renders the stored values of one tool's instances.
function initialValues(tool: ToolOptionsToolView | undefined): ToolValues {
  const out: ToolValues = {};
  for (const instance of tool?.instances ?? []) {
    out[instance.id] = { ...(instance.values ?? {}) };
  }
  return out;
}

export function ToolsSection() {
  const { t } = useTranslation();
  const [state, setState] = useState<ToolOptionsState | null>(null);
  const [values, setValues] = useState<Record<ToolKey, ToolValues>>({
    image: {},
    video: {},
  });
  const [error, setError] = useState('');
  const [saving, setSaving] = useState<ToolKey | null>(null);
  const [saved, setSaved] = useState<ToolKey | null>(null);

  useEffect(() => {
    let live = true;
    void api
      .toolOptions()
      .then((st) => {
        if (!live) return;
        setState(st);
        setValues({
          image: initialValues(st.image),
          video: initialValues(st.video),
        });
      })
      .catch((err) => {
        if (live) setError(String(err));
      });
    return () => {
      live = false;
    };
  }, []);

  // setField stores one knob; an undefined value clears it, which the
  // host writes as "provider default".
  const setField = (
    tool: ToolKey,
    instanceID: string,
    name: string,
    value: unknown,
  ) => {
    setSaved(null);
    setValues((prev) => {
      const toolValues = { ...prev[tool] };
      const next = { ...(toolValues[instanceID] ?? {}) };
      if (value === undefined || value === '') {
        delete next[name];
      } else {
        next[name] = value;
      }
      toolValues[instanceID] = next;
      return { ...prev, [tool]: toolValues };
    });
  };

  const save = async (tool: ToolKey) => {
    setSaving(tool);
    setSaved(null);
    try {
      // Both cards travel together: the host replaces each tool's block,
      // so saving one must carry the other's current values unchanged.
      const req: ToolOptionsRequest = {
        image: values.image,
        video: values.video,
      };
      await api.saveToolOptions(req);
      setError('');
      const st = await api.toolOptions();
      setState(st);
      setValues({
        image: initialValues(st.image),
        video: initialValues(st.video),
      });
      setSaved(tool);
    } catch (err) {
      setError(String(err));
    } finally {
      setSaving(null);
    }
  };

  const renderField = (
    tool: ToolKey,
    instance: ToolOptionInstanceView,
    field: ToolOptionFieldView,
  ) => {
    const value = values[tool][instance.id]?.[field.name];
    const label = t(fieldLabelKey(field.name), { defaultValue: field.name });
    const bounds =
      field.kind === 'int' || field.kind === 'float'
        ? [field.min, field.max]
            .map((bound) =>
              bound === undefined || bound === null ? '' : bound,
            )
            .filter((bound) => bound !== '')
            .join('–')
        : '';
    let control;
    if (field.kind === 'enum') {
      control = (
        <select
          value={typeof value === 'string' ? value : ''}
          onChange={(e) =>
            setField(tool, instance.id, field.name, e.target.value)
          }
          className="w-full rounded-lg border border-edge bg-panel px-2 py-1.5 text-xs outline-none focus:border-accent"
        >
          <option value="">{t('config.toolsUnset')}</option>
          {(field.values ?? []).map((option) => (
            <option key={option} value={option}>
              {option}
            </option>
          ))}
        </select>
      );
    } else if (field.kind === 'bool') {
      const current =
        value === undefined ? '' : value === true ? 'true' : 'false';
      control = (
        <select
          value={current}
          onChange={(e) => {
            const raw = e.target.value;
            setField(
              tool,
              instance.id,
              field.name,
              raw === '' ? undefined : raw === 'true',
            );
          }}
          className="w-full rounded-lg border border-edge bg-panel px-2 py-1.5 text-xs outline-none focus:border-accent"
        >
          <option value="">{t('config.toolsUnset')}</option>
          <option value="true">{t('config.toolsOn')}</option>
          <option value="false">{t('config.toolsOff')}</option>
        </select>
      );
    } else {
      control = (
        <input
          type={field.kind === 'string' ? 'text' : 'number'}
          value={value === undefined ? '' : String(value)}
          min={field.min ?? undefined}
          max={field.max ?? undefined}
          step={field.kind === 'float' ? 'any' : undefined}
          onChange={(e) => {
            const raw = e.target.value.trim();
            setField(
              tool,
              instance.id,
              field.name,
              raw === ''
                ? undefined
                : field.kind === 'string'
                  ? raw
                  : Number(raw),
            );
          }}
          className="w-full rounded-lg border border-edge bg-panel px-2 py-1.5 text-xs outline-none focus:border-accent"
        />
      );
    }
    return (
      <label key={field.name} className="min-w-0 space-y-1">
        <span className="flex items-baseline gap-1.5">
          <span className="text-xs text-dim">{label}</span>
          {bounds && (
            <span className="text-[0.7143rem] text-dim/70">{bounds}</span>
          )}
        </span>
        {control}
      </label>
    );
  };

  const renderCard = (tool: ToolKey, view: ToolOptionsToolView | undefined) => (
    <section className="space-y-3 rounded-xl border border-edge bg-panel2 p-4">
      <header className="flex items-center gap-2">
        <Wrench size="1rem" className="shrink-0 text-accent" />
        <h3 className="text-sm font-medium">
          {t(
            tool === 'image'
              ? 'config.toolsImageTitle'
              : 'config.toolsVideoTitle',
          )}
        </h3>
      </header>
      <p className="text-xs text-dim">
        {t(
          tool === 'image' ? 'config.toolsImageHint' : 'config.toolsVideoHint',
        )}
      </p>
      {(view?.instances ?? []).length === 0 && (
        <p className="text-xs text-dim">{t('config.toolsNoInstances')}</p>
      )}
      {(view?.instances ?? []).map((instance) => (
        <div
          key={instance.id}
          className="space-y-2 rounded-lg border border-edge bg-panel p-3"
        >
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm text-fg">{instance.label}</span>
            <code className="rounded bg-panel2 px-1.5 py-0.5 text-[0.7143rem] text-dim">
              {instance.id}
            </code>
            {instance.managed && (
              <span className="rounded bg-panel2 px-1.5 py-0.5 text-[0.7143rem] text-dim">
                {t('config.toolsManaged')}
              </span>
            )}
          </div>
          {(instance.fields ?? []).length === 0 ? (
            <p className="text-xs text-dim">{t('config.toolsNoFields')}</p>
          ) : (
            <div className="grid gap-2 sm:grid-cols-2">
              {(instance.fields ?? []).map((field) =>
                renderField(tool, instance, field),
              )}
            </div>
          )}
        </div>
      ))}
      <div className="flex items-center gap-3">
        <button
          type="button"
          onClick={() => void save(tool)}
          disabled={saving !== null}
          className="flex items-center gap-1.5 rounded-lg bg-accent px-4 py-1.5 text-sm text-white hover:opacity-90 disabled:opacity-40"
        >
          {saving === tool && (
            <Loader2 size="1.0000rem" className="animate-spin" />
          )}
          {t('setup.saveApply')}
        </button>
        {saved === tool && (
          <span className="text-xs text-ok">{t('config.toolsSaved')}</span>
        )}
      </div>
    </section>
  );

  if (state === null && error === '') {
    return (
      <div className="flex items-center justify-center gap-2 p-4 text-dim">
        <Loader2 size="0.9286rem" className="animate-spin" />
      </div>
    );
  }
  return (
    <div className="space-y-3">
      {renderCard('image', state?.image)}
      {renderCard('video', state?.video)}
      {error !== '' && (
        <p className="whitespace-pre-wrap break-all text-xs text-err">
          {error}
        </p>
      )}
    </div>
  );
}
