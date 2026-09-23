import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Bookmark,
  Check,
  Database,
  Eye,
  EyeOff,
  Pencil,
  Plus,
  Sparkles,
  Trash2,
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
import { MenuSelect } from './MenuSelect';
import { Badge } from './ui/Badge';
import { Button, IconButton } from './ui/Button';
import { ConfirmDialog } from './ui/ConfirmDialog';
import { EmptyState } from './ui/EmptyState';
import { ICON } from './ui/icon';
import { Input } from './ui/Input';
import { SaveBar } from './ui/SaveBar';
import { NumberSetting, SettingRow, ToggleSetting } from './ui/SettingRow';

// The long-term memory card of the Memory tab: the user-level facts the
// host injects into every turn, plus the queue of suggestions the
// post-turn review proposed and is waiting for a verdict on.
//
// The two lists share one component on purpose: accepting a suggestion is
// the same write as typing a fact by hand, and seeing them next to each
// other is what makes that obvious. Facts are always written through the
// host's store — this card never composes its own SQL — so the limits,
// the dedupe and the provenance are the same everywhere.
//
// Each list is a card: header, rows, the knobs that decide how the list
// is used, then the save bar. A row leads with the text it carries and
// keeps the chips and the actions on the line below, so a long fact
// cannot push its own scope badge out of the column the eye is
// following.

function scopeTone(scope: string | undefined): 'accent' | 'neutral' {
  return scope === 'global' ? 'accent' : 'neutral';
}

