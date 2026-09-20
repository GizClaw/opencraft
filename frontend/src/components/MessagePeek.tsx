import { memo, useEffect, useMemo, useRef, useState } from 'react';
import { Loader2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useStore } from '../lib/store';
import { Markdown, type MarkdownLinkHandler } from './Markdown';
import { ICON } from './ui/icon';

// With a normal number of turns the ticks form a compact, vertically
// centered ruler: every turn is one short line and the gap between
// lines stays small. Long sessions keep exactly that face: the ruler
// draws the turns around the middle of the viewport (the slice) and
// slides that block as the transcript scrolls, so one dash always
// means one turn and a session with thousands of turns never ends up
// drawing a hatch that runs past the column. Near either end of the
// conversation the slice clamps and the block itself gets shorter, and
// the end it was cut at fades out while the transcript's own end stays
// a hard edge.
const DENSE_TICK_THRESHOLD = 40;
// Turns drawn on either side of the anchor. 41 dashes stack to 478px at
// the compact pitch — the block a 40-turn ruler draws — so the middle
// of a long session is the same ruler a short session shows.
const DENSE_SLICE_HALF = 20;
// Preview Markdown is bounded so a hover over a huge assistant reply
// does not pay the full parse cost; the chat transcript stays the
// place for the complete answer.
export const MAX_PEEK_USER_CHARS = 800;
export const MAX_PEEK_MARKDOWN_CHARS = 4000;

export interface MessagePeekItem {
  // index is the turn's position in turnArtifacts; ChatView uses it as
  // the jump target and as the current-position identity.
  index: number;
}

export interface MessagePeekPreview {
  user: string;
  answer: string;
  running: boolean;
}

function boundedMarkdown(text: string, max: number) {
  if (text.length <= max) return text;
  return `${text.slice(0, max).trimEnd()}\n\n…`;
}

