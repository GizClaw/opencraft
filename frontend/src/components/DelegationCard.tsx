import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ChevronRight, Plus, Workflow, X } from 'lucide-react';
import { api } from '../lib/api';
import { useStore } from '../lib/store';
import type { DelegationState } from '../lib/types';
import { ICON } from './ui/icon';
import { Modal } from './ui/Modal';
import { NumberField } from './ui/NumberField';
import { SaveBar } from './ui/SaveBar';

// The delegation item of the Tools tab: one list item that opens a
// dialog editing the delegation policy — how much delegated work may
// run at once, how deep it may nest, and which targets the model may
// hand work to.
//
// The policy is enforced on every delegate call, and hiding a target
// from the listing is a hint rather than a guarantee: a call that names
// a restricted target is refused. The service limits are read by the
// delegation service at assembly, so a save reloads the document
// instead of poking the running service.

const inputClass =
  'w-full rounded-control border border-edge bg-panel px-2.5 py-1.5 text-xs text-fg ' +
  'outline-none transition-colors hover:border-accent/50 focus:border-accent ' +
  'disabled:opacity-40';

/** TargetEditor edits one target list: removable chips, a free-text
 *  input (a pattern like `writer*` is a valid entry) and the targets the
 *  runtime currently offers as one-click suggestions. */
