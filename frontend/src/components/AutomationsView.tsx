import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  CalendarClock,
  Check,
  ChevronDown,
  ChevronUp,
  History,
  Loader2,
  MoreHorizontal,
  Pause,
  Pencil,
  Play,
  Plus,
  Search,
  Settings2,
  Sparkles,
  Trash2,
  X,
  Zap,
} from 'lucide-react';
import { api } from '../lib/api';
import { useStore } from '../lib/store';
import type {
  AutomationRun,
  AutomationSchedule,
  AutomationTask,
  SessionMeta,
} from '../lib/types';
import { ICON } from './ui/icon';
import { Button } from './ui/Button';
import { ConfirmDialog } from './ui/ConfirmDialog';
import { Overlay } from './ui/Overlay';
import { Popover } from './ui/Popover';

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

const emptyForm = (workspace: string, yoloOnly: boolean): FormState => ({
  id: '',
  name: '',
  prompt: '',
  scheduleType: 'daily',
  intervalHours: 2,
  intervalWeeks: 1,
  days: ['MO', 'TU', 'WE', 'TH', 'FR'],
  time: '09:00',
  workspace,
  mode: yoloOnly ? 'yolo' : 'workspace',
  model: '',
  think: '',
  notify: 'always',
  sessionMode: 'new',
  sessionID: '',
  enabled: true,
});

