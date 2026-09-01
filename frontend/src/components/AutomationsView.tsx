import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Clock,
  History,
  Loader2,
  Pencil,
  Play,
  Plus,
  Trash2,
  X,
} from 'lucide-react';
import { api } from '../lib/api';
import { useStore } from '../lib/store';
import type {
  AutomationRun,
  AutomationSchedule,
  AutomationTask,
} from '../lib/types';

interface FormState {
  id: string;
  name: string;
  prompt: string;
  scheduleType: string;
  intervalHours: number;
  days: string[];
  time: string;
  cron: string;
  workspace: string;
  mode: string;
  model: string;
  think: string;
  notify: string;
  enabled: boolean;
}

const WEEKDAYS = ['MO', 'TU', 'WE', 'TH', 'FR', 'SA', 'SU'];

const emptyForm = (workspace: string): FormState => ({
  id: '',
  name: '',
  prompt: '',
  scheduleType: 'daily',
  intervalHours: 2,
  days: ['MO', 'TU', 'WE', 'TH', 'FR'],
  time: '09:00',
  cron: '0 9 * * 1-5',
  workspace,
  mode: 'workspace',
  model: '',
  think: '',
  notify: 'always',
  enabled: true,
});

const taskToForm = (task: AutomationTask): FormState => ({
  id: task.id,
  name: task.name,
  prompt: task.prompt,
  scheduleType: task.schedule.type,
  intervalHours: task.schedule.interval_hours ?? 2,
  days: task.schedule.days ?? [],
  time: task.schedule.time ?? '09:00',
  cron: task.schedule.cron ?? '',
  workspace: task.workspace,
  mode: task.mode || 'workspace',
  model: task.model ?? '',
  think: task.think ?? '',
  notify: task.notify || 'always',
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
    schedule.days = f.days;
    schedule.time = f.time;
  } else if (f.scheduleType === 'cron') {
    schedule.cron = f.cron;
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
    case 'cron':
      return s.cron ?? '';
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

export function AutomationsView() {
  const { t } = useTranslation();
  const workspace = useStore((s) => s.workspace);
  const automations = useStore((s) => s.automations);
  const runs = useStore((s) => s.automationRuns);
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

  useEffect(() => {
    void loadAutomations();
  }, [loadAutomations]);

  const openNew = () => {
    setError('');
    setForm(emptyForm(workspace));
  };

  const openEdit = (task: AutomationTask) => {
    setError('');
    setForm(taskToForm(task));
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

  const toggleEnabled = async (task: AutomationTask) => {
    try {
      await api.saveAutomation({ ...task, enabled: !task.enabled });
      void loadAutomations();
    } catch (err) {
      flash(String(err));
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
      setHistoryFor((h) => (h === id ? null : h));
      void loadAutomations();
    } catch (err) {
      flash(String(err));
    }
  };

  const showHistory = (id: string) => {
    setHistoryFor(id);
    void loadAutomationRuns(id);
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

      {form && (
        <div className="rounded-xl border border-edge bg-panel2 p-3 space-y-3">
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-semibold">
              {form.id ? t('automations.edit') : t('automations.new')}
            </h3>
            <button
              onClick={() => setForm(null)}
              className="text-dim hover:text-fg"
              title={t('tools.close')}
            >
              <X size="1.1428rem" />
            </button>
          </div>

          <div className="grid grid-cols-2 gap-2">
            <input
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
              placeholder={t('automations.name')}
              className="rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
            />
            <input
              value={form.workspace}
              onChange={(e) => setForm({ ...form, workspace: e.target.value })}
              placeholder={t('automations.workspace')}
              className="rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
            />
          </div>

          <textarea
            value={form.prompt}
            onChange={(e) => setForm({ ...form, prompt: e.target.value })}
            placeholder={t('automations.promptPlaceholder')}
            rows={4}
            className="w-full rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent resize-y"
          />

          <div className="flex flex-wrap items-center gap-2">
            <select
              value={form.scheduleType}
              onChange={(e) =>
                setForm({ ...form, scheduleType: e.target.value })
              }
              className="rounded-lg border border-edge bg-panel px-2 py-1.5 text-sm outline-none"
            >
              <option value="hourly">{t('automations.hourly')}</option>
              <option value="daily">{t('automations.daily')}</option>
              <option value="weekdays">{t('automations.weekdays')}</option>
              <option value="weekly">{t('automations.weekly')}</option>
              <option value="cron">{t('automations.cron')}</option>
            </select>

            {form.scheduleType === 'hourly' && (
              <label className="flex items-center gap-1.5 text-sm text-dim">
                {t('automations.every')}
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
                  className="w-16 rounded-lg border border-edge bg-panel px-2 py-1.5 text-sm outline-none focus:border-accent"
                />
                h
              </label>
            )}

            {(form.scheduleType === 'daily' ||
              form.scheduleType === 'weekdays' ||
              form.scheduleType === 'weekly') && (
              <input
                type="time"
                value={form.time}
                onChange={(e) => setForm({ ...form, time: e.target.value })}
                className="rounded-lg border border-edge bg-panel px-2 py-1.5 text-sm outline-none focus:border-accent"
              />
            )}

            {form.scheduleType === 'cron' && (
              <input
                value={form.cron}
                onChange={(e) => setForm({ ...form, cron: e.target.value })}
                placeholder="0 9 * * 1-5"
                className="flex-1 min-w-40 rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
              />
            )}
          </div>

          {(form.scheduleType === 'hourly' ||
            form.scheduleType === 'weekly') && (
            <div className="flex flex-wrap gap-1.5">
              {WEEKDAYS.map((d) => (
                <button
                  key={d}
                  onClick={() => toggleDay(d)}
                  className={`rounded px-2 py-1 text-xs border ${
                    form.days.includes(d)
                      ? 'border-accent/50 bg-accent/15 text-accent'
                      : 'border-edge bg-panel text-dim hover:text-fg'
                  }`}
                >
                  {d}
                </button>
              ))}
            </div>
          )}

          <div className="grid grid-cols-4 gap-2">
            <select
              value={form.mode}
              onChange={(e) => setForm({ ...form, mode: e.target.value })}
              className="rounded-lg border border-edge bg-panel px-2 py-1.5 text-sm outline-none"
            >
              <option value="workspace">
                {t('automations.modeWorkspace')}
              </option>
              <option value="read-only">{t('automations.modeReadonly')}</option>
              <option value="yolo">{t('automations.modeYolo')}</option>
            </select>
            <select
              value={form.think}
              onChange={(e) => setForm({ ...form, think: e.target.value })}
              className="rounded-lg border border-edge bg-panel px-2 py-1.5 text-sm outline-none"
            >
              <option value="">{t('automations.thinkDefault')}</option>
              <option value="low">{t('automations.thinkLow')}</option>
              <option value="medium">{t('automations.thinkMedium')}</option>
              <option value="high">{t('automations.thinkHigh')}</option>
            </select>
            <select
              value={form.notify}
              onChange={(e) => setForm({ ...form, notify: e.target.value })}
              className="rounded-lg border border-edge bg-panel px-2 py-1.5 text-sm outline-none"
            >
              <option value="always">{t('automations.notifyAlways')}</option>
              <option value="failed">{t('automations.notifyFailed')}</option>
              <option value="never">{t('automations.notifyNever')}</option>
            </select>
            <input
              value={form.model}
              onChange={(e) => setForm({ ...form, model: e.target.value })}
              placeholder={t('automations.model')}
              className="rounded-lg border border-edge bg-panel px-2 py-1.5 text-sm outline-none focus:border-accent"
            />
          </div>

          {form.mode === 'yolo' && (
            <p className="text-xs text-err">{t('automations.yoloWarning')}</p>
          )}

          <div className="flex items-center gap-2">
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
          </div>
        </div>
      )}

      {automations.length === 0 && !form && (
        <div className="rounded-xl border border-edge bg-panel2 p-6 text-center text-sm text-dim">
          {t('automations.empty')}
        </div>
      )}

      <div className="flex gap-3">
        <div className="flex-1 min-w-0 space-y-2">
          {automations.map((task) => {
            const mismatch = !workspace || task.workspace !== workspace;
            return (
              <div
                key={task.id}
                className="rounded-xl border border-edge bg-panel2 p-3 space-y-2"
              >
                <div className="flex items-center gap-2">
                  <Clock size="1.0000rem" className="text-accent shrink-0" />
                  <span className="text-sm font-semibold min-w-0 truncate">
                    {task.name}
                  </span>
                  {!task.enabled && (
                    <span className="rounded border border-edge px-1.5 py-0.5 text-[0.7143rem] text-dim">
                      {t('automations.paused')}
                    </span>
                  )}
                  {mismatch && (
                    <span
                      className="rounded border border-err/40 bg-err/10 px-1.5 py-0.5 text-[0.7143rem] text-err"
                      title={task.workspace}
                    >
                      {t('automations.needWorkspace')}
                    </span>
                  )}
                  <span className="flex-1" />
                  <label className="flex items-center gap-1.5 text-xs text-dim">
                    <input
                      type="checkbox"
                      checked={task.enabled}
                      onChange={() => void toggleEnabled(task)}
                    />
                    {t('automations.enabled')}
                  </label>
                </div>
                <p className="text-xs text-dim min-w-0 truncate">
                  {task.prompt}
                </p>
                <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-dim">
                  <span>{scheduleLabel(task.schedule)}</span>
                  <span className="min-w-0 truncate">{task.workspace}</span>
                  <span>
                    {t('automations.next')}: {fmtTime(task.next_run_at)}
                  </span>
                  {task.last_status && (
                    <span
                      className={`rounded px-1.5 py-0.5 ${statusClass(
                        task.last_status,
                      )}`}
                    >
                      {task.last_status}
                    </span>
                  )}
                </div>
                <div className="flex items-center gap-2">
                  <button
                    onClick={() => void runNow(task)}
                    disabled={
                      runningId === task.id || !task.enabled || mismatch
                    }
                    className="flex items-center gap-1 rounded-lg border border-edge px-2.5 py-1 text-xs text-dim hover:text-fg disabled:opacity-40"
                    title={t('automations.runNow')}
                  >
                    {runningId === task.id ? (
                      <Loader2 size="0.8571rem" className="animate-spin" />
                    ) : (
                      <Play size="0.8571rem" />
                    )}
                    {t('automations.runNow')}
                  </button>
                  <button
                    onClick={() => showHistory(task.id)}
                    className="flex items-center gap-1 rounded-lg border border-edge px-2.5 py-1 text-xs text-dim hover:text-fg"
                  >
                    <History size="0.8571rem" />
                    {t('automations.history')}
                  </button>
                  <button
                    onClick={() => openEdit(task)}
                    className="flex items-center gap-1 rounded-lg border border-edge px-2.5 py-1 text-xs text-dim hover:text-fg"
                  >
                    <Pencil size="0.8571rem" />
                    {t('automations.edit')}
                  </button>
                  <button
                    onClick={() => setConfirmDelete(task.id)}
                    className="flex items-center gap-1 rounded-lg border border-edge px-2.5 py-1 text-xs text-dim hover:text-err"
                  >
                    <Trash2 size="0.8571rem" />
                    {t('automations.delete')}
                  </button>
                </div>
                {confirmDelete === task.id && (
                  <div className="flex items-center gap-2 text-xs text-dim">
                    <span>
                      {t('automations.deleteConfirm', { name: task.name })}
                    </span>
                    <button
                      onClick={() => void remove(task.id)}
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
              </div>
            );
          })}
        </div>

        {historyFor && (
          <div className="w-80 shrink-0 rounded-xl border border-edge bg-panel2 p-3 space-y-2 max-h-full overflow-y-auto">
            <div className="flex items-center justify-between">
              <h3 className="text-sm font-semibold">
                {t('automations.history')}
              </h3>
              <button
                onClick={() => setHistoryFor(null)}
                className="text-dim hover:text-fg"
              >
                <X size="1.0000rem" />
              </button>
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
      </div>
    </div>
  );
}
