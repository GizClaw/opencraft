import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Check,
  ChevronDown,
  ChevronRight,
  Film,
  Image as ImageIcon,
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
import { ICON } from './ui/icon';
import { Modal } from './ui/Modal';
import { NumberField } from './ui/NumberField';
import { Popover } from './ui/Popover';
import { SaveBar } from './ui/SaveBar';

// The generation tools section of the Tools tab: one item per tool, the
// way the MCP list works. Clicking an item opens a dialog with the
// provider-specific settings that deployment configured; the
// model-facing tools keep only the common parameters.

type ToolValues = Record<string, Record<string, unknown>>;
type ToolKey = 'image' | 'video';

const controlClass =
  'w-full rounded-control border border-edge bg-panel px-2 py-1 text-xs text-fg ' +
  'outline-none transition-colors hover:border-accent/50 focus:border-accent ' +
  'disabled:opacity-40';

const fieldClass =
  'w-full rounded-control border border-edge bg-panel px-2.5 py-1.5 text-xs text-fg ' +
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
  const [anchor, setAnchor] = useState<HTMLElement | null>(null);
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const selected = options.find((option) => option.value === value);

  const toggle = () => {
    setAnchor(triggerRef.current);
    setOpen((v) => !v);
  };

  return (
    <>
      <button
        ref={triggerRef}
        type="button"
        aria-label={label}
        aria-haspopup="listbox"
        aria-expanded={open}
        onClick={toggle}
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
            size={ICON.xs}
            className={`shrink-0 text-dim transition-transform ${
              open ? 'rotate-180' : ''
            }`}
          />
        </span>
      </button>
      <Popover
        open={open}
        onClose={() => setOpen(false)}
        anchor={anchor}
        ariaLabel={label}
        align="end"
        keyboard
        maxHeight={288}
        panelClassName="min-w-full rounded-control border border-edge bg-panel p-1 shadow-popover"
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
              className={`flex w-full items-center gap-2 rounded-control px-2.5 py-1.5 text-left text-xs transition-colors ${
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
              {isSelected && <Check size={ICON.xs} className="shrink-0" />}
            </button>
          );
        })}
      </Popover>
    </>
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
      // The documented default is on the row's own line, so the
      // placeholder stays short enough to read inside the control.
      control =
        field.kind === 'string' ? (
          <input
            aria-label={label}
            placeholder={providerDefault}
            value={value === undefined ? '' : String(value)}
            onChange={(e) => {
              const raw = e.target.value.trim();
              setField(
                tool,
                instance.id,
                field.name,
                raw === '' ? undefined : raw,
              );
            }}
            className={fieldClass}
          />
        ) : (
          // An emptied number goes back to the provider's default, which
          // is what the placeholder has said all along.
          <NumberField
            label={label}
            allowEmpty
            placeholder={providerDefault}
            value={typeof value === 'number' ? value : ''}
            min={field.min ?? undefined}
            max={field.max ?? undefined}
            step={field.kind === 'float' ? 'any' : undefined}
            onChange={(next) =>
              setField(
                tool,
                instance.id,
                field.name,
                next === '' ? undefined : next,
              )
            }
            className="w-full"
          />
        );
    }
    return (
      <div
        key={field.name}
        className="flex items-start gap-4 px-3 py-2 hover:bg-panel2"
      >
        <div className="min-w-0 flex-1">
          <span className="block truncate text-xs text-fg">{label}</span>
          {/* The name, the provider's vocabulary and the documented
              default wrap instead of truncating: a cut line hides
              exactly the value the row exists to name. */}
          <span className="block break-words font-mono text-micro text-dim">
            {field.name}
            {hint && <span className="ml-1.5 text-faint">· {hint}</span>}
            {defaultHint && (
              <span className="ml-1.5 text-faint">· {defaultHint}</span>
            )}
          </span>
          {description !== '' && (
            <span className="mt-0.5 block text-micro leading-snug text-dim">
              {description}
            </span>
          )}
        </div>
        <div className="w-52 shrink-0 self-center">{control}</div>
      </div>
    );
  };

  const renderInstance = (tool: ToolKey, instance: ToolOptionInstanceView) => (
    <div
      key={instance.id}
      className="overflow-hidden rounded-control border border-edge/70 bg-panel2"
    >
      <div className="flex items-center gap-2 border-b border-edge/60 px-3 py-2">
        <span className="truncate text-xs font-medium text-fg">
          {instance.label}
        </span>
        {instance.managed && (
          <span className="shrink-0 rounded-full border border-edge px-1.5 py-0.5 text-micro text-dim">
            {t('config.toolsManaged')}
          </span>
        )}
        <span className="flex-1" />
        <code className="shrink-0 font-mono text-micro text-dim">
          {instance.id}
        </code>
      </div>
      {(instance.presets ?? []).length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5 border-b border-edge/60 px-3 py-2">
          <span className="text-micro text-dim">
            {t('config.toolsPresets')}
          </span>
          {(instance.presets ?? []).map((preset) => (
            <button
              key={preset.id}
              type="button"
              onClick={() => applyPreset(tool, instance.id, preset)}
              className="rounded-full border border-edge bg-panel px-2 py-0.5 text-micro text-dim transition-colors hover:border-accent/50 hover:text-fg"
            >
              {t(`config.toolPreset.${preset.id}`, { defaultValue: preset.id })}
            </button>
          ))}
        </div>
      )}
      {(instance.fields ?? []).length === 0 ? (
        <p className="px-3 py-2.5 text-xs text-dim">
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
        className="[content-visibility:auto] [contain-intrinsic-size:auto_4.5rem] rounded-card border border-edge bg-panel2 p-3 transition-colors hover:border-accent/40"
      >
        <button
          type="button"
          onClick={() => {
            setSaved(null);
            setOpenTool(tool);
          }}
          className="flex w-full min-w-0 items-center gap-2 text-left"
        >
          <Icon size={ICON.sm} className="shrink-0 text-accent" />
          <span className="min-w-0 flex-1">
            <span className="flex flex-wrap items-center gap-1.5">
              <span className="min-w-0 truncate text-sm font-semibold">
                {title(tool)}
              </span>
              {configured > 0 && (
                <span className="shrink-0 rounded-tight border border-edge bg-panel px-1.5 py-0.5 text-micro text-dim">
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
          <ChevronRight size={ICON.sm} className="shrink-0 text-dim" />
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
    // The dialog opens from inside the settings panel, and that panel
    // clips its own overflow: a provider with a long vocabulary would
    // lose its header and save bar behind the clip. Portal it out, and
    // give the control column enough room for the provider-default label.
    return (
      <Modal
        open
        onClose={() => setOpenTool(null)}
        title={title(tool)}
        icon={Icon}
        width="42rem"
        portal
        footer={
          <SaveBar
            saved={saved === tool}
            error={error}
            saving={saving === tool}
            disabled={saving !== null || instances.length === 0}
            onSave={() => void save(tool)}
          />
        }
      >
        <p className="text-xs text-dim">
          {t(
            tool === 'image'
              ? 'config.toolsImageHint'
              : 'config.toolsVideoHint',
          )}
        </p>
        {instances.length === 0 ? (
          <div className="rounded-control border border-dashed border-edge/70 px-3 py-6 text-center text-xs text-dim">
            {t('config.toolsNoInstances')}
          </div>
        ) : (
          instances.map((instance) => renderInstance(tool, instance))
        )}
      </Modal>
    );
  };

  if (state === null && error === '') {
    return (
      <div className="space-y-3">
        {[0, 1].map((key) => (
          <div
            key={key}
            className="h-16 animate-pulse rounded-card border border-edge/70 bg-panel"
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
        <p className="rounded-control border border-err/40 bg-err/5 px-3 py-2 text-xs break-words text-err">
          {error}
        </p>
      )}
      {openTool !== null && renderDialog(openTool, state?.[openTool])}
    </div>
  );
}
