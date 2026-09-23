import { useCallback, useEffect, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Bookmark,
  Check,
  Database,
  Eye,
  EyeOff,
  Pencil,
  Plus,
  Search,
  Sparkles,
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
import { MenuSelect } from './MenuSelect';
import { Badge } from './ui/Badge';
import { Button, IconButton } from './ui/Button';
import { ConfirmDialog } from './ui/ConfirmDialog';
import { EmptyState } from './ui/EmptyState';
import { ICON } from './ui/icon';
import { SaveBar } from './ui/SaveBar';
import { NumberSetting, SettingRow, ToggleSetting } from './ui/SettingRow';
import { Textarea } from './ui/Textarea';

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

// The row's meta line: whatever a row carries, in one wrapping line with
// a mid-dot between neighbours, so no item has to know whether it is the
// first (which is what the hand-written fragments were for).
function MetaLine({ items }: { items: ReactNode[] }) {
  const kept = items.filter(
    (item) => item !== '' && item !== undefined && item !== null,
  );
  return (
    <div className="mt-2 flex flex-wrap items-center gap-x-1.5 gap-y-1 text-micro text-faint">
      {kept.map((item, index) => (
        <span key={index} className="flex min-w-0 items-center gap-1.5">
          {index > 0 && <MetaDot />}
          {item}
        </span>
      ))}
    </div>
  );
}

// The store has two axes — which project a fact belongs to, and whether
// it still rides along — and one flat list hides both: at a hundred facts
// the eye cannot tell this project's rows from another's, and a stopped
// one looks like a live one until its meta line is read. The list is
// four piles in the order a reader asks about them, and the chips count
// them.
type FactPile = 'project' | 'other' | 'global' | 'stale';

const PILE_ORDER: FactPile[] = ['project', 'other', 'global', 'stale'];

const PILE_KEY: Record<FactPile, string> = {
  project: 'config.memoryFactsPileProject',
  other: 'config.memoryFactsPileOther',
  global: 'config.memoryFactsPileGlobal',
  stale: 'config.memoryFactsPileStale',
};

// Below this the list fits on one screen and a filter row would be a
// control with nothing to do.
const FILTER_FROM = 6;

// A pile heading: the name, a rule, and the count. The rule is what
// makes it read as a divider between two groups rather than as one more
// line of text.
function PileHeading({ label, count }: { label: string; count: number }) {
  return (
    <div className="flex items-center gap-2">
      <span className="text-label font-medium text-dim">{label}</span>
      <span aria-hidden className="h-px flex-1 bg-edge" />
      <span className="text-micro tabular-nums text-faint">{count}</span>
    </div>
  );
}

// The sub-heading of a card's second half ("each turn", "when it runs"):
// a label and its hint, the shape a SettingRow leads with. It replaces
// the uppercase micro-letters the two used to wear — a device that says
// nothing in Chinese and that no other card uses.
function SubHeading({ label, hint }: { label: string; hint?: string }) {
  return (
    <div className="min-w-0">
      <h4 className="text-xs font-semibold text-fg">{label}</h4>
      {hint !== undefined && hint !== '' && (
        <p className="mt-0.5 text-label text-dim">{hint}</p>
      )}
    </div>
  );
}

function PileChip({
  active,
  tip,
  label,
  count,
  onClick,
}: {
  active: boolean;
  tip: string;
  label: string;
  count: number;
  onClick: () => void;
}) {
  // The count is part of the button's name: without spelling it out, the
  // two children run together ("All8") in the accessible name too.
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      aria-label={`${label} ${count}`}
      data-tip={tip}
      className={`flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-micro transition-colors ${
        active
          ? 'border-accent/40 bg-accent/10 text-accent'
          : 'border-edge bg-panel text-dim hover:border-accent/40 hover:text-fg'
      }`}
    >
      {label}
      <span className="tabular-nums opacity-70">{count}</span>
    </button>
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
  // The list's two view controls: which pile is shown, and the query that
  // narrows the pool before the chips count it.
  const [pile, setPile] = useState<FactPile | 'all'>('all');
  const [query, setQuery] = useState('');

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

  // Which pile a fact belongs to. Stale wins over the scope axes: a
  // stopped fact is news whichever project it came from, and it is the
  // one pile a reader goes looking for. "Other projects" is the pile the
  // old flat list could not show: those facts are in the store, they are
  // simply not in this project's prompts.
  const pileOf = (fact: MemoryFact): FactPile => {
    if (fact.stale) return 'stale';
    if (fact.scope === 'global') return 'global';
    return otherWorkspace(fact.workspace) === '' ? 'project' : 'other';
  };
  const needle = query.trim().toLowerCase();
  // The search narrows the pool first, so a chip counts the rows it
  // would actually show rather than the whole store.
  const pool =
    needle === ''
      ? facts
      : facts.filter((fact) => fact.text.toLowerCase().includes(needle));
  const countIn = (id: FactPile) =>
    pool.filter((fact) => pileOf(fact) === id).length;
  const piles = PILE_ORDER.filter((id) => pile === 'all' || id === pile)
    .map((id) => ({ id, rows: pool.filter((fact) => pileOf(fact) === id) }))
    .filter((group) => group.rows.length > 0);

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
            <div className="space-y-3">
              {facts.length > FILTER_FROM && (
                <div className="flex flex-wrap items-center gap-1.5">
                  <PileChip
                    active={pile === 'all'}
                    tip={t('config.memoryFactsPileAllTip')}
                    label={t('config.memoryFactsPileAll')}
                    count={pool.length}
                    onClick={() => setPile('all')}
                  />
                  {PILE_ORDER.filter((id) => countIn(id) > 0).map((id) => (
                    <PileChip
                      key={id}
                      active={pile === id}
                      tip={t(`${PILE_KEY[id]}Tip`)}
                      label={t(PILE_KEY[id])}
                      count={countIn(id)}
                      onClick={() => setPile(id)}
                    />
                  ))}
                  {/* The field takes whatever the chips leave on this
                      line and stops at 52; when the chips wrap it, the
                      search starts the next line instead of hanging off
                      the right edge on its own. */}
                  <div className="flex h-6 min-w-40 flex-1 items-center gap-1.5 rounded-control border border-edge bg-panel px-2 focus-within:border-accent sm:max-w-52">
                    <Search size={ICON.xs} className="shrink-0 text-dim" />
                    <input
                      value={query}
                      onChange={(e) => setQuery(e.target.value)}
                      aria-label={t('config.memoryFactsSearch')}
                      placeholder={t('config.memoryFactsSearch')}
                      className="h-6 min-w-0 flex-1 bg-transparent text-xs outline-none"
                    />
                    {query !== '' && (
                      <button
                        type="button"
                        aria-label={t('config.memoryFactsSearchClear')}
                        onClick={() => setQuery('')}
                        className="shrink-0 text-dim hover:text-fg"
                      >
                        <X size={ICON.xs} />
                      </button>
                    )}
                  </div>
                </div>
              )}
              {piles.length === 0 ? (
                <p className="text-xs text-dim">
                  {t('config.memoryFactsNoMatch')}
                </p>
              ) : (
                piles.map((group) => (
                  <div key={group.id} className="space-y-2">
                    <PileHeading
                      label={t(PILE_KEY[group.id])}
                      count={group.rows.length}
                    />
                    <ul className="flex flex-col gap-2">
                      {group.rows.map((fact) => {
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
                              <div className="space-y-2">
                                {/* A fact is a sentence and the store
                                    takes up to 4 KB of one, so the field
                                    it is edited in has to show more than
                                    its first line. Enter commits (the
                                    key that adds one above), Shift+Enter
                                    breaks the line, Escape drops the
                                    edit. */}
                                <Textarea
                                  value={editText}
                                  onChange={(e) => setEditText(e.target.value)}
                                  onKeyDown={(e) => {
                                    if (e.key === 'Enter' && !e.shiftKey) {
                                      e.preventDefault();
                                      void saveEdit(fact.id);
                                    }
                                    if (e.key === 'Escape') setEditingId('');
                                  }}
                                  aria-label={t('config.memoryFactsEdit')}
                                  surface="raised"
                                  rows={2}
                                  autoFocus
                                />
                                <div className="flex items-center justify-end gap-2">
                                  <Button
                                    variant="quiet"
                                    size="sm"
                                    onClick={() => setEditingId('')}
                                  >
                                    {t('config.memoryFactsCancelEdit')}
                                  </Button>
                                  <Button
                                    variant="primary"
                                    size="sm"
                                    loading={busyId === fact.id}
                                    onClick={() => void saveEdit(fact.id)}
                                  >
                                    <Check size={ICON.xs} />
                                    {t('config.memoryFactsSaveEdit')}
                                  </Button>
                                </div>
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
                                {/* The scope is the pile's job now, so the row
                            carries only what is its own: its kind, when
                            it moved, and where it came from. */}
                                <MetaLine
                                  items={[
                                    fact.kind,
                                    updated === ''
                                      ? ''
                                      : t('config.memoryFactsUpdated', {
                                          date: updated,
                                        }),
                                    elsewhere === '' ? (
                                      ''
                                    ) : (
                                      <span
                                        className="max-w-48 truncate font-mono"
                                        data-tip={elsewhere}
                                      >
                                        {elsewhere}
                                      </span>
                                    ),
                                    source === undefined || source === '' ? (
                                      ''
                                    ) : (
                                      <button
                                        onClick={() =>
                                          onOpenConversation?.(
                                            source,
                                            fact.workspace,
                                          )
                                        }
                                        disabled={
                                          onOpenConversation === undefined
                                        }
                                        className="text-accent hover:underline disabled:opacity-40"
                                      >
                                        {t('config.memoryFactsOpenSource')}
                                      </button>
                                    ),
                                  ]}
                                />
                              </>
                            )}
                          </li>
                        );
                      })}
                    </ul>
                  </div>
                ))
              )}
            </div>
          )}
        </div>

        {state && (
          <div className="space-y-3 border-t border-edge px-4 py-3.5">
            <SubHeading
              label={t('config.memoryFactsEachTurn')}
              hint={t('config.memoryFactsEachTurnHint')}
            />
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
                        <MetaLine
                          items={[
                            <Badge tone={scopeTone(suggestion.scope)}>
                              {scopeLabel(suggestion.scope)}
                            </Badge>,
                            kind ?? '',
                            suggestion.source_run === undefined ||
                            suggestion.source_run === '' ? (
                              ''
                            ) : (
                              <span className="font-mono">
                                {suggestion.source_run}
                              </span>
                            ),
                            source === undefined || source === '' ? (
                              ''
                            ) : (
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
                            ),
                          ]}
                        />
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
            <SubHeading
              label={t('config.reviewWhen')}
              hint={t('config.reviewWhenHint')}
            />
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
