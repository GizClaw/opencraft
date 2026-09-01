import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  CalendarClock,
  Check,
  ChevronUp,
  History,
  Loader2,
  Play,
  Plus,
  Search,
  Settings2,
  Sparkles,
  Trash2,
  X,
} from 'lucide-react';
import { api } from '../lib/api';
import { useStore } from '../lib/store';
import type {
  AutomationRun,
  AutomationSchedule,
  AutomationTask,
  SessionMeta,
} from '../lib/types';

interface FormState {
  id: string;
  name: string;
  prompt: string;
  scheduleType: string;
  intervalHours: number;
  intervalWeeks: number;
  days: string[];
  time: string;
  workspace: string;
  mode: string;
  model: string;
  think: string;
  notify: string;
  sessionMode: 'new' | 'existing';
  sessionID: string;
  enabled: boolean;
}

const WEEKDAYS = ['MO', 'TU', 'WE', 'TH', 'FR', 'SA', 'SU'];

const emptyForm = (workspace: string): FormState => ({
  id: '',
  name: '',
  prompt: '',
  scheduleType: 'daily',
  intervalHours: 2,
  intervalWeeks: 1,
  days: ['MO', 'TU', 'WE', 'TH', 'FR'],
  time: '09:00',
  workspace,
  mode: 'workspace',
  model: '',
  think: '',
  notify: 'always',
  sessionMode: 'new',
  sessionID: '',
  enabled: true,
});

const taskToForm = (task: AutomationTask): FormState => ({
  id: task.id,
  name: task.name,
  prompt: task.prompt,
  scheduleType: task.schedule.type,
  intervalHours: task.schedule.interval_hours ?? 2,
  intervalWeeks: task.schedule.interval_weeks ?? 1,
  days: task.schedule.days ?? [],
  time: task.schedule.time ?? '09:00',
  workspace: task.workspace,
  mode: task.mode || 'workspace',
  model: task.model ?? '',
  think: task.think ?? '',
  notify: task.notify || 'always',
  sessionMode: task.conversation_id ? 'existing' : 'new',
  sessionID: task.conversation_id ?? '',
  enabled: task.enabled,
});

const formToTask = (f: FormState): AutomationTask => {
  const schedule: AutomationSchedule = { type: f.scheduleType };
  if (f.scheduleType === 'hourly') {
    schedule.interval_hours = f.intervalHours;
    if (f.days.length > 0) schedule.days = f.days;
  } else if (f.scheduleType === 'daily' || f.scheduleType === 'weekdays') {
    schedule.time = f.time;
  } else if (f.scheduleType === 'weekly') {
    schedule.interval_weeks = f.intervalWeeks;
    schedule.days = f.days;
    schedule.time = f.time;
  }
  return {
    id: f.id,
    name: f.name.trim(),
    prompt: f.prompt.trim(),
    schedule,
    workspace: f.workspace.trim(),
    mode: f.mode,
    model: f.model.trim(),
    think: f.think,
    notify: f.notify,
    conversation_id: f.sessionMode === 'existing' ? f.sessionID : '',
    enabled: f.enabled,
    created_at: '',
    updated_at: '',
    last_run_at: '',
    last_status: '',
    next_run_at: '',
  };
};

function scheduleLabel(s: AutomationSchedule): string {
  switch (s.type) {
    case 'hourly':
      return s.days && s.days.length > 0
        ? `${s.interval_hours}h · ${s.days.join(',')}`
        : `${s.interval_hours}h`;
    case 'daily':
      return `daily ${s.time}`;
    case 'weekdays':
      return `weekdays ${s.time}`;
    case 'weekly':
      return `weekly ${(s.days ?? []).join(',')} ${s.time}`;
    default:
      return s.type;
  }
}

function statusClass(status: string): string {
  switch (status) {
    case 'completed':
      return 'text-ok border border-ok/30 bg-ok/10';
    case 'failed':
      return 'text-err border border-err/40 bg-err/10';
    case 'running':
      return 'text-accent border border-accent/30 bg-accent/10';
    default:
      return 'text-dim border border-edge bg-panel';
  }
}

function fmtTime(iso: string): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString();
}

interface CapsuleOption {
  value: string;
  label: string;
}

