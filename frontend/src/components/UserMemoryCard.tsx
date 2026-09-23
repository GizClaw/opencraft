import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Database,
  Eye,
  EyeOff,
  Check,
  Pencil,
  Plus,
  RotateCcw,
  Trash2,
  X,
} from 'lucide-react';
import { api } from '../lib/api';
import { useStore } from '../lib/store';
import { formatDateTime } from '../lib/datetime';
import type {
  MemoryFact,
  ReviewState,
  ReviewSuggestion,
  UserMemoryState,
} from '../lib/types';
import { Badge } from './ui/Badge';
import { Button } from './ui/Button';
import { ConfirmDialog } from './ui/ConfirmDialog';
import { ICON } from './ui/icon';
import { SaveBar } from './ui/SaveBar';

// The long-term memory card of the Memory tab: the user-level facts the
// host injects into every turn, plus the queue of suggestions the
// post-turn review proposed and is waiting for a verdict on.
//
// Both halves are one card on purpose: accepting a suggestion is the
// same write as typing a fact by hand, and seeing the two lists next to
// each other is what makes that obvious. Facts are always written
// through the host's store — this card never composes its own SQL — so
// the limits, the dedupe and the provenance are the same everywhere.

const inputClass =
  'w-full rounded-control border border-edge bg-panel px-2.5 py-1.5 text-xs text-fg ' +
  'outline-none transition-colors hover:border-accent/50 focus:border-accent ' +
  'disabled:opacity-40';

function scopeTone(scope: string | undefined): 'accent' | 'neutral' {
  return scope === 'global' ? 'accent' : 'neutral';
}

