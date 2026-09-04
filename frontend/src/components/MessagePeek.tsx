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

export interface MessagePeekItem {
  // index is the turn's position in turnArtifacts; ChatView uses it as
  // the jump target and as the current-position identity.
  index: number;
  user: string;
  answer: string;
  running: boolean;
}

// MessagePeek renders the compact turn ruler on the left side of the
// transcript. Hovering a tick shows the user request as the title and
// the turn's final assistant text as markdown content; clicking a tick
// asks ChatView to scroll to that turn. The active turn is highlighted
// with a wider accent tick instead of a separate indicator.
export const MessagePeek = memo(function MessagePeek({
  items,
  currentIndex,
  onJump,
}: {
  items: MessagePeekItem[];
  currentIndex: number;
  onJump: (turnIndex: number) => void;
}) {
  const { t } = useTranslation();
  const rootRef = useRef<HTMLDivElement>(null);
  const scrubberRef = useRef<HTMLDivElement>(null);
  const [hovered, setHovered] = useState<number | null>(null);
  const [tooltipTop, setTooltipTop] = useState(0);
  const dense = items.length > DENSE_TICK_THRESHOLD;
  const activeIndex = currentIndex >= 0 ? currentIndex : 0;

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

  if (items.length === 0) return null;

  const hoveredItem =
    hovered == null || hovered >= items.length ? null : items[hovered];
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
            const active = item.index === currentIndex;
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
          aria-valuenow={Math.max(1, (hovered ?? activeIndex) + 1)}
          aria-valuetext={t('chat.messagePeekTurn', {
            count: Math.max(1, (hovered ?? activeIndex) + 1),
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
          onFocus={() => setHovered(activeIndex)}
          onBlur={() => setHovered(null)}
          onKeyDown={(event) => {
            if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
              event.preventDefault();
              const direction = event.key === 'ArrowDown' ? 1 : -1;
              const current = Math.max(0, hovered ?? activeIndex);
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
              onJump(Math.max(0, hovered ?? activeIndex));
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
      {dense && currentIndex >= 0 && currentIndex < items.length && (
        <span
          aria-hidden="true"
          className="pointer-events-none absolute left-1/2 h-[3px] w-5 -translate-x-1/2 -translate-y-1/2 rounded-full bg-accent"
          style={{
            top: `${((currentIndex + 0.5) / items.length) * 100}%`,
          }}
        />
      )}
      {hoveredItem && (
        <div
          role="tooltip"
          className="pointer-events-none absolute left-8 z-50 w-[22rem] rounded-2xl border border-edge/80 bg-panel/95 p-4 shadow-2xl ring-1 ring-edge/40 backdrop-blur-sm"
          style={{ top: tooltipTop }}
        >
          {hoveredItem.user ? (
            <div className="flex items-start justify-between gap-3">
              <div className="prose-chat min-w-0 flex-1 text-sm font-semibold leading-relaxed text-fg [&_p:first-child]:my-0 [&_p:first-child]:line-clamp-2 [&_p:first-child]:whitespace-pre-wrap [&_p:first-child]:break-words">
                <Markdown text={hoveredItem.user} />
              </div>
              {hoveredItem.running && (
                <span className="mt-0.5 flex shrink-0 items-center gap-1 text-xs text-accent">
                  <Loader2 size="0.8571rem" className="animate-spin" />
                  {t('chat.messagePeekRunning')}
                </span>
              )}
            </div>
          ) : (
            hoveredItem.running && (
              <div className="flex items-center gap-1.5 text-xs text-accent">
                <Loader2 size="0.8571rem" className="animate-spin" />
                {t('chat.messagePeekRunning')}
              </div>
            )
          )}
          {hoveredItem.user && hoveredItem.answer && (
            <div className="my-3 h-px bg-edge/70" />
          )}
          {hoveredItem.answer ? (
            <div>
              <div className="text-[0.7143rem] font-semibold uppercase tracking-wider text-dim">
                {t('chat.messagePeekAssistant')}
              </div>
              <div className="prose-chat mt-1.5 max-h-44 overflow-y-auto pr-1 text-xs opacity-75 [&_p:first-child]:mt-0 [&_p:last-child]:mb-0 [&_p]:break-words [&_p]:whitespace-pre-wrap">
                <Markdown text={hoveredItem.answer} />
              </div>
            </div>
          ) : (
            !hoveredItem.user &&
            !hoveredItem.running && (
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