function TargetEditor({
  label,
  emptyHint,
  addLabel,
  removeLabel,
  suggestionLabel,
  suggestionName,
  value,
  suggestions,
  onChange,
}: {
  label: string;
  emptyHint: string;
  addLabel: string;
  removeLabel: (name: string) => string;
  suggestionLabel: string;
  suggestionName: (name: string) => string;
  value: string[];
  suggestions: string[];
  onChange: (next: string[]) => void;
}) {
  const [draft, setDraft] = useState('');

  const add = (name: string) => {
    const trimmed = name.trim();
    if (trimmed === '' || value.includes(trimmed)) {
      setDraft('');
      return;
    }
    onChange([...value, trimmed]);
    setDraft('');
  };

  return (
    <div className="space-y-1.5">
      <p className="text-xs text-dim">{label}</p>
      <div className="flex min-h-6 flex-wrap items-center gap-1.5">
        {value.length === 0 && (
          <span className="text-micro text-faint">{emptyHint}</span>
        )}
        {value.map((name) => (
          <span
            key={name}
            className="inline-flex items-center gap-1 rounded-tight border border-edge bg-panel px-1.5 py-0.5 text-micro"
          >
            <code className="font-mono">{name}</code>
            <button
              type="button"
              aria-label={removeLabel(name)}
              onClick={() => onChange(value.filter((v) => v !== name))}
              className="text-dim hover:text-err"
            >
              <X size={ICON.xs} />
            </button>
          </span>
        ))}
      </div>
      <div className="flex items-center gap-1.5">
        <input
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault();
              add(draft);
            }
          }}
          placeholder={addLabel}
          aria-label={addLabel}
          className={inputClass}
        />
        <button
          type="button"
          onClick={() => add(draft)}
          disabled={draft.trim() === ''}
          className="flex shrink-0 items-center gap-1 rounded-control border border-edge px-2 py-1.5 text-xs text-dim transition-colors hover:border-accent/40 hover:text-fg disabled:opacity-40"
        >
          <Plus size={ICON.xs} />
          {addLabel}
        </button>
      </div>
      {suggestions.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-micro text-faint">{suggestionLabel}</span>
          {suggestions.map((name) => (
            <button
              key={name}
              type="button"
              aria-label={suggestionName(name)}
              onClick={() => add(name)}
              className="rounded-tight border border-edge bg-panel px-1.5 py-0.5 font-mono text-micro text-dim transition-colors hover:border-accent/40 hover:text-fg"
            >
              {name}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

export function DelegationCard() {
  const { t } = useTranslation();
  const toast = useStore((s) => s.toast);
  const [state, setState] = useState<DelegationState | null>(null);
  const [open, setOpen] = useState(false);
  const [concurrency, setConcurrency] = useState(4);
  const [depth, setDepth] = useState(8);
  const [allowed, setAllowed] = useState<string[]>([]);
  const [blocked, setBlocked] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState('');

  const apply = (next: DelegationState) => {
    setState(next);
    setConcurrency(next.max_concurrency);
    setDepth(next.max_depth);
    setAllowed(next.allowed_targets ?? []);
    setBlocked(next.blocked_targets ?? []);
  };

  useEffect(() => {
    let live = true;
    void api
      .delegationState()
      .then((next) => {
        if (live) apply(next);
      })
      .catch((err) => {
        if (live) setError(String(err));
      });
    return () => {
      live = false;
    };
  }, []);

  const save = async () => {
    setSaving(true);
    setError('');
    try {
      await api.saveDelegationSettings({
        max_concurrency: concurrency,
        max_depth: depth,
        allowed_targets: allowed,
        blocked_targets: blocked,
      });
      apply(await api.delegationState());
      setSaved(true);
      toast(t('config.delegationSaved'), 'info');
      setOpen(false);
    } catch (err) {
      setError(String(err));
    } finally {
      setSaving(false);
    }
  };

  if (state === null && error === '') {
    return (
      <div className="h-16 animate-pulse rounded-card border border-edge/70 bg-panel" />
    );
  }

  // The suggestion row offers what the runtime has; a name already
  // listed on either side has nothing to add.
  const suggestions = (state?.targets ?? []).filter(
    (name) => !allowed.includes(name) && !blocked.includes(name),
  );

  return (
    <>
      <ul className="flex flex-col gap-2">
        <li className="[content-visibility:auto] [contain-intrinsic-size:auto_4.5rem] rounded-card border border-edge bg-panel2 p-3 transition-colors hover:border-accent/40">
          <button
            type="button"
            onClick={() => {
              setSaved(false);
              setOpen(true);
            }}
            className="flex w-full min-w-0 items-center gap-2 text-left"
          >
            <Workflow size={ICON.sm} className="shrink-0 text-accent" />
            <span className="min-w-0 flex-1">
              <span className="flex flex-wrap items-center gap-1.5">
                <span className="min-w-0 truncate text-sm font-semibold">
                  {t('config.delegationTitle')}
                </span>
                <span className="shrink-0 rounded-tight border border-edge bg-panel px-1.5 py-0.5 text-micro text-dim">
                  {t('config.delegationBadge', {
                    concurrency: state?.max_concurrency ?? 0,
                    depth: state?.max_depth ?? 0,
                  })}
                </span>
                {state !== null && !state.targets_available && (
                  <span className="shrink-0 rounded-tight border border-edge bg-panel px-1.5 py-0.5 text-micro text-dim">
                    {t('config.delegationTargetsUnknown')}
                  </span>
                )}
              </span>
              <span className="mt-1 block truncate text-xs text-dim">
                {t('config.delegationHint')}
              </span>
            </span>
            <ChevronRight size={ICON.sm} className="shrink-0 text-dim" />
          </button>
        </li>
      </ul>

      <Modal
        open={open}
        onClose={() => setOpen(false)}
        title={t('config.delegationTitle')}
        icon={Workflow}
        width="38rem"
        portal
        bodyClassName="p-0"
        footer={
          <SaveBar
            saved={saved}
            error={error}
            saving={saving}
            onSave={() => void save()}
          />
        }
      >
        <div className="min-h-0 flex-1 space-y-3 overflow-y-auto px-4 py-3">
          <p className="text-xs text-dim">{t('config.delegationHint')}</p>

          <div className="grid grid-cols-2 gap-3">
            {/* A div, not a label: the −/+ cells are buttons, and a
                label element's control is its first labelable
                descendant — the minus cell. The field carries its name
                as an aria-label instead. */}
            <div className="space-y-1">
              <span className="block text-xs text-dim">
                {t('config.delegationConcurrency')}
              </span>
              <NumberField
                label={t('config.delegationConcurrency')}
                size="sm"
                steppers
                min={state?.min_max_concurrency ?? 1}
                max={state?.max_max_concurrency ?? undefined}
                value={concurrency}
                onChange={(next) => {
                  if (next === '') return;
                  setConcurrency(next);
                  setSaved(false);
                }}
                className="w-full"
              />
              <span className="block text-micro text-faint">
                {t('config.delegationConcurrencyHint', {
                  max: state?.max_max_concurrency ?? 0,
                  fallback: state?.default_max_concurrency ?? 0,
                })}
              </span>
            </div>
            <div className="space-y-1">
              <span className="block text-xs text-dim">
                {t('config.delegationDepth')}
              </span>
              <NumberField
                label={t('config.delegationDepth')}
                size="sm"
                steppers
                min={state?.min_max_depth ?? 1}
                max={state?.max_max_depth ?? undefined}
                value={depth}
                onChange={(next) => {
                  if (next === '') return;
                  setDepth(next);
                  setSaved(false);
                }}
                className="w-full"
              />
              <span className="block text-micro text-faint">
                {t('config.delegationDepthHint', {
                  max: state?.max_max_depth ?? 0,
                  fallback: state?.default_max_depth ?? 0,
                })}
              </span>
            </div>
          </div>

          <p className="text-micro text-faint">
            {t('config.delegationPolicyHint', {
              max: state?.max_targets ?? 0,
            })}
          </p>

          <TargetEditor
            label={t('config.delegationAllowed')}
            emptyHint={t('config.delegationAllowedEmpty')}
            addLabel={t('config.delegationAddTarget')}
            removeLabel={(name) => t('config.delegationRemoveTarget', { name })}
            suggestionLabel={t('config.delegationSuggested')}
            suggestionName={(name) =>
              t('config.delegationAllowTarget', { name })
            }
            value={allowed}
            suggestions={suggestions}
            onChange={(next) => {
              setAllowed(next);
              setSaved(false);
            }}
          />

          <TargetEditor
            label={t('config.delegationBlocked')}
            emptyHint={t('config.delegationBlockedEmpty')}
            addLabel={t('config.delegationAddTarget')}
            removeLabel={(name) => t('config.delegationRemoveTarget', { name })}
            suggestionLabel={t('config.delegationSuggested')}
            suggestionName={(name) =>
              t('config.delegationBlockTarget', { name })
            }
            value={blocked}
            suggestions={suggestions.filter((name) => !allowed.includes(name))}
            onChange={(next) => {
              setBlocked(next);
              setSaved(false);
            }}
          />
        </div>
      </Modal>
    </>
  );
}