// MessagePeek renders the compact turn ruler on the left side of the
// transcript. Hovering a tick shows the user request as the title and
// the turn's final assistant text as markdown content; clicking a tick
// asks ChatView to scroll to that turn. The active turn is highlighted
// with a wider accent tick instead of a separate indicator.
export const MessagePeek = memo(function MessagePeek({
  items,
  activeRange,
  onJump,
  getPreview,
  revision,
}: {
  items: MessagePeekItem[];
  // activeRange covers every turn whose rows intersect the current
  // viewport; all of them are highlighted on the ruler.
  activeRange: { start: number; end: number } | null;
  onJump: (turnIndex: number) => void;
  getPreview: (turnIndex: number) => MessagePeekPreview;
  // revision only exists to re-fetch a hovered preview when the turn
  // archive changes (new turn, artifacts, turn_end). Token streaming
  // does not change it, so an open tooltip is not reparsed every delta.
  revision?: unknown;
}) {
  const { t } = useTranslation();
  const openFileTarget = useStore((s) => s.openFileTarget);
  // Links inside a preview follow the chat's own rule: they open in the
  // session's file panel, exactly like a link in the transcript.
  const openLink: MarkdownLinkHandler = (href, base) =>
    void openFileTarget(href, base ?? '');
  const rootRef = useRef<HTMLDivElement>(null);
  const scrubberRef = useRef<HTMLDivElement>(null);
  const cardRef = useRef<HTMLDivElement>(null);
  const [hovered, setHovered] = useState<number | null>(null);
  const [tooltipTop, setTooltipTop] = useState(0);
  const [preview, setPreview] = useState<MessagePeekPreview | null>(null);
  const dense = items.length > DENSE_TICK_THRESHOLD;
  const hasActiveRange =
    activeRange !== null &&
    activeRange.start >= 0 &&
    activeRange.end >= 0 &&
    activeRange.end < items.length &&
    activeRange.start <= activeRange.end;
  // The slice is anchored on the turn in the middle of the viewport, so
  // the accent dashes (every turn on screen) land in the middle of the
  // block. Before the transcript has been measured there is no current
  // range yet, and a session opens at its newest turn, so that stands
  // in until the first measurement lands.
  const newestTurn = Math.max(0, items.length - 1);
  const currentStart =
    activeRange !== null && hasActiveRange ? activeRange.start : newestTurn;
  const currentEnd =
    activeRange !== null && hasActiveRange ? activeRange.end : newestTurn;
  const anchor = Math.floor((currentStart + currentEnd) / 2);
  // The slice is the window the dense ruler draws: one dash per turn
  // for the turns around the anchor, clamped at both ends of the
  // conversation. The block hugs its content exactly like the compact
  // ruler, so it stays centered and gets shorter near the ends instead
  // of spilling out of the column.
  const slice = useMemo(() => {
    if (!dense) return null;
    return {
      start: Math.max(0, anchor - DENSE_SLICE_HALF),
      end: Math.min(items.length - 1, anchor + DENSE_SLICE_HALF),
    };
  }, [anchor, dense, items.length]);
  // Preview markdown is expensive enough that rapid scrubber moves
  // should not parse every crossed turn. Debounce the fetch and cache
  // one preview per turn; the cache resets when the archive revision
  // changes (turn_end, new turn, artifacts).
  const previewCacheRef = useRef<Map<number, MessagePeekPreview>>(new Map());
  const previewTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const revisionRef = useRef<unknown>(revision);
  // focusTurn is where the ruler starts when it takes focus: the middle
  // of the slice, because the window is all the dense ruler can point
  // at, or the first turn on screen for the compact ruler.
  const focusTurn = dense
    ? anchor
    : activeRange !== null && hasActiveRange
      ? activeRange.start
      : 0;

  useEffect(() => {
    if (hovered == null || !rootRef.current) return;
    // The slice moves with the transcript, so a hovered turn can slide
    // out of the window; there is no dash to point at until the pointer
    // finds it again.
    if (slice && (hovered < slice.start || hovered > slice.end)) return;
    const rootRect = rootRef.current.getBoundingClientRect();
    const tickRect = rootRef.current
      .querySelector<HTMLElement>(
        `[data-peek-tick="${hovered}"], [data-peek-index="${hovered}"]`,
      )
      ?.getBoundingClientRect();
    if (!tickRect || !rootRect.height) return;
    // The card is taller than a dash and the block sits close to the
    // edge near either end of the transcript, so center the card on its
    // dash but keep it inside the column. Its own height is the honest
    // measure; before the card has rendered, fall back to an estimate.
    const cardHeight =
      cardRef.current?.offsetHeight ??
      Math.max(120, Math.min(280, rootRect.height - 16));
    const center = tickRect.top - rootRect.top + tickRect.height / 2;
    setTooltipTop(
      Math.max(
        8,
        Math.min(center - cardHeight / 2, rootRect.height - cardHeight - 8),
      ),
    );
  }, [hovered, items.length, preview, slice]);

  useEffect(() => {
    if (hovered == null || hovered >= items.length) {
      setPreview(null);
      return;
    }
    if (revisionRef.current !== revision) {
      revisionRef.current = revision;
      previewCacheRef.current.clear();
    }
    const cached = previewCacheRef.current.get(hovered);
    if (cached) {
      setPreview(cached);
      return;
    }
    if (previewTimerRef.current != null) {
      clearTimeout(previewTimerRef.current);
    }
    previewTimerRef.current = setTimeout(() => {
      previewTimerRef.current = null;
      const next = getPreview(hovered);
      previewCacheRef.current.set(hovered, next);
      setPreview(next);
    }, 90);
    return () => {
      if (previewTimerRef.current != null) {
        clearTimeout(previewTimerRef.current);
        previewTimerRef.current = null;
      }
    };
  }, [getPreview, hovered, items.length, revision]);

  if (items.length === 0) return null;

  // The scrubber's hit area covers the slice rather than the column:
  // pointer position maps onto the turns the block draws, so a quarter
  // of the way down means the same thing whether the session has 41
  // turns or 4100. Reaching turns outside the window is the
  // transcript's job — the block slides there as it scrolls.
  const indexFromPointer = (clientY: number) => {
    const el = scrubberRef.current;
    if (!el || !slice) return 0;
    const rect = el.getBoundingClientRect();
    const progress = Math.max(
      0,
      Math.min(1, (clientY - rect.top) / Math.max(1, rect.height)),
    );
    return Math.min(
      slice.end,
      slice.start + Math.floor(progress * (slice.end - slice.start + 1)),
    );
  };
  // A slice that was cut at either end fades over the last rung of its
  // own scale: the block says "there is more in that direction" while
  // the transcript's own ends stay hard edges, so the block itself is
  // the position signal.
  const cutTop = slice !== null && slice.start > 0;
  const cutBottom = slice !== null && slice.end < items.length - 1;
  const cut = cutTop
    ? cutBottom
      ? 'both'
      : 'top'
    : cutBottom
      ? 'bottom'
      : 'none';
  const fadeWidth = '2rem';
  const fade =
    cutTop || cutBottom
      ? `linear-gradient(to bottom, ${
          cutTop ? `transparent 0, black ${fadeWidth}` : 'black 0'
        }, ${
          cutBottom
            ? `black calc(100% - ${fadeWidth}), transparent 100%`
            : 'black 100%'
        })`
      : null;

  // The root is exactly as wide as one tick button, so a compact tick
  // and a dense dash center on the same line inside the left gutter.
  return (
    <div
      ref={rootRef}
      data-testid="message-peek"
      className="pointer-events-none absolute inset-y-0 left-2 z-[var(--oc-z-raised)] hidden w-6 items-center md:flex"
    >
      {!dense && (
        <div className="flex flex-col items-center gap-1">
          {items.map((item) => {
            const active =
              activeRange !== null &&
              item.index >= activeRange.start &&
              item.index <= activeRange.end;
            return (
              <button
                key={item.index}
                type="button"
                data-peek-index={item.index}
                aria-label={t('chat.messagePeekJump', {
                  count: item.index + 1,
                })}
                onMouseEnter={() => setHovered(item.index)}
                onMouseLeave={() =>
                  setHovered((prev) => (prev === item.index ? null : prev))
                }
                onFocus={() => setHovered(item.index)}
                onBlur={() =>
                  setHovered((prev) => (prev === item.index ? null : prev))
                }
                onClick={() => {
                  setHovered(null);
                  onJump(item.index);
                }}
                className="group pointer-events-auto flex h-2 w-6 items-center justify-center rounded-tight hover:bg-accent/10"
              >
                <span
                  className={`h-[3px] rounded-full transition-all duration-150 ${
                    active
                      ? 'w-4 bg-accent'
                      : 'w-2 bg-dim/45 group-hover:w-3.5 group-hover:bg-accent/70'
                  }`}
                />
              </button>
            );
          })}
        </div>
      )}
      {dense && slice && (
        <div
          data-peek-cut={cut}
          className="relative flex flex-col items-center gap-1"
          style={fade ? { maskImage: fade, WebkitMaskImage: fade } : undefined}
        >
          {items.slice(slice.start, slice.end + 1).map((item) => {
            const active =
              activeRange !== null &&
              item.index >= activeRange.start &&
              item.index <= activeRange.end;
            const hot = hovered === item.index;
            return (
              <span
                key={item.index}
                className="pointer-events-none flex h-2 w-6 items-center justify-center"
              >
                <span
                  data-peek-tick={item.index}
                  className={`h-[3px] rounded-full transition-all duration-150 ${
                    active
                      ? 'w-4 bg-accent'
                      : hot
                        ? 'w-3.5 bg-accent/70'
                        : 'w-2 bg-dim/45'
                  }`}
                />
              </span>
            );
          })}
          <div
            ref={scrubberRef}
            role="slider"
            tabIndex={0}
            aria-label={t('chat.messagePeekScrubber')}
            aria-valuemin={1}
            aria-valuemax={items.length}
            aria-valuenow={Math.max(1, (hovered ?? focusTurn) + 1)}
            aria-valuetext={t('chat.messagePeekTurn', {
              count: Math.max(1, (hovered ?? focusTurn) + 1),
            })}
            onPointerMove={(event) => {
              const index = indexFromPointer(event.clientY);
              setHovered((prev) => (prev === index ? prev : index));
            }}
            onPointerDown={(event) => {
              setHovered(indexFromPointer(event.clientY));
            }}
            onClick={(event) => {
              const index = indexFromPointer(event.clientY);
              setHovered(null);
              onJump(index);
            }}
            onPointerLeave={() => setHovered(null)}
            onFocus={() => setHovered(focusTurn)}
            onBlur={() => setHovered(null)}
            onKeyDown={(event) => {
              if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
                event.preventDefault();
                const direction = event.key === 'ArrowDown' ? 1 : -1;
                // Arrows walk the block; the window itself follows the
                // transcript, not the keyboard.
                const current = Math.max(slice.start, hovered ?? focusTurn);
                setHovered(
                  Math.min(
                    slice.end,
                    Math.max(slice.start, current + direction),
                  ),
                );
              } else if (event.key === 'Home' || event.key === 'End') {
                // The ends of the conversation are outside the window,
                // so Home/End scroll the transcript there instead of
                // moving a cursor the block cannot show; the ruler
                // re-anchors on the turn that arrives.
                event.preventDefault();
                setHovered(null);
                onJump(event.key === 'Home' ? 0 : items.length - 1);
              } else if (event.key === 'Enter') {
                event.preventDefault();
                onJump(Math.max(slice.start, hovered ?? focusTurn));
              }
            }}
            className="pointer-events-auto absolute inset-y-0 left-1/2 w-8 -translate-x-1/2 cursor-pointer rounded-full outline-none transition-colors hover:bg-accent/5 focus-visible:ring-2 focus-visible:ring-accent/50"
          />
        </div>
      )}
      {preview && (
        <div
          ref={cardRef}
          role="tooltip"
          className="pointer-events-none absolute left-8 z-[var(--oc-z-popover)] w-[22rem] rounded-card border border-edge/80 bg-panel/95 p-4 shadow-modal ring-1 ring-edge/40 backdrop-blur-sm"
          style={{ top: tooltipTop }}
        >
          {preview.user ? (
            <div className="flex items-start justify-between gap-3">
              <div className="prose-chat min-w-0 flex-1 text-sm font-semibold leading-relaxed text-fg [&_p:first-child]:my-0 [&_p:first-child]:line-clamp-2 [&_p:first-child]:whitespace-pre-wrap [&_p:first-child]:break-words">
                <Markdown
                  text={boundedMarkdown(preview.user, 800)}
                  onOpen={openLink}
                />
              </div>
              {preview.running && (
                <span className="mt-0.5 flex shrink-0 items-center gap-1 text-xs text-accent">
                  <Loader2 size={ICON.xs} className="animate-spin" />
                  {t('chat.messagePeekRunning')}
                </span>
              )}
            </div>
          ) : (
            preview.running && (
              <div className="flex items-center gap-1.5 text-xs text-accent">
                <Loader2 size={ICON.xs} className="animate-spin" />
                {t('chat.messagePeekRunning')}
              </div>
            )
          )}
          {preview.user && preview.answer && (
            <div className="my-3 h-px bg-edge/70" />
          )}
          {preview.answer ? (
            <div>
              <div className="text-micro font-semibold uppercase tracking-wider text-dim">
                {t('chat.messagePeekAssistant')}
              </div>
              <div className="prose-chat mt-1.5 max-h-44 overflow-y-auto pr-1 text-xs opacity-75 [&_p:first-child]:mt-0 [&_p:last-child]:mb-0 [&_p]:break-words [&_p]:whitespace-pre-wrap">
                <Markdown
                  text={boundedMarkdown(
                    preview.answer,
                    MAX_PEEK_MARKDOWN_CHARS,
                  )}
                  onOpen={openLink}
                />
              </div>
            </div>
          ) : (
            !preview.user &&
            !preview.running && (
              <p className="text-xs text-dim">
                {t('chat.messagePeekNoContent')}
              </p>
            )
          )}
        </div>
      )}
    </div>
  );
});
