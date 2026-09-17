import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Check,
  ChevronDown,
  Film,
  Image as ImageIcon,
  Loader2,
  Wrench,
} from 'lucide-react';
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

const controlClass =
  'w-full rounded-lg border border-edge bg-panel px-2 py-1 text-xs text-fg ' +
  'outline-none transition-colors hover:border-accent/50 focus:border-accent ' +
  'disabled:opacity-40';

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

// ToolOptionMenu is the settings page's listbox pattern (a floating menu
// with a check mark, not a native select) for one provider knob.
function ToolOptionMenu({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: string;
  options: { value: string; label: string }[];
  onChange: (value: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const selected = options.find((option) => option.value === value);

  useEffect(() => {
    if (!open) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false);
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [open]);

  return (
    <div ref={rootRef} className="relative">
      <button
        type="button"
        aria-label={label}
        aria-haspopup="listbox"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className="flex h-7 w-full items-center gap-1.5 rounded-lg border border-edge bg-panel px-2 text-xs text-fg outline-none transition-colors hover:border-accent/60 focus:border-accent"
      >
        <span
          className={`min-w-0 flex-1 truncate text-left ${
            selected ? '' : 'text-dim'
          }`}
        >
          {selected?.label ?? ''}
        </span>
        <ChevronDown
          size="0.8571rem"
          className={`shrink-0 text-dim transition-transform ${
            open ? 'rotate-180' : ''
          }`}
        />
      </button>
      {open && (
        <>
          <div
            className="fixed inset-0 z-30"
            onMouseDown={() => setOpen(false)}
          />
          <div
            role="listbox"
            className="absolute top-full right-0 z-40 mt-1 min-w-full rounded-lg border border-edge/80 bg-panel/95 p-1 shadow-xl backdrop-blur-md"
          >
            {options.map((option) => {
              const isSelected = option.value === value;
              return (
                <button
                  key={option.value || 'unset'}
                  type="button"
                  role="option"
                  aria-selected={isSelected}
                  onClick={() => {
                    onChange(option.value);
                    setOpen(false);
                  }}
                  className={`flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-xs transition-colors ${
                    isSelected
                      ? 'bg-accent/10 text-accent'
                      : 'text-dim hover:bg-panel2 hover:text-fg'
                  }`}
                >
                  <span
                    className={`min-w-0 flex-1 truncate ${
                      option.value === '' ? '' : 'font-mono'
                    }`}
                  >
                    {option.label}
                  </span>
                  {isSelected && (
                    <Check size="0.8571rem" className="shrink-0" />
                  )}
                </button>
              );
            })}
          </div>
        </>
      )}
    </div>
  );
}

// boundsOf describes a numeric field's accepted range for the hint line.
function boundsOf(field: ToolOptionFieldView): string {
  const lower = field.min ?? undefined;
  const upper = field.max ?? undefined;
  if (lower === undefined && upper === undefined) return '';
  const lowerText = lower === undefined ? '' : `${lower}`;
  const upperText = upper === undefined ? '' : `${upper}`;
  if (
    lower !== undefined &&
    upper !== undefined &&
    !field.exclusive_min &&
    !field.exclusive_max
  ) {
    return `${lowerText}–${upperText}`;
  }
  const parts: string[] = [];
  if (lower !== undefined) {
    parts.push(`${field.exclusive_min ? '>' : '≥'} ${lowerText}`);
  }
  if (upper !== undefined) {
    parts.push(`${field.exclusive_max ? '<' : '≤'} ${upperText}`);
  }
  return parts.join(', ');
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
  const savedTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

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
      if (savedTimer.current) clearTimeout(savedTimer.current);
    };
  }, []);

  const markSaved = (tool: ToolKey) => {
    setSaved(tool);
    if (savedTimer.current) clearTimeout(savedTimer.current);
    savedTimer.current = setTimeout(() => setSaved(null), 2500);
  };

  // setField stores one knob; an undefined or empty value clears it,
  // which the host writes as "provider default".
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
      markSaved(tool);
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
    const hint =
      field.kind === 'enum'
        ? (field.values ?? []).join(' · ')
        : boundsOf(field);
    let control;
    if (field.kind === 'enum' || field.kind === 'bool') {
      const options =
        field.kind === 'enum'
          ? [
              { value: '', label: t('config.toolsUnset') },
              ...(field.values ?? []).map((option) => ({
                value: option,
                label: option,
              })),
            ]
          : [
              { value: '', label: t('config.toolsUnset') },
              { value: 'true', label: t('config.toolsOn') },
              { value: 'false', label: t('config.toolsOff') },
            ];
      const current =
        field.kind === 'bool'
          ? value === undefined
            ? ''
            : value === true
              ? 'true'
              : 'false'
          : typeof value === 'string'
            ? value
            : '';
      control = (
        <ToolOptionMenu
          label={label}
          value={current}
          options={options}
          onChange={(next) => {
            if (field.kind !== 'bool') {
              setField(tool, instance.id, field.name, next);
              return;
            }
            setField(
              tool,
              instance.id,
              field.name,
              next === '' ? undefined : next === 'true',
            );
          }}
        />
      );
    } else {
      control = (
        <input
          aria-label={label}
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
          className={controlClass}
        />
      );
    }
    return (
      <div
        key={field.name}
        className="flex items-center gap-4 px-3 py-2 hover:bg-panel2/40"
      >
        <div className="min-w-0 flex-1">
          <span className="block truncate text-xs text-fg">{label}</span>
          <span className="block truncate font-mono text-[0.7143rem] text-dim/80">
            {field.name}
            {hint && <span className="ml-1.5 text-dim/60">· {hint}</span>}
          </span>
        </div>
        <div className="w-40 shrink-0">{control}</div>
      </div>
    );
  };

  const renderInstance = (tool: ToolKey, instance: ToolOptionInstanceView) => (
    <div
      key={instance.id}
      className="overflow-hidden rounded-lg border border-edge/70 bg-panel/40"
    >
      <div className="flex items-center gap-2 border-b border-edge/60 px-3 py-2">
        <span className="truncate text-xs font-medium text-fg">
          {instance.label}
        </span>
        {instance.managed && (
          <span className="shrink-0 rounded-full border border-edge px-1.5 py-0.5 text-[0.7143rem] text-dim">
            {t('config.toolsManaged')}
          </span>
        )}
        <span className="flex-1" />
        <code className="shrink-0 font-mono text-[0.7143rem] text-dim">
          {instance.id}
        </code>
      </div>
      {(instance.fields ?? []).length === 0 ? (
        <p className="px-3 py-2.5 text-xs text-dim/80">
          {t('config.toolsNoFields')}
        </p>
      ) : (
        <div className="divide-y divide-edge/50">
          {(instance.fields ?? []).map((field) =>
            renderField(tool, instance, field),
          )}
        </div>
      )}
    </div>
  );

  const renderCard = (tool: ToolKey, view: ToolOptionsToolView | undefined) => {
    const instances = view?.instances ?? [];
    const Icon = tool === 'image' ? ImageIcon : Film;
    return (
      <section className="rounded-xl border border-edge bg-panel2 p-3">
        <header className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex items-center gap-2 text-sm font-medium">
              <Icon size="1.0000rem" className="shrink-0 text-accent" />
              {t(
                tool === 'image'
                  ? 'config.toolsImageTitle'
                  : 'config.toolsVideoTitle',
              )}
            </div>
            <p className="mt-1 text-xs text-dim/80">
              {t(
                tool === 'image'
                  ? 'config.toolsImageHint'
                  : 'config.toolsVideoHint',
              )}
            </p>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            {saved === tool && (
              <span className="inline-flex items-center gap-1 text-[0.7143rem] text-ok">
                <Check size="0.8571rem" />
                {t('config.toolsSaved')}
              </span>
            )}
            <button
              type="button"
              onClick={() => void save(tool)}
              disabled={saving !== null || instances.length === 0}
              className="flex items-center gap-1.5 rounded-lg bg-accent px-3 py-1.5 text-xs text-white hover:opacity-90 disabled:opacity-40"
            >
              {saving === tool && (
                <Loader2 size="0.8571rem" className="animate-spin" />
              )}
              {t('setup.saveApply')}
            </button>
          </div>
        </header>
        <div className="mt-3 space-y-2">
          {instances.length === 0 ? (
            <div className="flex items-center justify-center gap-2 rounded-lg border border-dashed border-edge/70 px-3 py-6 text-xs text-dim/80">
              <Wrench size="0.9286rem" className="shrink-0" />
              {t('config.toolsNoInstances')}
            </div>
          ) : (
            instances.map((instance) => renderInstance(tool, instance))
          )}
        </div>
      </section>
    );
  };

  if (state === null && error === '') {
    return (
      <div className="space-y-3">
        {[0, 1].map((key) => (
          <div
            key={key}
            className="h-40 animate-pulse rounded-xl border border-edge/70 bg-panel/70"
          />
        ))}
      </div>
    );
  }
  return (
    <div className="space-y-3">
      {renderCard('image', state?.image)}
      {renderCard('video', state?.video)}
      {error !== '' && (
        <p className="whitespace-pre-wrap break-all rounded-lg border border-err/40 bg-err/5 px-3 py-2 text-xs text-err">
          {error}
        </p>
      )}
    </div>
  );
}