export function UserMemoryCard({
  onOpenConversation,
}: {
  /**
   * Jumps to the conversation a fact or suggestion came from. The
   * workspace is the one the turn ran in, so the caller can switch to
   * it before resuming; it is empty for a fact with no workspace (a
   * global one carries no conversation of its own).
   */
  onOpenConversation?: (conversationId: string, workspacePath?: string) => void;
}) {
  const { t } = useTranslation();
  const toast = useStore((s) => s.toast);

  const [state, setState] = useState<UserMemoryState | null>(null);
  const [facts, setFacts] = useState<MemoryFact[]>([]);
  const [review, setReview] = useState<ReviewState | null>(null);
  const [suggestions, setSuggestions] = useState<ReviewSuggestion[]>([]);
  const [loadError, setLoadError] = useState('');

  // Draft of the memory settings, seeded from the loaded state.
  const [enabled, setEnabled] = useState(true);
  const [maxItems, setMaxItems] = useState(0);
  const [maxChars, setMaxChars] = useState(0);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [saveError, setSaveError] = useState('');

  // Draft of the review cadence.
  const [reviewEnabled, setReviewEnabled] = useState(false);
  const [everyTurns, setEveryTurns] = useState(0);
  const [minToolCalls, setMinToolCalls] = useState(0);
  const [reviewSaving, setReviewSaving] = useState(false);
  const [reviewSaved, setReviewSaved] = useState(false);
  const [reviewError, setReviewError] = useState('');

  const [draft, setDraft] = useState('');
  const [draftScope, setDraftScope] = useState('');
  const [adding, setAdding] = useState(false);
  const [editingId, setEditingId] = useState('');
  const [editText, setEditText] = useState('');
  const [busyId, setBusyId] = useState('');
  const [deleteFact, setDeleteFact] = useState<MemoryFact | null>(null);

  const applyState = useCallback((next: UserMemoryState) => {
    setState(next);
    setEnabled(next.enabled);
    setMaxItems(next.inject_max_items);
    setMaxChars(next.inject_max_chars);
    setSaved(false);
  }, []);

  const applyReview = useCallback((next: ReviewState) => {
    setReview(next);
    setReviewEnabled(next.enabled);
    setEveryTurns(next.every_turns);
    setMinToolCalls(next.min_tool_calls);
    setReviewSaved(false);
  }, []);

  const reload = useCallback(async () => {
    const [nextState, nextFacts, nextReview, nextSuggestions] =
      await Promise.all([
        api.userMemoryState(),
        api.memoryFacts(),
        api.reviewSettings(),
        api.reviewSuggestions(),
      ]);
    applyState(nextState);
    setFacts(nextFacts ?? []);
    applyReview(nextReview);
    setSuggestions(nextSuggestions ?? []);
  }, [applyState, applyReview]);

  useEffect(() => {
    let live = true;
    void reload().catch((err) => {
      if (live) setLoadError(String(err));
    });
    return () => {
      live = false;
    };
  }, [reload]);

  const refreshFacts = async () => {
    applyState(await api.userMemoryState());
    setFacts((await api.memoryFacts()) ?? []);
  };

  const refreshQueue = async () => {
    applyReview(await api.reviewSettings());
    setSuggestions((await api.reviewSuggestions()) ?? []);
  };

  const addFact = async () => {
    const text = draft.trim();
    if (text === '') return;
    setAdding(true);
    try {
      await api.addMemoryFact({ text, scope: draftScope });
      setDraft('');
      await refreshFacts();
    } catch (err) {
      toast(String(err), 'warning');
    } finally {
      setAdding(false);
    }
  };

  const saveEdit = async (id: string) => {
    const text = editText.trim();
    if (text === '') return;
    setBusyId(id);
    try {
      await api.updateMemoryFact(id, text);
      setEditingId('');
      await refreshFacts();
    } catch (err) {
      toast(String(err), 'warning');
    } finally {
      setBusyId('');
    }
  };

  const toggleStale = async (fact: MemoryFact) => {
    setBusyId(fact.id);
    try {
      await api.setMemoryFactStale(fact.id, !fact.stale);
      await refreshFacts();
    } catch (err) {
      toast(String(err), 'warning');
    } finally {
      setBusyId('');
    }
  };

  const removeFact = async (fact: MemoryFact) => {
    setBusyId(fact.id);
    try {
      await api.removeMemoryFact(fact.id);
      setDeleteFact(null);
      await refreshFacts();
    } catch (err) {
      toast(String(err), 'warning');
    } finally {
      setBusyId('');
    }
  };

  const accept = async (suggestion: ReviewSuggestion) => {
    setBusyId(suggestion.id);
    try {
      await api.acceptReviewSuggestion(suggestion.id);
      await refreshQueue();
      await refreshFacts();
      toast(t('config.reviewAccepted'), 'info');
    } catch (err) {
      toast(String(err), 'warning');
    } finally {
      setBusyId('');
    }
  };

  const discard = async (suggestion: ReviewSuggestion) => {
    setBusyId(suggestion.id);
    try {
      await api.discardReviewSuggestion(suggestion.id);
      await refreshQueue();
      toast(t('config.reviewDiscarded'), 'info');
    } catch (err) {
      toast(String(err), 'warning');
    } finally {
      setBusyId('');
    }
  };

  const saveSettings = async () => {
    setSaving(true);
    setSaveError('');
    try {
      await api.saveUserMemorySettings({
        enabled,
        inject_max_items: maxItems,
        inject_max_chars: maxChars,
      });
      applyState(await api.userMemoryState());
      setSaved(true);
      toast(t('config.memoryFactsSaved'), 'info');
    } catch (err) {
      setSaveError(String(err));
    } finally {
      setSaving(false);
    }
  };

  const saveReview = async () => {
    setReviewSaving(true);
    setReviewError('');
    try {
      await api.saveReviewSettings({
        enabled: reviewEnabled,
        every_turns: everyTurns,
        min_tool_calls: minToolCalls,
      });
      applyReview(await api.reviewSettings());
      setReviewSaved(true);
      toast(t('config.reviewSaved'), 'info');
    } catch (err) {
      setReviewError(String(err));
    } finally {
      setReviewSaving(false);
    }
  };

  if (state === null && loadError === '') {
    return (
      <div className="h-16 animate-pulse rounded-card border border-edge/70 bg-panel" />
    );
  }

  const scopeLabel = (scope: string | undefined) =>
    scope === 'global'
      ? t('config.memoryFactsScopeGlobal')
      : t('config.memoryFactsScopeWorkspace');

  return (
    <div className="space-y-4">
      <div className="rounded-card border border-edge bg-panel2 p-3">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex items-center gap-2 text-sm font-medium">
              <Database size={ICON.sm} className="shrink-0 text-accent" />
              {t('config.memoryFactsTitle')}
            </div>
            <p className="mt-1 text-xs text-faint">
              {t('config.memoryFactsHint')}
            </p>
          </div>
          {state && (
            <div className="flex shrink-0 items-center gap-1.5">
              <Badge>
                {t('config.memoryFactsCount', {
                  live: state.live,
                  count: state.max_items,
                })}
              </Badge>
              {state.stale > 0 && (
                <Badge tone="warn">
                  {t('config.memoryFactsStaleCount', { stale: state.stale })}
                </Badge>
              )}
            </div>
          )}
        </div>

        {loadError !== '' && (
          <p className="mt-2 rounded-control border border-err/40 bg-err/10 px-3 py-2 text-xs text-err break-words">
            {loadError}
          </p>
        )}

        {state && !state.available && (
          <p className="mt-2.5 rounded-control border border-warn/40 bg-warn/10 px-3 py-2 text-xs text-warn">
            {t('config.memoryFactsUnavailable')}
          </p>
        )}

        {state && (
          <>
            <div className="mt-2.5 grid grid-cols-3 gap-3">
              <label className="flex items-center gap-2 rounded-control border border-edge bg-panel px-2.5 py-2">
                <input
                  type="checkbox"
                  checked={enabled}
                  onChange={(e) => {
                    setEnabled(e.target.checked);
                    setSaved(false);
                  }}
                  className="accent-accent"
                />
                <span className="min-w-0 text-xs">
                  {t('config.memoryFactsEnabled')}
                </span>
              </label>
              <label className="space-y-1.5">
                <span className="text-xs text-dim">
                  {t('config.memoryFactsItems')}
                </span>
                <input
                  type="number"
                  min={state.min_inject_max_items}
                  max={state.max_inject_max_items}
                  value={maxItems}
                  onChange={(e) => {
                    setMaxItems(Number(e.target.value) || 0);
                    setSaved(false);
                  }}
                  className={inputClass}
                />
                <span className="block text-micro text-faint">
                  {t('config.memoryFactsRange', {
                    min: state.min_inject_max_items,
                    max: state.max_inject_max_items,
                    value: state.default_inject_max_items,
                  })}
                </span>
              </label>
              <label className="space-y-1.5">
                <span className="text-xs text-dim">
                  {t('config.memoryFactsChars')}
                </span>
                <input
                  type="number"
                  min={state.min_inject_max_chars}
                  max={state.max_inject_max_chars}
                  step={256}
                  value={maxChars}
                  onChange={(e) => {
                    setMaxChars(Number(e.target.value) || 0);
                    setSaved(false);
                  }}
                  className={inputClass}
                />
                <span className="block text-micro text-faint">
                  {t('config.memoryFactsRange', {
                    min: state.min_inject_max_chars,
                    max: state.max_inject_max_chars,
                    value: state.default_inject_max_chars,
                  })}
                </span>
              </label>
            </div>
            <SaveBar
              saved={saved}
              error={saveError}
              saving={saving}
              onSave={() => void saveSettings()}
              className="mt-2 rounded-control"
            />
          </>
        )}

        <div className="mt-3 border-t border-edge pt-3">
          <div className="flex items-center gap-2">
            <input
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') void addFact();
              }}
              placeholder={t('config.memoryFactsAddPlaceholder')}
              aria-label={t('config.memoryFactsAddPlaceholder')}
              className={`${inputClass} flex-1`}
            />
            <select
              value={draftScope}
              onChange={(e) => setDraftScope(e.target.value)}
              aria-label={t('config.memoryFactsScopeLabel')}
              className="rounded-control border border-edge bg-panel px-2 py-1.5 text-xs text-fg outline-none focus:border-accent"
            >
              <option value="">{t('config.memoryFactsScopeAuto')}</option>
              <option value="workspace">
                {t('config.memoryFactsScopeWorkspace')}
              </option>
              <option value="global">
                {t('config.memoryFactsScopeGlobal')}
              </option>
            </select>
            <Button
              variant="secondary"
              size="sm"
              loading={adding}
              disabled={draft.trim() === ''}
              onClick={() => void addFact()}
            >
              <Plus size={ICON.xs} />
              {t('config.memoryFactsAdd')}
            </Button>
          </div>

          {facts.length === 0 ? (
            <p className="mt-2.5 text-xs text-dim">
              {t('config.memoryFactsEmpty')}
            </p>
          ) : (
            <ul className="mt-2.5 flex flex-col gap-2">
              {facts.map((fact) => {
                const source = fact.source_conversation;
                return (
                  <li
                    key={fact.id}
                    className="rounded-card border border-edge bg-panel p-2.5"
                  >
                    <div className="flex items-start gap-2">
                      <Badge tone={scopeTone(fact.scope)}>
                        {scopeLabel(fact.scope)}
                      </Badge>
                      {fact.stale && (
                        <Badge tone="warn">
                          {t('config.memoryFactsStale')}
                        </Badge>
                      )}
                      {editingId === fact.id ? (
                        <div className="flex min-w-0 flex-1 items-center gap-1.5">
                          <input
                            value={editText}
                            onChange={(e) => setEditText(e.target.value)}
                            aria-label={t('config.memoryFactsEdit')}
                            className={`${inputClass} flex-1`}
                          />
                          <button
                            onClick={() => void saveEdit(fact.id)}
                            disabled={busyId === fact.id}
                            aria-label={t('config.memoryFactsSaveEdit')}
                            className="rounded-control border border-ok/40 p-1 text-ok hover:bg-ok/10 disabled:opacity-40"
                          >
                            <Check size={ICON.xs} />
                          </button>
                          <button
                            onClick={() => setEditingId('')}
                            aria-label={t('config.memoryFactsCancelEdit')}
                            className="rounded-control border border-edge p-1 text-dim hover:bg-panel hover:text-fg"
                          >
                            <X size={ICON.xs} />
                          </button>
                        </div>
                      ) : (
                        <span className="min-w-0 flex-1 text-sm text-fg break-words">
                          {fact.text}
                        </span>
                      )}
                      {editingId !== fact.id && (
                        <div className="flex shrink-0 items-center gap-1">
                          <button
                            onClick={() => {
                              setEditingId(fact.id);
                              setEditText(fact.text);
                            }}
                            aria-label={t('config.memoryFactsEdit')}
                            data-tip={t('config.memoryFactsEdit')}
                            className="rounded-control border border-edge p-1 text-dim hover:bg-panel hover:text-fg"
                          >
                            <Pencil size={ICON.xs} />
                          </button>
                          <button
                            onClick={() => void toggleStale(fact)}
                            disabled={busyId === fact.id}
                            aria-label={
                              fact.stale
                                ? t('config.memoryFactsUnstale')
                                : t('config.memoryFactsMarkStale')
                            }
                            data-tip={
                              fact.stale
                                ? t('config.memoryFactsUnstale')
                                : t('config.memoryFactsMarkStale')
                            }
                            className="rounded-control border border-edge p-1 text-dim hover:bg-panel hover:text-fg disabled:opacity-40"
                          >
                            {fact.stale ? (
                              <Eye size={ICON.xs} />
                            ) : (
                              <EyeOff size={ICON.xs} />
                            )}
                          </button>
                          <button
                            onClick={() => setDeleteFact(fact)}
                            aria-label={t('config.memoryFactsDelete')}
                            data-tip={t('config.memoryFactsDelete')}
                            className="rounded-control border border-edge p-1 text-dim hover:bg-err/10 hover:text-err"
                          >
                            <Trash2 size={ICON.xs} />
                          </button>
                        </div>
                      )}
                    </div>
                    <div className="mt-1 flex flex-wrap items-center gap-2 text-micro text-dim">
                      {fact.kind !== '' && (
                        <span className="font-mono">{fact.kind}</span>
                      )}
                      {fact.updated_at !== undefined && (
                        <span>{formatDateTime(fact.updated_at)}</span>
                      )}
                      {fact.workspace !== undefined &&
                        fact.workspace !== '' &&
                        fact.scope !== 'global' && (
                          <span
                            className="truncate font-mono"
                            data-tip={fact.workspace}
                          >
                            {fact.workspace}
                          </span>
                        )}
                      {source !== undefined && source !== '' && (
                        <button
                          onClick={() =>
                            onOpenConversation?.(source, fact.workspace)
                          }
                          disabled={onOpenConversation === undefined}
                          className="text-accent hover:underline disabled:opacity-40"
                        >
                          {t('config.memoryFactsOpenSource')}
                        </button>
                      )}
                    </div>
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      </div>

      <div className="rounded-card border border-edge bg-panel2 p-3">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex items-center gap-2 text-sm font-medium">
              <RotateCcw size={ICON.sm} className="shrink-0 text-accent" />
              {t('config.reviewTitle')}
            </div>
            <p className="mt-1 text-xs text-faint">{t('config.reviewHint')}</p>
          </div>
          {review && (
            <Badge>
              {t('config.reviewCounts', {
                pending: review.pending,
                accepted: review.accepted,
                discarded: review.discarded,
              })}
            </Badge>
          )}
        </div>

        {review && !review.available && (
          <p className="mt-2.5 rounded-control border border-warn/40 bg-warn/10 px-3 py-2 text-xs text-warn">
            {t('config.reviewUnavailable')}
          </p>
        )}

        {review && (
          <>
            <div className="mt-2.5 grid grid-cols-3 gap-3">
              <label className="flex items-center gap-2 rounded-control border border-edge bg-panel px-2.5 py-2">
                <input
                  type="checkbox"
                  checked={reviewEnabled}
                  onChange={(e) => {
                    setReviewEnabled(e.target.checked);
                    setReviewSaved(false);
                  }}
                  className="accent-accent"
                />
                <span className="min-w-0 text-xs">
                  {t('config.reviewEnabled')}
                </span>
              </label>
              <label className="space-y-1.5">
                <span className="text-xs text-dim">
                  {t('config.reviewEveryTurns')}
                </span>
                <input
                  type="number"
                  min={review.min_every_turns}
                  max={review.max_every_turns}
                  value={everyTurns}
                  onChange={(e) => {
                    setEveryTurns(Number(e.target.value) || 0);
                    setReviewSaved(false);
                  }}
                  className={inputClass}
                />
              </label>
              <label className="space-y-1.5">
                <span className="text-xs text-dim">
                  {t('config.reviewMinToolCalls')}
                </span>
                <input
                  type="number"
                  min={review.min_min_tool_calls}
                  max={review.max_min_tool_calls}
                  value={minToolCalls}
                  onChange={(e) => {
                    setMinToolCalls(Number(e.target.value) || 0);
                    setReviewSaved(false);
                  }}
                  className={inputClass}
                />
              </label>
            </div>
            <p className="mt-2 text-micro text-faint">
              {t('config.reviewLimits', {
                max: review.max_suggestions,
                seconds: review.timeout_seconds,
                failure: review.on_failure
                  ? t('config.reviewOnFailureYes')
                  : t('config.reviewOnFailureNo'),
              })}
            </p>
            <SaveBar
              saved={reviewSaved}
              error={reviewError}
              saving={reviewSaving}
              onSave={() => void saveReview()}
              className="mt-2 rounded-control"
            />
          </>
        )}

        <div className="mt-3 border-t border-edge pt-3">
          {suggestions.length === 0 ? (
            <p className="text-xs text-dim">{t('config.reviewEmpty')}</p>
          ) : (
            <ul className="flex flex-col gap-2">
              {suggestions.map((suggestion) => {
                const source = suggestion.source_conversation;
                return (
                  <li
                    key={suggestion.id}
                    className="rounded-card border border-edge bg-panel p-2.5"
                  >
                    <div className="flex items-start gap-2">
                      <Badge tone={scopeTone(suggestion.scope)}>
                        {scopeLabel(suggestion.scope)}
                      </Badge>
                      <span className="min-w-0 flex-1 text-sm text-fg break-words">
                        {suggestion.text || suggestion.payload || ''}
                      </span>
                    </div>
                    {suggestion.reason !== undefined &&
                      suggestion.reason !== '' && (
                        <p className="mt-1 text-micro text-dim">
                          {t('config.reviewReason', {
                            reason: suggestion.reason,
                          })}
                        </p>
                      )}
                    <div className="mt-1.5 flex flex-wrap items-center gap-2 text-micro text-dim">
                      {suggestion.source_run !== undefined &&
                        suggestion.source_run !== '' && (
                          <span className="font-mono">
                            {suggestion.source_run}
                          </span>
                        )}
                      {source !== undefined && source !== '' && (
                        <button
                          onClick={() =>
                            onOpenConversation?.(
                              source,
                              suggestion.source_workspace,
                            )
                          }
                          disabled={onOpenConversation === undefined}
                          className="text-accent hover:underline disabled:opacity-40"
                        >
                          {t('config.memoryFactsOpenSource')}
                        </button>
                      )}
                      <span className="flex-1" />
                      <Button
                        variant="secondary"
                        size="sm"
                        loading={busyId === suggestion.id}
                        onClick={() => void accept(suggestion)}
                      >
                        {t('config.reviewAccept')}
                      </Button>
                      <Button
                        variant="quiet"
                        size="sm"
                        disabled={busyId === suggestion.id}
                        onClick={() => void discard(suggestion)}
                      >
                        {t('config.reviewDiscard')}
                      </Button>
                    </div>
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      </div>

      <ConfirmDialog
        open={deleteFact !== null}
        tone="danger"
        title={t('config.memoryFactsDeleteConfirm')}
        body={deleteFact?.text}
        confirmLabel={t('config.memoryFactsDelete')}
        onCancel={() => setDeleteFact(null)}
        onConfirm={() => {
          if (deleteFact !== null) void removeFact(deleteFact);
        }}
      />
    </div>
  );
}
