import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Check,
  ChevronDown,
  ChevronRight,
  Film,
  Image as ImageIcon,
  Loader2,
  X,
} from 'lucide-react';
import { api } from '../lib/api';
import type {
  ToolOptionFieldView,
  ToolOptionInstanceView,
  ToolOptionPresetView,
  ToolOptionsRequest,
  ToolOptionsState,
  ToolOptionsToolView,
} from '../lib/types';

// The generation tools section of the Tools tab: one item per tool, the
// way the MCP list works. Clicking an item opens a dialog with the
// provider-specific settings that deployment configured; the
// model-facing tools keep only the common parameters.

type ToolValues = Record<string, Record<string, unknown>>;
type ToolKey = 'image' | 'video';

const controlClass =
  'w-full rounded-lg border border-edge bg-panel px-2 py-1 text-xs text-fg ' +
  'outline-none transition-colors hover:border-accent/50 focus:border-accent ' +
  'disabled:opacity-40';

const fieldClass =
  'w-full rounded-lg border border-edge bg-panel px-2.5 py-1.5 text-xs text-fg ' +
  'outline-none transition-colors hover:border-accent/50 focus:border-accent';

const TOOLS: ToolKey[] = ['image', 'video'];

// fieldLabelKey maps a dotted field path onto its translation key.
function fieldLabelKey(name: string): string {
  return `config.toolField.${name.replace(/\./g, '_')}`;
}

// fieldDescriptionKey maps a dotted field path onto the optional
// one-line explanation shown under the label.
function fieldDescriptionKey(name: string): string {
  return `config.toolFieldDesc.${name.replace(/\./g, '_')}`;
}

// displayDefault renders a documented provider default for the hint
// line: booleans read as on/off, everything else verbatim.
function displayDefault(
  field: ToolOptionFieldView,
  on: string,
  off: string,
): string | null {
  if (!field.default) return null;
  if (field.kind === 'bool') {
    return field.default === 'true' ? on : off;
  }
  return field.default;
}

