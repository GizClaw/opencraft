import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactNode,
  type UIEvent,
} from 'react';
import {
  Brain,
  Check,
  ChevronDown,
  ChevronRight,
  ClipboardList,
  Loader2,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { PlanPanelState, PlanSnapshot } from '../lib/plan';
import type { SandboxProcess } from '../lib/types';
import { ICON } from './ui/icon';

// ActivityThink is the model's newest reasoning block as the card
// renders it: the block's id (a new id is new activity), its text, and
// whether reasoning is being produced right now.
export interface ActivityThink {
  id: string;
  text: string;
  live: boolean;
}

// MAX_PROCESS_ROWS caps the process list. A conversation can keep a few
// sessions alive at once; the card is a glance at the running ones, and
// a row it does not show is still readable in the transcript.
const MAX_PROCESS_ROWS = 4;

/**
 * ActivityCard is the conversation's live activity overlay, pinned to
 * the top-left of the chat area: the current plan, the model's latest
 * thought, and what this conversation's sandboxed processes are
 * printing.
 * It is an overlay, not a row above the transcript — a docked card
 * reflows with the chat column, so opening the file panel would stretch
 * it and take that height from the conversation for as long as the card
 * lives.
 *
 * The card has no dismissal of its own and no close button: the chat
 * pane mounts it only while the work it reports is real (a turn in
 * flight, or a process still running — see ChatView), and inside that
 * lifetime how long a section keeps its content is the section's own
 * business. Nothing here outlives the activity, so there is nothing to
 * close.
 *
 * What the card does have is one fold, at its top: the header is a
 * button that folds the whole card down to itself, and the dot goes on
 * pulsing there, so a reader who wants the corner back still sees that
 * work is happening. A folded card shrinks to the header's own width
 * instead of keeping the card's column. The fold belongs to the card,
 * the way the sections' folds belong to them: nothing that arrives
 * re-opens it — a plan revision, the next thought and the next process
 * all land in a folded card without a sound — and it lasts as long as
 * the card does, so the next turn opens unfolded. Unfolding brings the
 * body back at the sections' own defaults, because a folded card
 * renders no body for a fold made inside it to survive in.
 *
 * The sections share one rhythm: each is toggleable by hand, and no
 * update re-opens one — a plan revision, a new thought and a new process
 * all arrive without moving a section the reader folded. What still
 * folds itself away is finished content: a completed plan and a thought
 * the model has acted on are long and stale the moment they are done,
 * while process output opens on first sight and stays as the reader
 * leaves it, because that tail is itself the result. The process section
 * is about running work and nothing else: a process that stopped has no
 * activity left to report, so its row — and with it the exit status and
 * the output it printed — leaves at the next read, and the transcript is
 * what keeps the record. The feed goes on reporting a stopped process for
 * a while; that is the feed's read, not what the card shows. Nothing here
 * acts on the sandbox: process output is read-only, and acting on a
 * process stays in the transcript's own session cards.
 */
export function ActivityCard({
  plan,
  think,
  processes,
}: {
  plan: PlanPanelState | null;
  think: ActivityThink | null;
  processes: SandboxProcess[];
}) {
  const { t } = useTranslation();
  // The card's own fold: one control for the whole overlay, at the top
  // (see the component note). Local on purpose — the card lives as long
  // as the activity it reports, and so does this.
  const [folded, setFolded] = useState(false);
  const thinkText = think?.text ?? '';
  // Running processes only: the section reports what is happening, and a
  // stopped process is a record, not activity (see ProcessSection).
  const running = processes.filter((p) => p.running);
  const live =
    Boolean(plan?.live) || Boolean(think?.live) || running.length > 0;
  // A read that holds nothing but stopped processes has nothing to report:
  // the overlay renders nothing instead of an empty card.
  if (plan === null && thinkText === '' && running.length === 0) {
    return null;
  }
  return (
    <div
      data-testid="activity-card"
      data-live={live ? 'true' : 'false'}
      data-folded={folded ? 'true' : 'false'}
      className={`absolute left-4 top-3 z-[var(--oc-z-popover)] flex max-h-[min(50%,32rem)] max-w-[calc(100%-2rem)] flex-col overflow-hidden rounded-card border border-edge bg-panel2 shadow-popover ${
        folded ? 'w-fit' : 'w-96'
      }`}
    >
      <button
        onClick={() => setFolded((v) => !v)}
        aria-expanded={!folded}
        data-testid="activity-card-header"
        className={`flex w-full items-center gap-2 px-3 py-1 text-left transition-colors hover:bg-panel3 ${
          folded ? '' : 'border-b border-edge'
        }`}
      >
        <span
          className={`h-1.5 w-1.5 shrink-0 rounded-full ${
            live ? 'animate-pulse bg-accent' : 'bg-dim'
          }`}
        />
        <span className="min-w-0 flex-1 truncate text-micro font-medium uppercase tracking-wide text-dim">
          {t('chat.activityTitle')}
        </span>
        {folded ? (
          <ChevronRight size={ICON.sm} className="shrink-0 text-dim" />
        ) : (
          <ChevronDown size={ICON.sm} className="shrink-0 text-dim" />
        )}
      </button>
      {!folded && (
        /* The card scrolls as a whole; the two streaming sections cap
           themselves so neither can push the others out of the card. */
        <div className="min-h-0 flex-1 divide-y divide-edge overflow-y-auto">
          {plan && <PlanSection plan={plan.plan} live={plan.live} />}
          {thinkText !== '' && (
            <ThinkSection
              id={think?.id ?? ''}
              text={thinkText}
              live={Boolean(think?.live)}
            />
          )}
          {running.length > 0 && <ProcessSection processes={running} />}
        </div>
      )}
    </div>
  );
}

// SectionHeader is the row every section shares: a leading glyph, a
// title, an optional trailing read-out, and the expand chevron.
function SectionHeader({
  glyph,
  title,
  meta,
  open,
  onToggle,
  testID,
}: {
  glyph: ReactNode;
  title: string;
  meta?: string;
  open: boolean;
  onToggle: () => void;
  testID: string;
}) {
  return (
    <button
      onClick={onToggle}
      aria-expanded={open}
      data-testid={testID}
      className="flex w-full items-center gap-2 px-3 py-1.5 text-xs transition-colors hover:bg-panel3"
    >
      <span className="flex w-4 shrink-0 justify-center">{glyph}</span>
      <span className="min-w-0 flex-1 truncate text-left font-medium text-fg">
        {title}
      </span>
      {meta !== undefined && (
        <span className="shrink-0 tabular-nums text-dim">{meta}</span>
      )}
      {open ? (
        <ChevronDown size={ICON.sm} className="shrink-0 text-dim" />
      ) : (
        <ChevronRight size={ICON.sm} className="shrink-0 text-dim" />
      )}
    </button>
  );
}

// PlanSection is the conversation's current plan: a checklist that
// starts expanded while work is left and folds itself away once every
// step is done. A plan revision never re-opens it — the checklist is
// content, and its fold state belongs to the reader. An empty plan is
// the "loading" placeholder the update_plan call resolves into.
function PlanSection({ plan, live }: { plan: PlanSnapshot; live: boolean }) {
  const { t } = useTranslation();
  const done = plan.items.filter((s) => s.status === 'completed').length;
  const complete = plan.items.length > 0 && done === plan.items.length;
  const [open, setOpen] = useState(!complete);
  useEffect(() => {
    if (complete) setOpen(false);
  }, [complete]);
  return (
    <div data-testid="plan-section">
      <SectionHeader
        testID="plan-section-header"
        glyph={
          live ? (
            <Loader2 size={ICON.sm} className="animate-spin text-accent" />
          ) : (
            <ClipboardList size={ICON.sm} className="text-ok" />
          )
        }
        title={t('chat.planTitle')}
        meta={
          plan.items.length > 0 ? `${done}/${plan.items.length}` : undefined
        }
        open={open}
        onToggle={() => setOpen((v) => !v)}
      />
      {open && (
        <div className="space-y-1.5 px-3 pb-2 pt-0.5">
          {plan.items.length === 0 ? (
            <div className="flex items-center gap-2 text-xs text-dim">
              <Loader2 size={ICON.xs} className="animate-spin text-accent" />
              {t('chat.planLoading')}
            </div>
          ) : (
            <>
              {plan.explanation && (
                <div className="whitespace-pre-wrap text-xs text-dim">
                  {plan.explanation}
                </div>
              )}
              {plan.items.map((item, idx) => {
                const status = item.status ?? 'pending';
                const completed = status === 'completed';
                const inProgress = status === 'in_progress';
                return (
                  <div key={idx} className="flex items-start gap-2 text-xs">
                    {inProgress ? (
                      <Loader2
                        size={ICON.xs}
                        className="mt-0.5 shrink-0 animate-spin text-accent"
                      />
                    ) : completed ? (
                      <Check
                        size={ICON.xs}
                        className="mt-0.5 shrink-0 text-ok"
                      />
                    ) : (
                      <span className="mt-0.5 h-3 w-3 shrink-0 rounded-full border border-dim" />
                    )}
                    <span
                      className={`min-w-0 ${
                        completed ? 'text-dim line-through' : 'text-fg'
                      }`}
                    >
                      {item.step}
                    </span>
                  </div>
                );
              })}
            </>
          )}
        </div>
      )}
    </div>
  );
}

// ThinkSection is the model's latest reasoning block. Reasoning is the
// longest text the card holds and stops being the interesting part the
// moment the model acts on it, so the section opens with the live block
// and folds away to its header once the thought is done — while keeping
// that last block, which is what the card is for. A block that streams
// in later does not re-open a folded section.
function ThinkSection({
  id,
  text,
  live,
}: {
  id: string;
  text: string;
  live: boolean;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(live);
  useEffect(() => {
    if (!live) setOpen(false);
  }, [live]);
  // The revision tracks the stream (its length) as well as the block, so
  // every token re-pins the block while it is live.
  const tail = useStickyTail(`${id}|${open ? '1' : '0'}|${text.length}`);
  return (
    <div data-testid="think-section">
      <SectionHeader
        testID="think-section-header"
        glyph={
          live ? (
            <Loader2 size={ICON.sm} className="animate-spin text-accent" />
          ) : (
            <Brain size={ICON.sm} className="text-subagent" />
          )
        }
        title={live ? t('chat.thinking') : t('chat.thinkTitle')}
        open={open}
        onToggle={() => setOpen((v) => !v)}
      />
      {open && (
        <div
          ref={tail.attach}
          onScroll={tail.onScroll}
          data-testid="think-body"
          className="max-h-24 overflow-y-auto whitespace-pre-wrap break-words px-3 pb-2 pt-0.5 text-micro leading-snug text-dim"
        >
          {text}
        </div>
      )}
    </div>
  );
}

// ProcessSection lists the processes this conversation is running right
// now (newest first) with the output tail of the selected one. A process
// that stops leaves the list at the next read: the card reports live
// work, and how a command ended plus everything it printed are the
// transcript's record, not activity. The tail is read-only on purpose:
// stopping a process is a tool decision that goes through the model, and
// the card does not open a side door into the sandbox.
function ProcessSection({ processes }: { processes: SandboxProcess[] }) {
  const { t } = useTranslation();
  // Unlike the other sections this one opens by default and stays as the
  // reader leaves it: the tail is the result the user came for — while a
  // process the conversation starts later must not pop the section back
  // open after the reader folded it.
  const [open, setOpen] = useState(true);
  const [selected, setSelected] = useState('');
  // Newest first: the process the conversation just started is the one
  // the card is about.
  const rows = [...processes].reverse();
  // A selection that stopped is gone from rows, so the newest process
  // takes the tail over.
  const current = rows.find((p) => p.process_id === selected) ?? rows[0];
  const shown = rows.slice(0, MAX_PROCESS_ROWS);
  // The tail follows the selection, so a selected row stays in the list
  // even when it is older than the rows the cap shows.
  if (current && !shown.some((p) => p.process_id === current.process_id)) {
    shown.push(current);
  }
  const hidden = rows.length - shown.length;
  const tail = useStickyTail(
    `${current?.process_id ?? ''}|${current?.seq ?? 0}|${open ? '1' : '0'}`,
  );

  if (!current) return null;
  const empty = current.tail === '';
  return (
    <div data-testid="process-section">
      <SectionHeader
        testID="process-section-header"
        glyph={<Loader2 size={ICON.sm} className="animate-spin text-accent" />}
        title={t('chat.processTitle')}
        meta={processes.length > 1 ? String(processes.length) : undefined}
        open={open}
        onToggle={() => setOpen((v) => !v)}
      />
      {open && (
        <>
          <div className="pb-1">
            {shown.map((p) => {
              const isCurrent = p.process_id === current.process_id;
              return (
                <button
                  key={p.process_id}
                  onClick={() => setSelected(p.process_id)}
                  data-testid="process-row"
                  data-process-id={p.process_id}
                  className={`flex w-full items-center gap-1.5 px-3 py-0.5 text-left text-micro transition-colors ${
                    isCurrent ? 'bg-panel3' : 'hover:bg-panel3'
                  }`}
                >
                  <span className="h-1.5 w-1.5 shrink-0 animate-pulse rounded-full bg-accent" />
                  <span className="min-w-0 flex-1 truncate font-mono text-fg">
                    {p.argv.join(' ')}
                  </span>
                  <span className="shrink-0 text-dim">
                    {t('chat.processRunning')}
                  </span>
                </button>
              );
            })}
            {hidden > 0 && (
              <div className="px-3 pt-0.5 text-micro text-dim">
                {t('chat.processOlder', { count: hidden })}
              </div>
            )}
          </div>
          <pre
            ref={tail.attach}
            onScroll={tail.onScroll}
            data-testid="process-tail"
            className={`max-h-40 overflow-y-auto whitespace-pre-wrap break-all border-t border-edge/60 px-3 py-1.5 font-mono text-micro ${
              empty ? 'text-dim' : 'text-fg'
            }`}
          >
            {empty
              ? t('chat.processWaiting')
              : `${current.truncated ? `${t('chat.processTruncated')}\n` : ''}${
                  current.tail
                }`}
          </pre>
        </>
      )}
    </div>
  );
}

// useStickyTail keeps a scrollable block pinned to its newest line: it
// follows the stream while the reader is at the bottom and stops
// following the moment they scroll up, because someone reading back
// through previous lines should not be pulled down by the next token. A
// block that mounts or is reopened starts pinned.
function useStickyTail<T extends HTMLElement>(revision: string) {
  const node = useRef<T | null>(null);
  const stick = useRef(true);
  const attach = useCallback((el: T | null) => {
    node.current = el;
    if (el) stick.current = true;
  }, []);
  useEffect(() => {
    const el = node.current;
    if (!el || !stick.current) return;
    el.scrollTop = el.scrollHeight;
  }, [revision]);
  return {
    attach,
    onScroll: (event: UIEvent<T>) => {
      const el = event.currentTarget;
      stick.current = el.scrollHeight - el.scrollTop - el.clientHeight <= 8;
    },
  };
}
