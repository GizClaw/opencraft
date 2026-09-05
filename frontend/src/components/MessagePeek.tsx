import { memo, useEffect, useRef, useState } from 'react';
import { Loader2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Markdown } from './Markdown';

// With a normal number of turns the ticks form a compact, vertically
// centered ruler: every turn is one short line and the gap between
// lines stays small. Long sessions switch to a dense scrubber whose
// full height maps to turn order, so every turn remains reachable.
const DENSE_TICK_THRESHOLD = 40;
const GRADIENT_TICK_LIMIT = 256;
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
  const rootRef = useRef<HTMLDivElement>(null);
  const scrubberRef = useRef<HTMLDivElement>(null);
  const [hovered, setHovered] = useState<number | null>(null);
  const [tooltipTop, setTooltipTop] = useState(0);
  const [preview, setPreview] = useState<MessagePeekPreview | null>(null);
  const dense = items.length > DENSE_TICK_THRESHOLD;
  // Preview markdown is expensive enough that rapid scrubber moves
  // should not parse every crossed turn. Debounce the fetch and cache
  // one preview per turn; the cache resets when the archive revision
  // changes (turn_end, new turn, artifacts).
  const previewCacheRef = useRef<Map<number, MessagePeekPreview>>(new Map());
  const previewTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const revisionRef = useRef<unknown>(revision);
  const hasActiveRange =
    activeRange !== null &&
    activeRange.start >= 0 &&
    activeRange.end >= 0 &&
    activeRange.end < items.length &&
    activeRange.start <= activeRange.end;
  const focusTurn =
    activeRange !== null && hasActiveRange ? activeRange.start : 0;

  useEffect(() => {
    if (hovered == null || !rootRef.current) return;
    if (dense) {
      const height = rootRef.current.clientHeight || 1;
      const tick = ((hovered + 0.5) / items.length) * height;
      const cardHeight = Math.max(120, Math.min(280, height - 16));
      const top = Math.max(
        8,
        Math.min(tick - cardHeight / 2, height - cardHeight - 8),
      );
      setTooltipTop(Math.max(8, top));
      return;
    }
    const button = rootRef.current.querySelector<HTMLElement>(
      `[data-peek-index="${hovered}"]`,
    );
    const rootRect = rootRef.current.getBoundingClientRect();
    const buttonRect = button?.getBoundingClientRect();
    if (buttonRect && rootRect.height) {
      setTooltipTop(buttonRect.top - rootRect.top + buttonRect.height / 2);
    }
  }, [dense, hovered, items.length]);

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

  const indexFromPointer = (clientY: number) => {
    const el = scrubberRef.current ?? rootRef.current;
    if (!el) return 0;
    const rect = el.getBoundingClientRect();
    const y = clientY - rect.top;
    const progress = Math.max(0, Math.min(1, y / Math.max(1, rect.height)));
    return Math.min(items.length - 1, Math.floor(progress * items.length));
  };

  return (
    <div
      ref={rootRef}
      data-testid="message-peek"
      className="pointer-events-none absolute inset-y-0 left-2 z-20 hidden items-center md:flex"
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
                className="group pointer-events-auto flex h-2 w-6 items-center justify-center rounded hover:bg-accent/10"
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
      {dense && (
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
            const index = indexFromPointer(event.clientY);
            setHovered(index);
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
              const current = Math.max(0, hovered ?? focusTurn);
              const next = Math.min(
                items.length - 1,
                Math.max(0, current + direction),
              );
              setHovered(next);
            } else if (event.key === 'Home') {
              event.preventDefault();
              setHovered(0);
            } else if (event.key === 'End') {
              event.preventDefault();
              setHovered(items.length - 1);
            } else if (event.key === 'Enter') {
              event.preventDefault();
              onJump(Math.max(0, hovered ?? focusTurn));
            }
          }}
          className="pointer-events-auto absolute inset-y-1.5 left-1/2 w-8 -translate-x-1/2 cursor-pointer rounded-full outline-none transition-colors hover:bg-accent/5 focus-visible:ring-2 focus-visible:ring-accent/50"
          style={{
            backgroundImage:
              items.length <= GRADIENT_TICK_LIMIT
                ? `repeating-linear-gradient(to bottom, transparent 0, transparent calc(${100 / items.length}% - 1px), var(--color-dim) calc(${100 / items.length}% - 1px), var(--color-dim) ${100 / items.length}%)`
                : undefined,
          }}
        />
      )}
      {dense && hasActiveRange && (
        <span
          aria-hidden="true"
          className="pointer-events-none absolute left-1/2 w-5 -translate-x-1/2 rounded-full bg-accent/25"
          style={{
            top: `${((activeRange!.start + 0.5) / items.length) * 100}%`,
            height: `${Math.max(
              1,
              ((activeRange!.end - activeRange!.start + 1) / items.length) *
                100,
            )}%`,
          }}
        />
      )}
      {preview && (
        <div
          role="tooltip"
          className="pointer-events-none absolute left-8 z-50 w-[22rem] rounded-2xl border border-edge/80 bg-panel/95 p-4 shadow-2xl ring-1 ring-edge/40 backdrop-blur-sm"
          style={{ top: tooltipTop }}
        >
          {preview.user ? (
            <div className="flex items-start justify-between gap-3">
              <div className="prose-chat min-w-0 flex-1 text-sm font-semibold leading-relaxed text-fg [&_p:first-child]:my-0 [&_p:first-child]:line-clamp-2 [&_p:first-child]:whitespace-pre-wrap [&_p:first-child]:break-words">
                <Markdown text={boundedMarkdown(preview.user, 800)} />
              </div>
              {preview.running && (
                <span className="mt-0.5 flex shrink-0 items-center gap-1 text-xs text-accent">
                  <Loader2 size="0.8571rem" className="animate-spin" />
                  {t('chat.messagePeekRunning')}
                </span>
              )}
            </div>
          ) : (
            preview.running && (
              <div className="flex items-center gap-1.5 text-xs text-accent">
                <Loader2 size="0.8571rem" className="animate-spin" />
                {t('chat.messagePeekRunning')}
              </div>
            )
          )}
          {preview.user && preview.answer && (
            <div className="my-3 h-px bg-edge/70" />
          )}
          {preview.answer ? (
            <div>
              <div className="text-[0.7143rem] font-semibold uppercase tracking-wider text-dim">
                {t('chat.messagePeekAssistant')}
              </div>
              <div className="prose-chat mt-1.5 max-h-44 overflow-y-auto pr-1 text-xs opacity-75 [&_p:first-child]:mt-0 [&_p:last-child]:mb-0 [&_p]:break-words [&_p]:whitespace-pre-wrap">
                <Markdown
                  text={boundedMarkdown(
                    preview.answer,
                    MAX_PEEK_MARKDOWN_CHARS,
                  )}
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