const taskToForm = (task: AutomationTask, yoloOnly: boolean): FormState => ({
  id: task.id,
  name: task.name,
  prompt: task.prompt,
  scheduleType: task.schedule.type,
  intervalHours: task.schedule.interval_hours ?? 2,
  intervalWeeks: task.schedule.interval_weeks ?? 1,
  days: task.schedule.days ?? [],
  time: task.schedule.time ?? '09:00',
  workspace: task.workspace,
  mode: yoloOnly ? 'yolo' : task.mode || 'workspace',
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

function runErrorMessage(error: string, t: (key: string) => string): string {
  if (error === 'interrupted_by_app_restart' || error === '应用重启中断') {
    return t('automations.interruptedRestart');
  }
  return error;
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
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const label = options.find((o) => o.value === value)?.label ?? value;
  return (
    <div className="relative">
      <button
        type="button"
        ref={triggerRef}
        aria-haspopup="listbox"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className={
          buttonClassName ??
          'flex w-full items-center justify-between gap-1.5 rounded-control border border-edge bg-panel px-3 py-1.5 text-sm text-dim hover:text-fg'
        }
      >
        <span className="flex-1 min-w-0 truncate text-left">{label}</span>
        <ChevronUp size={ICON.xs} />
      </button>
      <Popover
        open={open}
        onClose={() => setOpen(false)}
        anchor={triggerRef.current}
        role="listbox"
        ariaLabel={label}
        matchWidth
        keyboard
        panelClassName={`rounded-card border border-edge bg-panel p-1.5 shadow-popover ${menuClassName}`}
      >
        {options.map((o) => (
          <button
            key={o.value}
            type="button"
            role="option"
            aria-selected={value === o.value}
            onClick={() => {
              setOpen(false);
              onChange(o.value);
            }}
            className={`flex w-full items-center justify-between rounded-control px-2 py-1.5 text-left text-xs ${
              value === o.value
                ? 'bg-accent/10 text-accent'
                : 'text-dim hover:bg-panel2 hover:text-fg'
            }`}
          >
            <span className="truncate" data-tip={o.label}>
              {o.label}
            </span>
            {value === o.value && <Check size={ICON.xs} />}
          </button>
        ))}
      </Popover>
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

// SectionCard is the settings-page card pattern: a rounded-tight panel2 card
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
    <div className="rounded-card border border-edge bg-panel2 p-4 space-y-3">
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
  const yoloOnly = useStore((s) => s.yoloOnly);
  const loadAutomations = useStore((s) => s.loadAutomations);
  const loadAutomationRuns = useStore((s) => s.loadAutomationRuns);
  const openSessionInWorkspace = useStore((s) => s.openSessionInWorkspace);
  const closeTools = useStore((s) => s.closeTools);
  const newChat = useStore((s) => s.newChat);
  const draftComposer = useStore((s) => s.draftComposer);
  const toast = useStore((s) => s.toast);
  const flash = useStore((s) => s.flash);

  const [form, setForm] = useState<FormState | null>(null);
  const [historyFor, setHistoryFor] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [runningId, setRunningId] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  // The three menus on this page — the model catalog, a row's actions and
  // the split "new" button — are anchored popovers, and the layer stack
  // owns Escape and the outside click. The two single-instance menus hold
  // their trigger in a ref; the row menu holds it in state, because every
  // row renders its own trigger and a shared ref would anchor the panel
  // to whichever row happened to render last.
  const [modelMenuOpen, setModelMenuOpen] = useState(false);
  const modelTriggerRef = useRef<HTMLButtonElement | null>(null);
  const [query, setQuery] = useState('');
  const [rowMenu, setRowMenu] = useState<{
    id: string;
    anchor: HTMLElement;
  } | null>(null);
  const [createMenuOpen, setCreateMenuOpen] = useState(false);
  const createTriggerRef = useRef<HTMLButtonElement | null>(null);
  const [filter, setFilter] = useState<
    'all' | 'active' | 'paused' | 'completed'
  >('all');
  const [sessionOptions, setSessionOptions] = useState<SessionMeta[]>([]);

  const thinkLevels = [
    { value: '', label: t('automations.thinkDefault') },
    { value: 'minimal', label: t('automations.thinkMinimal') },
    { value: 'low', label: t('automations.thinkLow') },
    { value: 'medium', label: t('automations.thinkMedium') },
    { value: 'high', label: t('automations.thinkHigh') },
    { value: 'xhigh', label: t('automations.thinkXHigh') },
  ];
  const thinkValue = form?.think ?? '';
  const thinkIndex = Math.max(
    0,
    thinkLevels.findIndex((l) => l.value === thinkValue),
  );
  const thinkLabel = thinkLevels[thinkIndex].label;
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
    setForm(emptyForm(workspace, yoloOnly));
    setHistoryFor(null);
  };

  const openAICreate = async () => {
    setCreateMenuOpen(false);
    await newChat();
    closeTools();
    draftComposer(
      "Let's set up a scheduled task together. First, explain how scheduled tasks work in OpenCraft. Then interview me to figure out what I need scheduled and when it should run.",
    );
  };

  const openEdit = (task: AutomationTask) => {
    setError('');
    setForm(taskToForm(task, yoloOnly));
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

  const toggleEnabled = async (task: AutomationTask) => {
    try {
      await api.saveAutomation({ ...task, enabled: !task.enabled });
      void loadAutomations();
    } catch (err) {
      flash(String(err));
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
    const task = automations.find((t) => t.id === run.task_id);
    const taskWorkspace = task?.workspace ?? workspace;
    try {
      await openSessionInWorkspace(run.conversation_id, taskWorkspace);
      closeTools();
    } catch (err) {
      flash(String(err));
    }
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
  const runningFor = (taskId: string) =>
    (runs[taskId] ?? []).some((r) => r.status === 'running');

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
    return t('automations.inDays', { count: days });
  };

  const filters: {
    value: 'all' | 'active' | 'paused' | 'completed';
    label: string;
  }[] = [
    { value: 'all', label: t('automations.filterAll') },
    { value: 'active', label: t('automations.filterActive') },
    { value: 'paused', label: t('automations.filterPaused') },
    { value: 'completed', label: t('automations.filterCompleted') },
  ];

  return (
    <div className="space-y-3">
      <div className="flex items-start justify-between gap-3">
        <p className="text-xs text-dim">{t('automations.hint')}</p>
        <div className="relative">
          <div className="flex items-center overflow-hidden rounded-control">
            <Button
              variant="primary"
              className="rounded-r-none"
              onClick={() => void openAICreate()}
              title={t('automations.createByAssistant')}
            >
              <Plus size={ICON.sm} />
              {t('automations.new')}
            </Button>
            <button
              ref={createTriggerRef}
              onClick={() => setCreateMenuOpen((v) => !v)}
              aria-haspopup="menu"
              aria-expanded={createMenuOpen}
              className="h-full border-l border-white/25 bg-accent px-1.5 py-1.5 text-white hover:opacity-90"
              data-tip={t('automations.createOptions')}
            >
              <ChevronDown size={ICON.xs} />
            </button>
          </div>
          <Popover
            open={createMenuOpen}
            onClose={() => setCreateMenuOpen(false)}
            anchor={createTriggerRef.current}
            role="menu"
            keyboard
            align="end"
            panelClassName="w-56 rounded-control border border-edge bg-panel p-1 shadow-popover"
          >
            <button
              role="menuitem"
              onClick={() => void openAICreate()}
              className="flex w-full items-center gap-2 rounded-control px-2 py-1.5 text-left text-xs text-dim hover:bg-panel2 hover:text-fg"
            >
              <Sparkles size={ICON.xs} className="shrink-0 text-accent" />
              <span className="flex-1 text-left">
                {t('automations.createByAssistant')}
              </span>
              <Check size={ICON.xs} className="shrink-0 text-accent" />
            </button>
            <div className="my-1 border-t border-edge" />
            <button
              role="menuitem"
              onClick={() => {
                setCreateMenuOpen(false);
                openNew();
              }}
              className="flex w-full items-center gap-2 rounded-control px-2 py-1.5 text-left text-xs text-dim hover:bg-panel2 hover:text-fg"
            >
              <Pencil size={ICON.xs} className="shrink-0" />
              <span className="flex-1 text-left">
                {t('automations.createManual')}
              </span>
            </button>
          </Popover>
        </div>
      </div>

      {error && (
        <div className="rounded-control border border-err/40 bg-err/10 px-3 py-2 text-sm text-err">
          {error}
        </div>
      )}

      <div className="relative">
        <Search
          size={ICON.xs}
          className="absolute left-2.5 top-1/2 -translate-y-1/2 text-dim"
        />
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t('automations.search')}
          className="w-full rounded-control border border-edge bg-panel pl-8 pr-3 py-1.5 text-sm outline-none focus:border-accent"
        />
      </div>

      <div className="flex flex-wrap gap-1.5">
        {filters.map((f) => (
          <button
            key={f.value}
            onClick={() => setFilter(f.value)}
            className={`rounded-control px-3 py-1 text-xs border ${
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
        <div className="rounded-card border border-edge bg-panel2 p-6 text-center text-sm text-dim">
          {t('automations.empty')}
        </div>
      ) : (
        <div className="space-y-2">
          {filtered.map((task) => (
            <div
              key={task.id}
              className="rounded-card border border-edge bg-panel2 p-3 hover:border-accent/40 transition-colors space-y-1.5"
            >
              <div className="flex items-start gap-2">
                <button
                  onClick={() => openEdit(task)}
                  className="flex-1 min-w-0 text-left space-y-1.5"
                >
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-semibold min-w-0 truncate">
                      {task.name}
                    </span>
                    {!task.enabled && (
                      <span className="rounded-tight border border-edge px-1.5 py-0.5 text-micro text-dim">
                        {t('automations.paused')}
                      </span>
                    )}
                    {task.last_status && (
                      <span
                        className={`rounded-tight px-1.5 py-0.5 text-micro ${statusClass(
                          task.last_status,
                        )}`}
                      >
                        {task.last_status}
                      </span>
                    )}
                    <span className="flex-1" />
                  </div>
                  <div className="flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs text-dim">
                    {/* An empty next_run_at used to print "Next: — —":
                        the placeholder plus the countdown's own dash. */}
                    {task.next_run_at ? (
                      <>
                        <span>
                          {t('automations.next')}: {fmtTime(task.next_run_at)}
                        </span>
                        {task.enabled && (
                          <span className="text-accent">
                            {timeUntil(task.next_run_at)}
                          </span>
                        )}
                      </>
                    ) : (
                      <span>
                        {t('automations.next')}: {t('automations.noNextRun')}
                      </span>
                    )}
                  </div>
                </button>
                <div className="relative shrink-0">
                  <button
                    aria-haspopup="menu"
                    aria-expanded={rowMenu?.id === task.id}
                    onClick={(e) =>
                      setRowMenu(
                        rowMenu?.id === task.id
                          ? null
                          : { id: task.id, anchor: e.currentTarget },
                      )
                    }
                    data-tip={t('automations.more')}
                    className="rounded-control p-1.5 text-dim hover:bg-panel hover:text-fg"
                  >
                    <MoreHorizontal size={ICON.sm} />
                  </button>
                  <Popover
                    open={rowMenu?.id === task.id}
                    onClose={() => setRowMenu(null)}
                    anchor={rowMenu?.anchor ?? null}
                    role="menu"
                    keyboard
                    align="end"
                    panelClassName="w-44 rounded-control border border-edge bg-panel p-1 shadow-popover"
                  >
                    <button
                      role="menuitem"
                      onClick={() => {
                        setRowMenu(null);
                        void toggleEnabled(task);
                      }}
                      className="flex w-full items-center gap-2 rounded-control px-2 py-1.5 text-left text-xs text-dim hover:bg-panel2 hover:text-fg"
                    >
                      {task.enabled ? (
                        <Pause size={ICON.xs} className="shrink-0" />
                      ) : (
                        <Play size={ICON.xs} className="shrink-0" />
                      )}
                      <span className="flex-1 text-left">
                        {task.enabled
                          ? t('automations.pause')
                          : t('automations.resume')}
                      </span>
                    </button>
                    <button
                      role="menuitem"
                      onClick={() => {
                        setRowMenu(null);
                        void runNow(task);
                      }}
                      disabled={
                        runningId === task.id ||
                        !task.enabled ||
                        runningFor(task.id)
                      }
                      className="flex w-full items-center gap-2 rounded-control px-2 py-1.5 text-left text-xs text-dim hover:bg-panel2 hover:text-fg disabled:opacity-40"
                    >
                      {runningId === task.id || runningFor(task.id) ? (
                        <Loader2
                          size={ICON.xs}
                          className="shrink-0 animate-spin"
                        />
                      ) : (
                        <Zap size={ICON.xs} className="shrink-0" />
                      )}
                      <span className="flex-1 text-left">
                        {t('automations.runNow')}
                      </span>
                    </button>
                    <div className="my-1 border-t border-edge" />
                    <button
                      role="menuitem"
                      onClick={() => {
                        setRowMenu(null);
                        setConfirmDelete(task.id);
                      }}
                      className="flex w-full items-center gap-2 rounded-control px-2 py-1.5 text-left text-xs text-dim hover:bg-err/10 hover:text-err"
                    >
                      <Trash2 size={ICON.xs} className="shrink-0" />
                      <span className="flex-1 text-left">
                        {t('automations.delete')}
                      </span>
                    </button>
                  </Popover>
                </div>
              </div>
            </div>
          ))}
        </div>
      )}

      {form && (
        <Overlay
          open
          onClose={() => setForm(null)}
          variant="drawer-right"
          ariaLabel={form.id ? t('automations.edit') : t('automations.new')}
          panelClassName="flex w-[26rem] max-w-[92vw] flex-col border-l border-edge bg-panel shadow-modal"
        >
          <div className="flex shrink-0 items-center justify-between border-b border-edge px-4 py-3">
            <h3 className="text-title font-semibold">
              {form.id ? t('automations.edit') : t('automations.new')}
            </h3>
            <button
              onClick={() => setForm(null)}
              className="text-dim hover:text-fg"
              data-tip={t('tools.close')}
            >
              <X size={ICON.md} />
            </button>
          </div>

          <div className="min-h-0 flex-1 space-y-4 overflow-y-auto p-4">
            <Field label={t('automations.name')}>
              <input
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                placeholder={t('automations.namePlaceholder')}
                className="w-full rounded-control border border-edge bg-panel2 px-3 py-1.5 text-sm outline-none focus:border-accent"
              />
            </Field>

            <Field label={t('automations.prompt')}>
              <textarea
                value={form.prompt}
                onChange={(e) => setForm({ ...form, prompt: e.target.value })}
                placeholder={t('automations.promptPlaceholder')}
                rows={4}
                className="w-full rounded-control border border-edge bg-panel2 px-3 py-1.5 text-sm outline-none focus:border-accent resize-y"
              />
            </Field>

            <SectionCard
              title={t('automations.details')}
              icon={<Settings2 size={ICON.md} />}
            >
              <Field label={t('automations.workspace')}>
                <CapsuleSelect
                  value={form.workspace}
                  options={workspaceOptions(form.workspace)}
                  onChange={(v) => setForm({ ...form, workspace: v })}
                  menuClassName="w-80"
                  buttonClassName="flex w-full items-center justify-between gap-1.5 rounded-control border border-edge bg-panel px-3 py-1.5 text-sm text-dim hover:text-fg"
                />
              </Field>
              <Field label={t('automations.sessionMode')}>
                <CapsuleSelect
                  value={form.sessionMode}
                  options={[
                    { value: 'new', label: t('automations.newSession') },
                    {
                      value: 'existing',
                      label: t('automations.existingSession'),
                    },
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
                    buttonClassName="flex w-full items-center justify-between gap-1.5 rounded-control border border-edge bg-panel px-3 py-1.5 text-sm text-dim hover:text-fg"
                  />
                  {sessionOptions.length === 0 && (
                    <p className="text-xs text-dim">
                      {t('automations.noSessions')}
                    </p>
                  )}
                </Field>
              )}
              <div className="grid grid-cols-2 gap-3">
                {form.sessionMode === 'new' && !yoloOnly && (
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
                    <div className="relative">
                      <button
                        type="button"
                        ref={modelTriggerRef}
                        aria-haspopup="listbox"
                        aria-expanded={modelMenuOpen}
                        onClick={() => setModelMenuOpen((v) => !v)}
                        data-tip={t('chat.modelLabel')}
                        className="flex w-full items-center gap-1.5 rounded-control border border-edge bg-panel px-3 py-1.5 text-sm text-dim hover:text-fg"
                      >
                        <Sparkles size={ICON.xs} className="text-accent" />
                        <span className="flex-1 truncate text-left">
                          {modelLabel}
                        </span>
                        <span className="text-edge">·</span>
                        <span>{thinkLabel}</span>
                        <ChevronUp size={ICON.xs} />
                      </button>
                      <Popover
                        open={modelMenuOpen}
                        onClose={() => setModelMenuOpen(false)}
                        anchor={modelTriggerRef.current}
                        role="listbox"
                        ariaLabel={t('chat.modelLabel')}
                        matchWidth
                        keyboard
                        panelClassName="rounded-card border border-edge bg-panel p-1.5 shadow-popover"
                      >
                        <div className="px-2 pb-1 pt-1.5 text-micro uppercase tracking-wider text-dim">
                          {t('chat.modelLabel')}
                        </div>
                        <div className="max-h-52 overflow-y-auto">
                          <button
                            type="button"
                            role="option"
                            aria-selected={!form.model}
                            onClick={() => {
                              setModelMenuOpen(false);
                              setForm({ ...form, model: '' });
                            }}
                            className={`flex w-full items-center justify-between rounded-control px-2 py-1.5 text-left text-xs ${
                              !form.model
                                ? 'bg-accent/10 text-accent'
                                : 'text-dim hover:bg-panel2 hover:text-fg'
                            }`}
                          >
                            <span>{t('chat.modelAuto')}</span>
                            {!form.model && <Check size={ICON.xs} />}
                          </button>
                          {modelOptions.map((m) => (
                            <button
                              key={m.id}
                              type="button"
                              role="option"
                              aria-selected={form.model === m.id}
                              onClick={() => {
                                setModelMenuOpen(false);
                                setForm({ ...form, model: m.id });
                              }}
                              className={`flex w-full items-center justify-between rounded-control px-2 py-1.5 text-left text-xs ${
                                form.model === m.id
                                  ? 'bg-accent/10 text-accent'
                                  : 'text-dim hover:bg-panel2 hover:text-fg'
                              }`}
                            >
                              <span className="truncate">{m.label}</span>
                              {form.model === m.id && <Check size={ICON.xs} />}
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
                            max={thinkLevels.length - 1}
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
                        <div className="flex justify-between px-2 pb-1.5 text-micro text-dim">
                          {thinkLevels.map((l) => (
                            <span key={l.value}>{l.label}</span>
                          ))}
                        </div>
                      </Popover>
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
              icon={<CalendarClock size={ICON.md} />}
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
                        className="w-full rounded-control border border-edge bg-panel px-2 py-1.5 text-sm outline-none focus:border-accent"
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
                      className="w-full rounded-control border border-edge bg-panel px-2 py-1.5 text-sm outline-none focus:border-accent"
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
                      className="w-full rounded-control border border-edge bg-panel px-2 py-1.5 text-sm outline-none focus:border-accent"
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
              <button
                onClick={() => void save()}
                disabled={saving}
                className="flex items-center gap-1.5 rounded-control bg-accent px-4 py-1.5 text-sm text-white hover:opacity-90 disabled:opacity-50"
              >
                {saving && <Loader2 size={ICON.sm} className="animate-spin" />}
                {t('automations.save')}
              </button>
              <button
                onClick={() => setForm(null)}
                className="rounded-control border border-edge px-3 py-1.5 text-sm text-dim hover:text-fg"
              >
                {t('interact.cancel')}
              </button>
            </div>

            {form.id && (
              <div className="space-y-2 border-t border-edge pt-3">
                <div className="flex items-center gap-2">
                  <h4 className="text-sm font-semibold">
                    {t('automations.history')}
                  </h4>
                  <History size={ICON.xs} className="text-dim" />
                </div>
                {historyRuns.length === 0 ? (
                  <p className="text-xs text-dim">{t('automations.noRuns')}</p>
                ) : (
                  historyRuns.map((run) => (
                    <div
                      key={run.id}
                      className="rounded-card border border-edge bg-panel p-2 space-y-1"
                    >
                      <div className="flex items-center gap-2 text-xs">
                        <span
                          className={`rounded-tight px-1.5 py-0.5 ${statusClass(
                            run.status,
                          )}`}
                        >
                          {run.status}
                        </span>
                        <span className="text-dim">{fmtTime(run.at)}</span>
                      </div>
                      {run.duration_ms > 0 && (
                        <p className="text-micro text-dim">
                          {t('automations.duration')}:{' '}
                          {(run.duration_ms / 1000).toFixed(1)}s
                        </p>
                      )}
                      {run.error && (
                        <p className="text-micro text-err min-w-0 truncate">
                          {runErrorMessage(run.error, t)}
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
          </div>
        </Overlay>
      )}

      <ConfirmDialog
        open={confirmDelete !== null}
        tone="danger"
        title={t('automations.deleteConfirm', {
          name: automations.find((a) => a.id === confirmDelete)?.name ?? '',
        })}
        confirmLabel={t('automations.delete')}
        onCancel={() => setConfirmDelete(null)}
        onConfirm={() => {
          if (confirmDelete !== null) void remove(confirmDelete);
        }}
      />
    </div>
  );
}