// The mid-dot between two plain metadata items in a row's meta line:
// without it "preference updated 09/12/2026" reads as one phrase.
function MetaDot() {
  return (
    <span aria-hidden className="text-faint">
      ·
    </span>
  );
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

  // A fact that came from another project is the one case where its
  // workspace is news; the current one is the same path on every row, so
  // it would be a column of identical strings.
  const otherWorkspace = (workspace: string | undefined) =>
    workspace !== undefined &&
    workspace !== '' &&
    workspace !== state?.workspace
      ? workspace
      : '';

  const range = (min: number, max: number, value: number) =>
    t('config.settingRange', { min, max, value });

  // What the two save bars say on their left. A bar belongs to the drafts
  // above it, so it claims there are unsaved changes only when those
  // drafts differ from what the runtime is using.
  const settingsDirty =
    state !== null &&
    (enabled !== state.enabled ||
      maxItems !== state.inject_max_items ||
      maxChars !== state.inject_max_chars);
  const cadenceDirty =
    review !== null &&
    (reviewEnabled !== review.enabled ||
      everyTurns !== review.every_turns ||
      minToolCalls !== review.min_tool_calls);

  return (
    <div className="space-y-4">
      {/* What the agent remembers, and the knobs that decide how much of
          it enters a turn. The list comes first because it is the reason
          the card exists; the count in the header is the answer to "how
          full is it", which the rows themselves cannot give. */}
      <section className="rounded-card border border-edge bg-panel2">
        <div className="flex items-start justify-between gap-4 px-4 py-3.5">
          <div className="min-w-0">
            <h3 className="flex items-center gap-2 text-title font-semibold">
              <Database size={ICON.md} className="shrink-0 text-accent" />
              {t('config.memoryFactsTitle')}
            </h3>
            <p className="mt-1 max-w-2xl text-xs text-dim">
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
          <p className="mx-4 mb-3 rounded-control border border-err/40 bg-err/10 px-3 py-2 text-xs text-err break-words">
            {loadError}
          </p>
        )}

        {state && !state.available && (
          <p className="mx-4 mb-3 rounded-control border border-warn/40 bg-warn/10 px-3 py-2 text-xs text-warn">
            {t('config.memoryFactsUnavailable')}
          </p>
        )}

        <div className="space-y-2 border-t border-edge px-4 py-3.5">
          {/* One frame rather than three: the sentence, the scope picker
              and the button sit on one line inside a single border, so
              the row reads as "write a fact" instead of as three
              unrelated controls that happen to be adjacent. The scope
              cell is the app's own dropdown (borderless, sized to its
              label) rather than a native select, which would drop the
              platform's arrow and its own popup list into the middle of
              the frame. */}
          <div className="flex items-center gap-1.5 rounded-control border border-edge bg-panel pr-1 pl-2.5 transition-colors focus-within:border-accent">
            <input
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') void addFact();
              }}
              placeholder={t('config.memoryFactsAddPlaceholder')}
              aria-label={t('config.memoryFactsAddPlaceholder')}
              className="min-w-0 flex-1 bg-transparent py-1.5 text-xs text-fg outline-none placeholder:text-faint"
            />
            <span aria-hidden className="h-4 w-px shrink-0 bg-edge" />
            <MenuSelect
              variant="inline"
              label={t('config.memoryFactsScopeLabel')}
              value={draftScope}
              options={[
                { value: '', label: t('config.memoryFactsScopeAuto') },
                {
                  value: 'workspace',
                  label: t('config.memoryFactsScopeWorkspace'),
                },
                { value: 'global', label: t('config.memoryFactsScopeGlobal') },
              ]}
              onChange={setDraftScope}
            />
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
            <EmptyState
              size="sm"
              icon={Bookmark}
              title={t('config.memoryFactsEmpty')}
              hint={t('config.memoryFactsEmptyHint')}
            />
          ) : (
            <ul className="flex flex-col gap-2">
              {facts.map((fact) => {
                const source = fact.source_conversation;
                const updated =
                  fact.updated_at === undefined
                    ? ''
                    : formatDateTime(fact.updated_at);
                const elsewhere = otherWorkspace(fact.workspace);
                return (
                  <li
                    key={fact.id}
                    className="[content-visibility:auto] [contain-intrinsic-size:auto_5rem] rounded-card border border-edge bg-panel p-3 transition-colors hover:border-accent/40"
                  >
                    {editingId === fact.id ? (
                      <div className="flex items-center gap-2">
                        <Input
                          value={editText}
                          onChange={(e) => setEditText(e.target.value)}
                          onKeyDown={(e) => {
                            if (e.key === 'Enter') void saveEdit(fact.id);
                          }}
                          aria-label={t('config.memoryFactsEdit')}
                          size="sm"
                          autoFocus
                          className="flex-1"
                        />
                        <Button
                          variant="secondary"
                          size="sm"
                          loading={busyId === fact.id}
                          onClick={() => void saveEdit(fact.id)}
                        >
                          <Check size={ICON.xs} />
                          {t('config.memoryFactsSaveEdit')}
                        </Button>
                        <Button
                          variant="quiet"
                          size="sm"
                          onClick={() => setEditingId('')}
                        >
                          {t('config.memoryFactsCancelEdit')}
                        </Button>
                      </div>
                    ) : (
                      <>
                        <div className="flex items-start gap-3">
                          <span
                            className={`min-w-0 flex-1 text-sm break-words ${
                              fact.stale ? 'text-dim' : 'text-fg'
                            }`}
                          >
                            {fact.text}
                          </span>
                          <div className="flex shrink-0 items-center gap-0.5">
                            <IconButton
                              label={t('config.memoryFactsEdit')}
                              size="sm"
                              onClick={() => {
                                setEditingId(fact.id);
                                setEditText(fact.text);
                              }}
                            >
                              <Pencil size={ICON.xs} />
                            </IconButton>
                            <IconButton
                              label={
                                fact.stale
                                  ? t('config.memoryFactsUnstale')
                                  : t('config.memoryFactsMarkStale')
                              }
                              size="sm"
                              disabled={busyId === fact.id}
                              onClick={() => void toggleStale(fact)}
                            >
                              {fact.stale ? (
                                <Eye size={ICON.xs} />
                              ) : (
                                <EyeOff size={ICON.xs} />
                              )}
                            </IconButton>
                            <IconButton
                              label={t('config.memoryFactsDelete')}
                              size="sm"
                              onClick={() => setDeleteFact(fact)}
                            >
                              <Trash2 size={ICON.xs} />
                            </IconButton>
                          </div>
                        </div>
                        <div className="mt-2 flex flex-wrap items-center gap-x-1.5 gap-y-1 text-micro text-faint">
                          <Badge tone={scopeTone(fact.scope)}>
                            {scopeLabel(fact.scope)}
                          </Badge>
                          {fact.stale && (
                            <Badge tone="warn">
                              {t('config.memoryFactsStale')}
                            </Badge>
                          )}
                          {fact.kind !== '' && (
                            <>
                              <MetaDot />
                              <span>{fact.kind}</span>
                            </>
                          )}
                          {updated !== '' && (
                            <>
                              <MetaDot />
                              <span>
                                {t('config.memoryFactsUpdated', {
                                  date: updated,
                                })}
                              </span>
                            </>
                          )}
                          {elsewhere !== '' && (
                            <>
                              <MetaDot />
                              <span
                                className="max-w-48 truncate font-mono"
                                data-tip={elsewhere}
                              >
                                {elsewhere}
                              </span>
                            </>
                          )}
                          {source !== undefined && source !== '' && (
                            <>
                              <MetaDot />
                              <button
                                onClick={() =>
                                  onOpenConversation?.(source, fact.workspace)
                                }
                                disabled={onOpenConversation === undefined}
                                className="text-accent hover:underline disabled:opacity-40"
                              >
                                {t('config.memoryFactsOpenSource')}
                              </button>
                            </>
                          )}
                        </div>
                      </>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
        </div>

        {state && (
          <div className="space-y-3 border-t border-edge px-4 py-3.5">
            <h4 className="text-micro font-medium tracking-wide text-faint uppercase">
              {t('config.memoryFactsInjection')}
            </h4>
            <ToggleSetting
              label={t('config.memoryFactsEnabled')}
              hint={t('config.memoryFactsEnabledHint')}
              checked={enabled}
              onChange={(checked) => {
                setEnabled(checked);
                setSaved(false);
              }}
            />
            <SettingRow
              label={t('config.memoryFactsItems')}
              hint={range(
                state.min_inject_max_items,
                state.max_inject_max_items,
                state.default_inject_max_items,
              )}
            >
              <NumberSetting
                label={t('config.memoryFactsItems')}
                unit={t('config.unitFacts')}
                min={state.min_inject_max_items}
                max={state.max_inject_max_items}
                value={maxItems}
                onChange={(value) => {
                  setMaxItems(value);
                  setSaved(false);
                }}
              />
            </SettingRow>
            <SettingRow
              label={t('config.memoryFactsChars')}
              hint={range(
                state.min_inject_max_chars,
                state.max_inject_max_chars,
                state.default_inject_max_chars,
              )}
            >
              <NumberSetting
                label={t('config.memoryFactsChars')}
                unit={t('config.unitBytes')}
                min={state.min_inject_max_chars}
                max={state.max_inject_max_chars}
                step={256}
                width="w-24"
                value={maxChars}
                onChange={(value) => {
                  setMaxChars(value);
                  setSaved(false);
                }}
              />
            </SettingRow>
          </div>
        )}

        {state && (
          <SaveBar
            saved={saved}
            error={saveError}
            saving={saving}
            onSave={() => void saveSettings()}
          >
            {settingsDirty && (
              <span className="text-dim">{t('config.unsaved')}</span>
            )}
          </SaveBar>
        )}
      </section>

      {/* The queue the post-turn review fills. Same shape as the facts
          above on purpose: accepting a suggestion is the same write as
          typing a fact by hand, and the row says where the candidate came
          from before it says how to answer it. */}
      <section
        id="settings-memory-suggestions"
        className="scroll-mt-4 rounded-card border border-edge bg-panel2"
      >
        <div className="flex items-start justify-between gap-4 px-4 py-3.5">
          <div className="min-w-0">
            <h3 className="flex items-center gap-2 text-title font-semibold">
              <Sparkles size={ICON.md} className="shrink-0 text-accent" />
              {t('config.reviewTitle')}
            </h3>
            <p className="mt-1 max-w-2xl text-xs text-dim">
              {t('config.reviewHint')}
            </p>
          </div>
          {review && (
            <div className="flex shrink-0 items-center gap-2">
              {/* What is waiting is the number that decides whether the
                  card needs attention, so it is the one that gets the
                  chip; the history behind it is a caption. The full
                  breakdown stays on the tooltip. */}
              <span
                data-tip={t('config.reviewCounts', {
                  pending: review.pending,
                  accepted: review.accepted,
                  discarded: review.discarded,
                })}
              >
                <Badge tone={review.pending > 0 ? 'accent' : 'neutral'}>
                  {t('config.reviewPending', { pending: review.pending })}
                </Badge>
              </span>
              <span className="text-micro text-faint">
                {t('config.reviewHistory', {
                  accepted: review.accepted,
                  discarded: review.discarded,
                })}
              </span>
            </div>
          )}
        </div>

        {review && !review.available && (
          <p className="mx-4 mb-3 rounded-control border border-warn/40 bg-warn/10 px-3 py-2 text-xs text-warn">
            {t('config.reviewUnavailable')}
          </p>
        )}

        <div className="border-t border-edge px-4 py-3.5">
          {suggestions.length === 0 ? (
            <EmptyState
              size="sm"
              icon={Sparkles}
              title={t('config.reviewEmpty')}
              hint={t('config.reviewEmptyHint')}
            />
          ) : (
            <ul className="flex flex-col gap-2">
              {suggestions.map((suggestion) => {
                const source = suggestion.source_conversation;
                const kind = suggestion.candidate_kind ?? suggestion.kind;
                return (
                  <li
                    key={suggestion.id}
                    className="[content-visibility:auto] [contain-intrinsic-size:auto_6rem] rounded-card border border-edge bg-panel p-3 transition-colors hover:border-accent/40"
                  >
                    <div className="flex flex-wrap items-start gap-x-3 gap-y-2">
                      <div className="min-w-0 flex-1">
                        <div className="text-sm text-fg break-words">
                          {suggestion.text || suggestion.payload || ''}
                        </div>
                        {suggestion.reason !== undefined &&
                          suggestion.reason !== '' && (
                            <p className="mt-1 text-micro text-dim">
                              {t('config.reviewReason', {
                                reason: suggestion.reason,
                              })}
                            </p>
                          )}
                        <div className="mt-2 flex flex-wrap items-center gap-x-1.5 gap-y-1 text-micro text-faint">
                          <Badge tone={scopeTone(suggestion.scope)}>
                            {scopeLabel(suggestion.scope)}
                          </Badge>
                          {kind !== undefined && kind !== '' && (
                            <>
                              <MetaDot />
                              <span>{kind}</span>
                            </>
                          )}
                          {suggestion.source_run !== undefined &&
                            suggestion.source_run !== '' && (
                              <>
                                <MetaDot />
                                <span className="font-mono">
                                  {suggestion.source_run}
                                </span>
                              </>
                            )}
                          {source !== undefined && source !== '' && (
                            <>
                              <MetaDot />
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
                            </>
                          )}
                        </div>
                      </div>
                      {/* The verdict is the row's one action: accept is
                          the accent button, discard is the quiet one
                          beside it, and both sit on the text's first
                          line rather than floating in the row's middle. */}
                      <div className="flex shrink-0 items-start gap-1.5">
                        <Button
                          variant="primary"
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
                    </div>
                  </li>
                );
              })}
            </ul>
          )}
        </div>

        {review && (
          <div className="space-y-3 border-t border-edge px-4 py-3.5">
            <h4 className="text-micro font-medium tracking-wide text-faint uppercase">
              {t('config.reviewCadence')}
            </h4>
            <ToggleSetting
              label={t('config.reviewEnabled')}
              hint={t('config.reviewEnabledHint')}
              checked={reviewEnabled}
              onChange={(checked) => {
                setReviewEnabled(checked);
                setReviewSaved(false);
              }}
            />
            <SettingRow
              label={t('config.reviewEveryTurns')}
              hint={range(
                review.min_every_turns,
                review.max_every_turns,
                review.default_every_turns,
              )}
            >
              <NumberSetting
                label={t('config.reviewEveryTurns')}
                unit={t('config.unitTurns')}
                min={review.min_every_turns}
                max={review.max_every_turns}
                value={everyTurns}
                onChange={(value) => {
                  setEveryTurns(value);
                  setReviewSaved(false);
                }}
              />
            </SettingRow>
            <SettingRow
              label={t('config.reviewMinToolCalls')}
              hint={range(
                review.min_min_tool_calls,
                review.max_min_tool_calls,
                review.default_min_tool_calls,
              )}
            >
              <NumberSetting
                label={t('config.reviewMinToolCalls')}
                unit={t('config.unitCalls')}
                min={review.min_min_tool_calls}
                max={review.max_min_tool_calls}
                value={minToolCalls}
                onChange={(value) => {
                  setMinToolCalls(value);
                  setReviewSaved(false);
                }}
              />
            </SettingRow>
            <p className="text-micro text-faint">
              {t('config.reviewLimits', {
                max: review.max_suggestions,
                seconds: review.timeout_seconds,
                failure: review.on_failure
                  ? t('config.reviewOnFailureYes')
                  : t('config.reviewOnFailureNo'),
              })}
            </p>
          </div>
        )}

        {review && (
          <SaveBar
            saved={reviewSaved}
            error={reviewError}
            saving={reviewSaving}
            onSave={() => void saveReview()}
          >
            {cadenceDirty && (
              <span className="text-dim">{t('config.unsaved')}</span>
            )}
          </SaveBar>
        )}
      </section>

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