// CapsuleSelect is the same pill-style picker used by the chat input
// box: a compact button that opens a dropdown option list.
function CapsuleSelect({
  value,
  options,
  onChange,
  menuClassName = 'w-48',
  buttonClassName,
}: {
  value: string;
  options: CapsuleOption[];
  onChange: (value: string) => void;
  menuClassName?: string;
  buttonClassName?: string;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onDown = (e: PointerEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener('pointerdown', onDown);
    return () => document.removeEventListener('pointerdown', onDown);
  }, [open]);
  const label = options.find((o) => o.value === value)?.label ?? value;
  return (
    <div className="relative" ref={ref}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className={
          buttonClassName ??
          'flex w-full items-center justify-between gap-1.5 rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm text-dim hover:text-fg'
        }
      >
        <span className="flex-1 min-w-0 truncate text-left">{label}</span>
        <ChevronUp size="0.7857rem" />
      </button>
      {open && (
        <div
          className={`absolute top-full left-0 z-40 mt-1.5 rounded-xl border border-edge bg-panel p-1.5 shadow-xl ${menuClassName}`}
        >
          {options.map((o) => (
            <button
              key={o.value}
              type="button"
              onClick={() => {
                setOpen(false);
                onChange(o.value);
              }}
              className={`flex w-full items-center justify-between rounded-md px-2 py-1.5 text-left text-xs ${
                value === o.value
                  ? 'bg-accent/10 text-accent'
                  : 'text-dim hover:bg-panel2 hover:text-fg'
              }`}
            >
              <span className="truncate" title={o.label}>
                {o.label}
              </span>
              {value === o.value && <Check size="0.8571rem" />}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

// Field is the settings-page field pattern: a small label above the
// control.
function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <label className="block space-y-1.5">
      <span className="text-xs text-dim">{label}</span>
      {children}
    </label>
  );
}

// SectionCard is the settings-page card pattern: a rounded panel2 card
// with an icon + title header.
function SectionCard({
  title,
  icon,
  children,
}: {
  title: string;
  icon?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div className="rounded-xl border border-edge bg-panel2 p-4 space-y-3">
      <div className="flex items-center gap-2 text-sm font-medium">
        {icon && <span className="text-accent">{icon}</span>}
        {title}
      </div>
      {children}
    </div>
  );
}

export function AutomationsView() {
  const { t } = useTranslation();
  const workspace = useStore((s) => s.workspace);
  const workspaces = useStore((s) => s.workspaces);
  const automations = useStore((s) => s.automations);
  const runs = useStore((s) => s.automationRuns);
  const modelOptions = useStore((s) => s.modelOptions);
  const loadAutomations = useStore((s) => s.loadAutomations);
  const loadAutomationRuns = useStore((s) => s.loadAutomationRuns);
  const resume = useStore((s) => s.resume);
  const closeTools = useStore((s) => s.closeTools);
  const toast = useStore((s) => s.toast);
  const flash = useStore((s) => s.flash);

  const [form, setForm] = useState<FormState | null>(null);
  const [historyFor, setHistoryFor] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [runningId, setRunningId] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [modelMenuOpen, setModelMenuOpen] = useState(false);
  const modelMenuRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!modelMenuOpen) return;
    const onDown = (e: PointerEvent) => {
      if (
        modelMenuRef.current &&
        !modelMenuRef.current.contains(e.target as Node)
      ) {
        setModelMenuOpen(false);
      }
    };
    document.addEventListener('pointerdown', onDown);
    return () => document.removeEventListener('pointerdown', onDown);
  }, [modelMenuOpen]);
  const [query, setQuery] = useState('');
  const [filter, setFilter] = useState<
    'all' | 'active' | 'paused' | 'completed'
  >('all');
  const [sessionOptions, setSessionOptions] = useState<SessionMeta[]>([]);

  const thinkLevels = [
    { value: 'low', label: t('automations.thinkLow') },
    { value: 'medium', label: t('automations.thinkMedium') },
    { value: 'high', label: t('automations.thinkHigh') },
  ];
  const thinkValue = form?.think ?? '';
  const thinkIndex = Math.max(
    0,
    thinkLevels.findIndex((l) => l.value === thinkValue),
  );
  const thinkLabel = thinkValue
    ? thinkLevels[thinkIndex].label
    : t('automations.thinkDefault');
  const modelLabel = form?.model
    ? (modelOptions.find((o) => o.id === form.model)?.label ?? form.model)
    : t('chat.modelAuto');
  const workspaceOptions = (current: string): CapsuleOption[] => {
    const list = workspaces.map((w) => ({
      value: w.path,
      label: `${w.title} · ${w.path}`,
    }));
    if (current && !list.some((o) => o.value === current)) {
      const name = current.split(/[\\/]/).filter(Boolean).pop() ?? current;
      list.unshift({ value: current, label: `${name} · ${current}` });
    }
    return list;
  };

  useEffect(() => {
    void loadAutomations();
  }, [loadAutomations]);

  useEffect(() => {
    if (!form?.workspace) {
      setSessionOptions([]);
      return;
    }
    let alive = true;
    void api
      .automationSessions(form.workspace)
      .then((list) => {
        if (alive) setSessionOptions(list ?? []);
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, [form?.workspace]);

  const openNew = () => {
    setError('');
    setForm(emptyForm(workspace));
    setHistoryFor(null);
  };

  const openEdit = (task: AutomationTask) => {
    setError('');
    setForm(taskToForm(task));
    setHistoryFor(task.id);
    void loadAutomationRuns(task.id);
  };

  const save = async () => {
    if (!form) return;
    setSaving(true);
    setError('');
    try {
      const saved = await api.saveAutomation(formToTask(form));
      toast(saved.id ? t('automations.saved') : t('automations.created'));
      setForm(null);
      void loadAutomations();
    } catch (err) {
      setError(String(err));
    } finally {
      setSaving(false);
    }
  };

  const runNow = async (task: AutomationTask) => {
    setRunningId(task.id);
    try {
      await api.runAutomationNow(task.id);
      toast(t('automations.queued'));
    } catch (err) {
      flash(String(err));
    } finally {
      setRunningId(null);
    }
  };

  const remove = async (id: string) => {
    try {
      await api.deleteAutomation(id);
      setConfirmDelete(null);
      setForm(null);
      setHistoryFor(null);
      void loadAutomations();
    } catch (err) {
      flash(String(err));
    }
  };

  const openRunSession = async (run: AutomationRun) => {
    if (!run.conversation_id) return;
    await resume(run.conversation_id);
    closeTools();
  };

  const toggleDay = (d: string) => {
    if (!form) return;
    setForm({
      ...form,
      days: form.days.includes(d)
        ? form.days.filter((x) => x !== d)
        : [...form.days, d],
    });
  };

  const historyRuns = historyFor ? (runs[historyFor] ?? []) : [];
  const runningRun = form?.id
    ? (runs[form.id] ?? []).find((r) => r.status === 'running')
    : undefined;

  const filtered = automations.filter((task) => {
    if (filter === 'active' && !task.enabled) return false;
    if (filter === 'paused' && task.enabled) return false;
    if (filter === 'completed' && task.last_status !== 'completed')
      return false;
    const q = query.trim().toLowerCase();
    if (!q) return true;
    return (
      task.name.toLowerCase().includes(q) ||
      task.prompt.toLowerCase().includes(q) ||
      task.workspace.toLowerCase().includes(q)
    );
  });

  const timeUntil = (iso: string): string => {
    if (!iso) return '—';
    const d = new Date(iso).getTime();
    if (Number.isNaN(d)) return '—';
    const diff = d - Date.now();
    if (diff <= 0) return t('automations.due');
    const mins = Math.floor(diff / 60000);
    if (mins < 60) return t('automations.inMinutes', { n: mins });
    const hours = Math.floor(mins / 60);
    if (hours < 24) return t('automations.inHours', { n: hours });
    const days = Math.floor(hours / 24);
    return t('automations.inDays', { n: days });
  };

  const filters: { value: 'all' | 'active' | 'paused' | 'completed'; label: string }[] = [
    { value: 'all', label: t('automations.filterAll') },
    { value: 'active', label: t('automations.filterActive') },
    { value: 'paused', label: t('automations.filterPaused') },
    { value: 'completed', label: t('automations.filterCompleted') },
  ];

  return (
    <div className="space-y-3">
      <div className="flex items-start justify-between gap-3">
        <p className="text-xs text-dim">{t('automations.hint')}</p>
        <button
          onClick={openNew}
          className="flex items-center gap-1.5 rounded-lg bg-accent px-3 py-1.5 text-sm text-white hover:opacity-90"
        >
          <Plus size="1.0000rem" />
          {t('automations.new')}
        </button>
      </div>

      {error && (
        <div className="rounded-lg border border-err/40 bg-err/10 px-3 py-2 text-sm text-err">
          {error}
        </div>
      )}

      <div className="relative">
        <Search
          size="0.8571rem"
          className="absolute left-2.5 top-1/2 -translate-y-1/2 text-dim"
        />
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t('automations.search')}
          className="w-full rounded-lg border border-edge bg-panel pl-8 pr-3 py-1.5 text-sm outline-none focus:border-accent"
        />
      </div>

      <div className="flex flex-wrap gap-1.5">
        {filters.map((f) => (
          <button
            key={f.value}
            onClick={() => setFilter(f.value)}
            className={`rounded-lg px-3 py-1 text-xs border ${
              filter === f.value
                ? 'border-accent/50 bg-accent/15 text-accent'
                : 'border-edge bg-panel text-dim hover:text-fg'
            }`}
          >
            {f.label}
          </button>
        ))}
      </div>

      {filtered.length === 0 ? (
        <div className="rounded-xl border border-edge bg-panel2 p-6 text-center text-sm text-dim">
          {t('automations.empty')}
        </div>
      ) : (
        <div className="space-y-2">
          {filtered.map((task) => (
            <button
              key={task.id}
              onClick={() => openEdit(task)}
              className="w-full rounded-xl border border-edge bg-panel2 p-3 text-left hover:border-accent/40 transition-colors space-y-1.5"
            >
              <div className="flex items-center gap-2">
                <span className="text-sm font-semibold min-w-0 truncate">
                  {task.name}
                </span>
                <span className="flex-1" />
                {!task.enabled && (
                  <span className="rounded border border-edge px-1.5 py-0.5 text-[0.7143rem] text-dim">
                    {t('automations.paused')}
                  </span>
                )}
                {task.last_status && (
                  <span
                    className={`rounded px-1.5 py-0.5 text-[0.7143rem] ${statusClass(
                      task.last_status,
                    )}`}
                  >
                    {task.last_status}
                  </span>
                )}
              </div>
              <div className="flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs text-dim">
                <span>
                  {t('automations.next')}: {fmtTime(task.next_run_at)}
                </span>
                <span className="text-accent">
                  {timeUntil(task.next_run_at)}
                </span>
              </div>
            </button>
          ))}
        </div>
      )}

      {form && (
        <>
          <div
            className="fixed inset-0 z-40 bg-black/30"
            onClick={() => setForm(null)}
          />
          <aside className="fixed inset-y-0 right-0 z-50 w-[26rem] max-w-[92vw] overflow-y-auto border-l border-edge bg-panel p-4 shadow-2xl space-y-3">
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-semibold">
              {form.id ? t('automations.edit') : t('automations.new')}
            </h3>
            <div className="flex items-center gap-3">
              <label className="flex items-center gap-1.5 text-xs text-dim">
                <input
                  type="checkbox"
                  checked={form.enabled}
                  onChange={(e) =>
                    setForm({ ...form, enabled: e.target.checked })
                  }
                  className="accent-accent"
                />
                {form.enabled
                  ? t('automations.enabled')
                  : t('automations.paused')}
              </label>
              <button
                onClick={() => setForm(null)}
                className="text-dim hover:text-fg"
                title={t('tools.close')}
              >
                <X size="1.1428rem" />
              </button>
            </div>
          </div>

          <Field label={t('automations.name')}>
            <input
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
              placeholder={t('automations.namePlaceholder')}
              className="w-full rounded-lg border border-edge bg-panel2 px-3 py-1.5 text-sm outline-none focus:border-accent"
            />
          </Field>

          <Field label={t('automations.prompt')}>
            <textarea
              value={form.prompt}
              onChange={(e) => setForm({ ...form, prompt: e.target.value })}
              placeholder={t('automations.promptPlaceholder')}
              rows={4}
              className="w-full rounded-lg border border-edge bg-panel2 px-3 py-1.5 text-sm outline-none focus:border-accent resize-y"
            />
          </Field>

          <SectionCard
            title={t('automations.details')}
            icon={<Settings2 size="1.0714rem" />}
          >
            <Field label={t('automations.workspace')}>
              <CapsuleSelect
                value={form.workspace}
                options={workspaceOptions(form.workspace)}
                onChange={(v) => setForm({ ...form, workspace: v })}
                menuClassName="w-80"
                buttonClassName="flex w-full items-center justify-between gap-1.5 rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm text-dim hover:text-fg"
              />
            </Field>
            <Field label={t('automations.sessionMode')}>
              <CapsuleSelect
                value={form.sessionMode}
                options={[
                  { value: 'new', label: t('automations.newSession') },
                  { value: 'existing', label: t('automations.existingSession') },
                ]}
                onChange={(v) =>
                  setForm({
                    ...form,
                    sessionMode: v === 'existing' ? 'existing' : 'new',
                  })
                }
              />
            </Field>
            {form.sessionMode === 'existing' && (
              <Field label={t('automations.sessionField')}>
                <CapsuleSelect
                  value={form.sessionID}
                  options={sessionOptions.map((s) => ({
                    value: s.id,
                    label: `${s.title || s.id} · ${s.id}`,
                  }))}
                  onChange={(v) => setForm({ ...form, sessionID: v })}
                  menuClassName="w-72"
                  buttonClassName="flex w-full items-center justify-between gap-1.5 rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm text-dim hover:text-fg"
                />
                {sessionOptions.length === 0 && (
                  <p className="text-xs text-dim">
                    {t('automations.noSessions')}
                  </p>
                )}
              </Field>
            )}
            <div className="grid grid-cols-2 gap-3">
              {form.sessionMode === 'new' && (
                <Field label={t('automations.permission')}>
                  <CapsuleSelect
                    value={form.mode}
                    options={[
                      {
                        value: 'workspace',
                        label: t('automations.modeWorkspace'),
                      },
                      {
                        value: 'read-only',
                        label: t('automations.modeReadonly'),
                      },
                      { value: 'yolo', label: t('automations.modeYolo') },
                    ]}
                    onChange={(v) => setForm({ ...form, mode: v })}
                  />
                </Field>
              )}
              <Field label={t('automations.notification')}>
                <CapsuleSelect
                  value={form.notify}
                  options={[
                    {
                      value: 'always',
                      label: t('automations.notifyAlways'),
                    },
                    {
                      value: 'failed',
                      label: t('automations.notifyFailed'),
                    },
                    { value: 'never', label: t('automations.notifyNever') },
                  ]}
                  onChange={(v) => setForm({ ...form, notify: v })}
                />
              </Field>
            </div>
            {form.sessionMode === 'new' && (
              <>
                <Field label={t('automations.modelField')}>
                  <div className="relative" ref={modelMenuRef}>
                <button
                  type="button"
                  onClick={() => setModelMenuOpen((v) => !v)}
                  title={t('chat.modelLabel')}
                  className="flex w-full items-center gap-1.5 rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm text-dim hover:text-fg"
                >
                  <Sparkles size="0.8571rem" className="text-accent" />
                  <span className="flex-1 truncate text-left">{modelLabel}</span>
                  <span className="text-edge">·</span>
                  <span>{thinkLabel}</span>
                  <ChevronUp size="0.7857rem" />
                </button>
                {modelMenuOpen && (
                  <div className="absolute top-full left-0 z-40 mt-1.5 w-full rounded-xl border border-edge bg-panel p-1.5 shadow-xl">
                      <div className="px-2 pb-1 pt-1.5 text-[0.7143rem] uppercase tracking-wider text-dim">
                        {t('chat.modelLabel')}
                      </div>
                      <div className="max-h-52 overflow-y-auto">
                        <button
                          type="button"
                          onClick={() => {
                            setModelMenuOpen(false);
                            setForm({ ...form, model: '' });
                          }}
                          className={`flex w-full items-center justify-between rounded-md px-2 py-1.5 text-left text-xs ${
                            !form.model
                              ? 'bg-accent/10 text-accent'
                              : 'text-dim hover:bg-panel2 hover:text-fg'
                          }`}
                        >
                          <span>{t('chat.modelAuto')}</span>
                          {!form.model && <Check size="0.8571rem" />}
                        </button>
                        {modelOptions.map((m) => (
                          <button
                            key={m.id}
                            type="button"
                            onClick={() => {
                              setModelMenuOpen(false);
                              setForm({ ...form, model: m.id });
                            }}
                            className={`flex w-full items-center justify-between rounded-md px-2 py-1.5 text-left text-xs ${
                              form.model === m.id
                                ? 'bg-accent/10 text-accent'
                                : 'text-dim hover:bg-panel2 hover:text-fg'
                            }`}
                          >
                            <span className="truncate">{m.label}</span>
                            {form.model === m.id && (
                              <Check size="0.8571rem" />
                            )}
                          </button>
                        ))}
                      </div>
                      <div className="my-1 border-t border-edge" />
                      <div className="flex items-center justify-between px-2 pt-1.5 text-xs">
                        <span className="text-dim">
                          {t('chat.thinkLabel')}
                        </span>
                        <span className="text-fg">{thinkLabel}</span>
                      </div>
                      <div className="px-2 pt-1.5">
                        <input
                          type="range"
                          min={0}
                          max={2}
                          step={1}
                          value={thinkIndex}
                          onChange={(e) => {
                            const v = Number(e.target.value);
                            setForm({
                              ...form,
                              think: thinkLevels[v]?.value ?? 'medium',
                            });
                          }}
                          className="w-full accent-accent"
                        />
                      </div>
                      <div className="flex justify-between px-2 pb-1.5 text-[0.7143rem] text-dim">
                        <span>{t('chat.thinkLow')}</span>
                        <span>{t('chat.thinkMedium')}</span>
                        <span>{t('chat.thinkHigh')}</span>
                      </div>
                  </div>
                )}
                  </div>
                </Field>
                {form.mode === 'yolo' && (
                  <p className="text-xs text-err">
                    {t('automations.yoloWarning')}
                  </p>
                )}
              </>
            )}
          </SectionCard>

          <SectionCard
            title={t('automations.timeCard')}
            icon={<CalendarClock size="1.0714rem" />}
          >
            <Field label={t('automations.scheduleField')}>
              <CapsuleSelect
                value={form.scheduleType}
                options={[
                  { value: 'hourly', label: t('automations.hourly') },
                  { value: 'daily', label: t('automations.daily') },
                  { value: 'weekdays', label: t('automations.weekdays') },
                  { value: 'weekly', label: t('automations.weekly') },
                ]}
                onChange={(v) => setForm({ ...form, scheduleType: v })}
              />
            </Field>
            <div className="grid grid-cols-2 gap-3">
              {form.scheduleType === 'hourly' && (
                <Field label={t('automations.intervalField')}>
                  <div className="flex items-center gap-1.5">
                    <input
                      type="number"
                      min={1}
                      value={form.intervalHours}
                      onChange={(e) =>
                        setForm({
                          ...form,
                          intervalHours: Number(e.target.value) || 1,
                        })
                      }
                      className="w-full rounded-lg border border-edge bg-panel px-2 py-1.5 text-sm outline-none focus:border-accent"
                    />
                    <span className="text-sm text-dim">h</span>
                  </div>
                </Field>
              )}
              {(form.scheduleType === 'daily' ||
                form.scheduleType === 'weekdays' ||
                form.scheduleType === 'weekly') && (
                <Field label={t('automations.timeField')}>
                  <input
                    type="time"
                    value={form.time}
                    onChange={(e) =>
                      setForm({ ...form, time: e.target.value })
                    }
                    className="w-full rounded-lg border border-edge bg-panel px-2 py-1.5 text-sm outline-none focus:border-accent"
                  />
                </Field>
              )}
              {form.scheduleType === 'weekly' && (
                <Field label={t('automations.weeksField')}>
                  <input
                    type="number"
                    min={1}
                    value={form.intervalWeeks}
                    onChange={(e) =>
                      setForm({
                        ...form,
                        intervalWeeks: Number(e.target.value) || 1,
                      })
                    }
                    className="w-full rounded-lg border border-edge bg-panel px-2 py-1.5 text-sm outline-none focus:border-accent"
                  />
                </Field>
              )}
            </div>
            {(form.scheduleType === 'hourly' ||
              form.scheduleType === 'weekly') && (
              <Field label={t('automations.weekdaysField')}>
                <div className="flex flex-wrap gap-1.5">
                  {WEEKDAYS.map((d) => (
                    <button
                      key={d}
                      onClick={() => toggleDay(d)}
                    className={`rounded-full px-2.5 py-1 text-xs border ${
                      form.days.includes(d)
                        ? 'border-accent/50 bg-accent/15 text-accent'
                          : 'border-edge bg-panel text-dim hover:text-fg'
                      }`}
                    >
                      {d}
                    </button>
                  ))}
                </div>
              </Field>
            )}
          </SectionCard>

          <div className="flex items-center gap-2">
            {form.id && (
              <button
                onClick={() => void runNow(formToTask(form))}
                disabled={
                  runningId === form.id || !form.enabled || !!runningRun
                }
                className="flex items-center gap-1 rounded-lg border border-edge px-2.5 py-1.5 text-sm text-dim hover:text-fg disabled:opacity-40"
                title={t('automations.runNow')}
              >
                {runningId === form.id || runningRun ? (
                  <Loader2 size="0.8571rem" className="animate-spin" />
                ) : (
                  <Play size="0.8571rem" />
                )}
                {t('automations.runNow')}
              </button>
            )}
            <button
              onClick={() => void save()}
              disabled={saving}
              className="flex items-center gap-1.5 rounded-lg bg-accent px-4 py-1.5 text-sm text-white hover:opacity-90 disabled:opacity-50"
            >
              {saving && <Loader2 size="1.0000rem" className="animate-spin" />}
              {t('automations.save')}
            </button>
            <button
              onClick={() => setForm(null)}
              className="rounded-lg border border-edge px-3 py-1.5 text-sm text-dim hover:text-fg"
            >
              {t('interact.cancel')}
            </button>
            <span className="flex-1" />
            {form.id && !confirmDelete && (
              <button
                onClick={() => setConfirmDelete(form.id)}
                className="flex items-center gap-1 rounded-lg border border-edge px-2.5 py-1.5 text-sm text-dim hover:text-err"
              >
                <Trash2 size="0.8571rem" />
                {t('automations.delete')}
              </button>
            )}
          </div>

          {confirmDelete && form.id && (
            <div className="flex items-center gap-2 text-xs text-dim">
              <span>
                {t('automations.deleteConfirm', { name: form.name })}
              </span>
              <button
                onClick={() => void remove(form.id)}
                className="rounded bg-err px-2 py-1 text-white"
              >
                {t('automations.delete')}
              </button>
              <button
                onClick={() => setConfirmDelete(null)}
                className="rounded border border-edge px-2 py-1"
              >
                {t('interact.cancel')}
              </button>
            </div>
          )}

          {form.id && (
            <div className="space-y-2 border-t border-edge pt-3">
              <div className="flex items-center gap-2">
                <h4 className="text-sm font-semibold">
                  {t('automations.history')}
                </h4>
                <History size="0.8571rem" className="text-dim" />
              </div>
              {historyRuns.length === 0 ? (
                <p className="text-xs text-dim">{t('automations.noRuns')}</p>
              ) : (
                historyRuns.map((run) => (
                  <div
                    key={run.id}
                    className="rounded-lg border border-edge bg-panel p-2 space-y-1"
                  >
                    <div className="flex items-center gap-2 text-xs">
                      <span
                        className={`rounded px-1.5 py-0.5 ${statusClass(
                          run.status,
                        )}`}
                      >
                        {run.status}
                      </span>
                      <span className="text-dim">{fmtTime(run.at)}</span>
                    </div>
                    {run.duration_ms > 0 && (
                      <p className="text-[0.7143rem] text-dim">
                        {t('automations.duration')}:{' '}
                        {(run.duration_ms / 1000).toFixed(1)}s
                      </p>
                    )}
                    {run.error && (
                      <p className="text-[0.7143rem] text-err min-w-0 truncate">
                        {run.error}
                      </p>
                    )}
                    {run.conversation_id && (
                      <button
                        onClick={() => void openRunSession(run)}
                        className="text-xs text-accent hover:underline"
                      >
                        {t('automations.openSession')}
                      </button>
                    )}
                  </div>
                ))
              )}
            </div>
          )}
          </aside>
        </>
      )}

    </div>
  );
}