// initialValues renders the stored values of one tool's instances.
function initialValues(tool: ToolOptionsToolView | undefined): ToolValues {
  const out: ToolValues = {};
  for (const instance of tool?.instances ?? []) {
    out[instance.id] = { ...(instance.values ?? {}) };
  }
  return out;
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
        className={controlClass}
      >
        <span className="flex items-center gap-1.5">
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
        </span>
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
  const [openTool, setOpenTool] = useState<ToolKey | null>(null);
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

  // applyPreset fills one provider's shortcut into the form. It merges
  // only the knobs the preset names, and nothing reaches the user layer
  // until the user saves the dialog.
  const applyPreset = (
    tool: ToolKey,
    instanceID: string,
    preset: ToolOptionPresetView,
  ) => {
    setSaved(null);
    setError('');
    setValues((prev) => {
      const toolValues = { ...prev[tool] };
      toolValues[instanceID] = {
        ...(toolValues[instanceID] ?? {}),
        ...preset.fields,
      };
      return { ...prev, [tool]: toolValues };
    });
  };

  const save = async (tool: ToolKey) => {
    setSaving(tool);
    setSaved(null);
    try {
      // Both tools travel together: the host replaces each one's block,
      // so saving the open dialog must carry the other's values unchanged.
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

  const title = (tool: ToolKey) =>
    t(tool === 'image' ? 'config.toolsImageTitle' : 'config.toolsVideoTitle');

  const renderField = (
    tool: ToolKey,
    instance: ToolOptionInstanceView,
    field: ToolOptionFieldView,
  ) => {
    const value = values[tool][instance.id]?.[field.name];
    const label = t(fieldLabelKey(field.name), { defaultValue: field.name });
    const description = t(fieldDescriptionKey(field.name), {
      defaultValue: '',
    });
    const providerDefault = t('config.toolsProviderDefault');
    const documented = displayDefault(
      field,
      t('config.toolsOn'),
      t('config.toolsOff'),
    );
    const defaultHint = documented
      ? t('config.toolsFieldDefault', { value: documented })
      : '';
    const hint =
      field.kind === 'enum'
        ? (field.values ?? []).join(' · ')
        : boundsOf(field);
    let control;
    if (field.kind === 'enum' || field.kind === 'bool') {
      const options =
        field.kind === 'enum'
          ? [
              { value: '', label: providerDefault },
              ...(field.values ?? []).map((option) => ({
                value: option,
                label: option,
              })),
            ]
          : [
              { value: '', label: providerDefault },
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
          placeholder={
            documented ? `${providerDefault} (${documented})` : providerDefault
          }
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
          className={fieldClass}
        />
      );
    }
    return (
      <div
        key={field.name}
        className="flex items-start gap-4 px-3 py-2 hover:bg-panel2/40"
      >
        <div className="min-w-0 flex-1">
          <span className="block truncate text-xs text-fg">{label}</span>
          <span className="block truncate font-mono text-[0.7143rem] text-dim/80">
            {field.name}
            {hint && <span className="ml-1.5 text-dim/60">· {hint}</span>}
            {defaultHint && (
              <span className="ml-1.5 text-dim/60">· {defaultHint}</span>
            )}
          </span>
          {description !== '' && (
            <span className="mt-0.5 block text-[0.7143rem] leading-snug text-dim/70">
              {description}
            </span>
          )}
        </div>
        <div className="w-40 shrink-0 self-center">{control}</div>
      </div>
    );
  };

  const renderInstance = (tool: ToolKey, instance: ToolOptionInstanceView) => (
    <div
      key={instance.id}
      className="overflow-hidden rounded-lg border border-edge/70 bg-panel2/40"
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
      {(instance.presets ?? []).length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5 border-b border-edge/60 px-3 py-2">
          <span className="text-[0.7143rem] text-dim/80">
            {t('config.toolsPresets')}
          </span>
          {(instance.presets ?? []).map((preset) => (
            <button
              key={preset.id}
              type="button"
              onClick={() => applyPreset(tool, instance.id, preset)}
              className="rounded-full border border-edge bg-panel px-2 py-0.5 text-[0.7143rem] text-dim transition-colors hover:border-accent/50 hover:text-fg"
            >
              {t(`config.toolPreset.${preset.id}`, { defaultValue: preset.id })}
            </button>
          ))}
        </div>
      )}
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

  const renderItem = (tool: ToolKey, view: ToolOptionsToolView | undefined) => {
    const instances = view?.instances ?? [];
    const configured = instances.reduce(
      (total, instance) => total + Object.keys(instance.values ?? {}).length,
      0,
    );
    const Icon = tool === 'image' ? ImageIcon : Film;
    return (
      <li
        key={tool}
        className="[content-visibility:auto] [contain-intrinsic-size:auto_4.5rem] rounded-xl border border-edge bg-panel2 p-3 transition-colors hover:border-accent/40"
      >
        <button
          type="button"
          onClick={() => {
            setSaved(null);
            setOpenTool(tool);
          }}
          className="flex w-full min-w-0 items-center gap-2 text-left"
        >
          <Icon size="0.9286rem" className="shrink-0 text-accent" />
          <span className="min-w-0 flex-1">
            <span className="flex flex-wrap items-center gap-1.5">
              <span className="min-w-0 truncate text-sm font-semibold">
                {title(tool)}
              </span>
              {configured > 0 && (
                <span className="shrink-0 rounded border border-edge bg-panel px-1.5 py-0.5 text-[0.7143rem] text-dim">
                  {t('config.toolsKnobCount', { count: configured })}
                </span>
              )}
            </span>
            <span className="mt-1 block truncate text-xs text-dim">
              {instances.length === 0
                ? t('config.toolsNoInstances')
                : instances.map((instance) => instance.label).join(' · ')}
            </span>
          </span>
          <ChevronRight size="1.0000rem" className="shrink-0 text-dim" />
        </button>
      </li>
    );
  };

  const renderDialog = (
    tool: ToolKey,
    view: ToolOptionsToolView | undefined,
  ) => {
    const instances = view?.instances ?? [];
    const Icon = tool === 'image' ? ImageIcon : Film;
    return (
      <div
        className="fixed inset-0 z-[60] grid place-items-center bg-black/60 p-6"
        onClick={() => setOpenTool(null)}
      >
        <div
          className="flex max-h-[calc(100vh-2rem)] w-[38rem] max-w-full flex-col rounded-2xl border border-edge bg-panel shadow-2xl"
          role="dialog"
          aria-modal="true"
          aria-label={title(tool)}
          onClick={(e) => e.stopPropagation()}
        >
          <div className="flex shrink-0 items-center justify-between gap-3 border-b border-edge px-4 py-3">
            <div className="flex min-w-0 items-center gap-2">
              <Icon size="1.0714rem" className="shrink-0 text-accent" />
              <h3 className="min-w-0 truncate text-sm font-semibold">
                {title(tool)}
              </h3>
            </div>
            <button
              type="button"
              onClick={() => setOpenTool(null)}
              aria-label={t('tools.close')}
              className="shrink-0 rounded-lg p-1 text-dim hover:bg-panel2 hover:text-fg"
            >
              <X size="1.0000rem" />
            </button>
          </div>
          <div className="min-h-0 flex-1 space-y-3 overflow-y-auto px-4 py-3">
            <p className="text-xs text-dim">
              {t(
                tool === 'image'
                  ? 'config.toolsImageHint'
                  : 'config.toolsVideoHint',
              )}
            </p>
            {instances.length === 0 ? (
              <div className="rounded-lg border border-dashed border-edge/70 px-3 py-6 text-center text-xs text-dim/80">
                {t('config.toolsNoInstances')}
              </div>
            ) : (
              instances.map((instance) => renderInstance(tool, instance))
            )}
          </div>
          <div className="flex shrink-0 items-center justify-between gap-3 border-t border-edge px-4 py-3">
            <span className="flex min-w-0 items-center gap-2 text-xs">
              {saved === tool && (
                <span className="inline-flex items-center gap-1 text-ok">
                  <Check size="0.8571rem" />
                  {t('config.toolsSaved')}
                </span>
              )}
              {error !== '' && (
                <span className="min-w-0 truncate text-err" title={error}>
                  {error}
                </span>
              )}
            </span>
            <button
              type="button"
              onClick={() => void save(tool)}
              disabled={saving !== null || instances.length === 0}
              className="flex shrink-0 items-center gap-1.5 rounded-lg bg-accent px-4 py-1.5 text-sm text-white hover:opacity-90 disabled:opacity-40"
            >
              {saving === tool && (
                <Loader2 size="1.0000rem" className="animate-spin" />
              )}
              {t('setup.saveApply')}
            </button>
          </div>
        </div>
      </div>
    );
  };

  if (state === null && error === '') {
    return (
      <div className="space-y-3">
        {[0, 1].map((key) => (
          <div
            key={key}
            className="h-16 animate-pulse rounded-xl border border-edge/70 bg-panel/70"
          />
        ))}
      </div>
    );
  }
  return (
    <div className="space-y-3">
      <p className="text-xs text-dim">{t('config.toolsHint')}</p>
      <ul className="flex flex-col gap-2">
        {TOOLS.map((tool) => renderItem(tool, state?.[tool]))}
      </ul>
      {error !== '' && openTool === null && (
        <p className="rounded-lg border border-err/40 bg-err/5 px-3 py-2 text-xs break-words text-err">
          {error}
        </p>
      )}
      {openTool !== null && renderDialog(openTool, state?.[openTool])}
    </div>
  );
}
