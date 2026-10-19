import { useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Gauge } from 'lucide-react';
import { formatCompact } from '../lib/compactNumber';
import type { ContextUsageDTO } from '../lib/types';
import { Popover } from './ui/Popover';
import { ICON } from './ui/icon';

// The composer's context meter: how much of the serving model's input
// window the last prompt took, and which part of it the harness added.
//
// Two surfaces, one number. Collapsed it is a ring and a fraction
// ("12.3k / 200k") in the composer's footer, right of the sandbox-mode
// chip — the chip says how this session runs, the ring says how full its
// window is, and both are session state rather than composer input, which
// is why neither sits next to Send. Opened it is the breakdown — the
// prompt split by category, in the order the backend lists them.
//
// The ids are wire values (sessions.ContextCategories on the Go side). A
// category this build does not know is dropped rather than drawn
// unlabelled, which is also why the table below is the only place a
// label is decided: the backend never sends text.
//
// The hues are picked against their neighbours, not against the whole
// list: what has to stay apart is what the stack draws side by side, and
// a missing category (no AGENTS.md, nothing folded yet) closes the gap
// between the ones around it — every pair below survives that, which is
// why this is not simply series-1 through series-7 in order.
const CATEGORIES = [
  {
    id: 'instructions',
    labelKey: 'chat.contextInstructions',
    // The harness's own voice, and the largest thing it adds: the accent
    // color keeps it reading as "the app", not as the user's content.
    color: 'var(--color-accent)',
  },
  {
    id: 'environment',
    labelKey: 'chat.contextEnvironment',
    color: 'var(--color-series-6)',
  },
  {
    id: 'project_docs',
    labelKey: 'chat.contextProjectDocs',
    color: 'var(--color-series-8)',
  },
  {
    id: 'user_memory',
    labelKey: 'chat.contextUserMemory',
    color: 'var(--color-series-7)',
  },
  {
    id: 'memory_summary',
    labelKey: 'chat.contextMemorySummary',
    color: 'var(--color-series-4)',
  },
  {
    id: 'conversation',
    labelKey: 'chat.contextConversation',
    color: 'var(--color-series-2)',
  },
  {
    id: 'turn_context',
    labelKey: 'chat.contextTurnContext',
    color: 'var(--color-series-3)',
  },
  {
    id: 'tools',
    labelKey: 'chat.contextTools',
    // The residual is not content: tool schemas, provider framing and
    // whatever the estimates above missed. Grey keeps it looking like
    // overhead instead of competing for the eye.
    color: 'var(--color-faint)',
  },
] as const;

const LABEL_BY_ID = new Map<string, string>(
  CATEGORIES.map((c) => [c.id, c.labelKey]),
);
const COLOR_BY_ID = new Map<string, string>(
  CATEGORIES.map((c) => [c.id, c.color]),
);

interface Slice {
  id: string;
  label: string;
  tokens: number;
  color: string;
}

// percentLabel keeps a share readable at both ends: a category that
// rounds to 0% is noise the row already shows in tokens, and 99.6% is
// not "100%" — it is "almost all of it".
function percentLabel(part: number, whole: number): string {
  if (whole <= 0 || part <= 0) return '0%';
  const pct = (part / whole) * 100;
  if (pct >= 99.95) return '100%';
  if (pct >= 10) return `${pct.toFixed(0)}%`;
  return `${pct.toFixed(1)}%`;
}

// windowPercent is percentLabel for the one ratio in the popover that can
// pass its whole. A prompt can run past the model window — an unfolded
// full-history replay, or a provider that counts past the window it
// enforces — and percentLabel would round 424% down to "100%", the one
// number that must not stand next to a full, red bar: it would say the
// prompt exactly filled a window it overflowed four times.
function windowPercent(total: number, limit: number): string {
  const pct = limit > 0 ? (total / limit) * 100 : 0;
  if (pct > 100.5) return `${Math.round(pct)}%`;
  return percentLabel(total, limit);
}

function Ring({ share, color }: { share: number; color: string }) {
  const radius = 6;
  const circumference = 2 * Math.PI * radius;
  const filled = Math.max(0, Math.min(1, share));
  return (
    <svg
      width={ICON.xs}
      height={ICON.xs}
      viewBox="0 0 16 16"
      aria-hidden
      className="shrink-0"
    >
      <circle
        cx="8"
        cy="8"
        r={radius}
        fill="none"
        stroke="var(--color-panel3)"
        strokeWidth="2"
      />
      <circle
        cx="8"
        cy="8"
        r={radius}
        fill="none"
        stroke={color}
        strokeWidth="2"
        strokeLinecap="round"
        strokeDasharray={`${filled * circumference} ${circumference}`}
        transform="rotate(-90 8 8)"
      />
    </svg>
  );
}

export function ContextMeter({ usage }: { usage: ContextUsageDTO }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement | null>(null);

  // The categories always sum to what was measured (the backend puts the
  // difference in the residual), so their sum is the one number every
  // share below divides by — including when the turn reported no usage
  // and the sum is an estimate.
  const total = useMemo(
    () => (usage.categories ?? []).reduce((sum, c) => sum + c.tokens, 0),
    [usage],
  );
  const slices = useMemo<Slice[]>(() => {
    const out: Slice[] = [];
    for (const c of usage.categories ?? []) {
      const labelKey = LABEL_BY_ID.get(c.id);
      const color = COLOR_BY_ID.get(c.id);
      if (!labelKey || !color || c.tokens <= 0) continue;
      out.push({ id: c.id, label: t(labelKey), tokens: c.tokens, color });
    }
    // Canonical order, whatever order the wire used: the stack has to
    // read the same on every turn or the bar reshuffles itself.
    const rank = new Map<string, number>(CATEGORIES.map((c, i) => [c.id, i]));
    out.sort((a, b) => (rank.get(a.id) ?? 0) - (rank.get(b.id) ?? 0));
    return out;
  }, [usage, t]);

  const limit = usage.limit_tokens ?? 0;
  const share = limit > 0 ? total / limit : 0;
  // A prompt at the edge of the window is a problem the user should see
  // before the fold surprises them, so the ring changes color first and
  // stops being decorative.
  const tone =
    share >= 0.9
      ? 'var(--color-err)'
      : share >= 0.7
        ? 'var(--color-warn)'
        : 'var(--color-accent)';
  const estimated = (usage.measured_tokens ?? 0) <= 0;
  // The conversation row is what the provider's count left once the rows
  // the harness rendered are subtracted, so it reads as a remainder rather
  // than as a size whenever the prompt carried less of the conversation
  // than prepare estimated - which is what folding does. Saying so is the
  // difference between a meter and a guess that looks like a meter.
  const capped = !estimated && usage.conversation_capped === true;

  if (total <= 0) return null;

  const usedLabel = formatCompact(total, { trim: true });
  const limitLabel = limit > 0 ? formatCompact(limit, { trim: true }) : '';

  return (
    <>
      <button
        ref={triggerRef}
        onClick={() => setOpen((v) => !v)}
        aria-label={t('chat.contextMeter')}
        aria-expanded={open}
        aria-haspopup="dialog"
        data-tip={t('chat.contextMeter')}
        className="flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-control px-1.5 py-1 text-micro tabular-nums text-dim transition-colors hover:bg-panel2 hover:text-fg"
      >
        {limit > 0 ? (
          <Ring share={share} color={tone} />
        ) : (
          <Gauge size={ICON.xs} className="shrink-0" aria-hidden />
        )}
        <span>{usedLabel}</span>
        {limit > 0 && (
          // The denominator is the first thing to go when the composer
          // narrows: the ring still carries the share, and the popover
          // has the full figure. The query is the footer's own width, so
          // a resized window and a split pane both land here.
          <span className="hidden text-faint @min-[30rem]:inline">
            / {limitLabel}
          </span>
        )}
      </button>
      {open && (
        <Popover
          open
          onClose={() => setOpen(false)}
          anchor={triggerRef.current}
          role="dialog"
          ariaLabel={t('chat.contextMeter')}
          align="start"
          panelClassName="w-72 rounded-control border border-edge bg-panel p-3 shadow-popover"
        >
          <div className="flex items-baseline gap-2">
            <span className="text-xs font-medium text-fg">
              {t('chat.contextTitle')}
            </span>
            {usage.model && (
              <span className="min-w-0 flex-1 truncate text-right text-micro text-faint">
                {usage.model}
              </span>
            )}
          </div>
          <div className="mt-2 flex items-baseline gap-2">
            <span className="text-title font-semibold tabular-nums text-fg">
              {usedLabel}
            </span>
            <span className="text-micro text-dim">
              {limit > 0
                ? t('chat.contextOfWindow', {
                    percent: windowPercent(total, limit),
                    limit: limitLabel,
                  })
                : t('chat.contextNoWindow')}
            </span>
          </div>
          <div className="mt-2 h-1.5 w-full overflow-hidden rounded-full bg-panel3">
            <div
              className="flex h-full"
              style={{
                width: `${(limit > 0 ? Math.min(1, share) : 1) * 100}%`,
              }}
            >
              {slices.map((s) => (
                <div
                  key={s.id}
                  style={{
                    width: `${(s.tokens / total) * 100}%`,
                    background: s.color,
                  }}
                  data-tip={`${s.label} · ${s.tokens.toLocaleString()}`}
                />
              ))}
            </div>
          </div>
          <ul className="mt-2.5 space-y-1">
            {slices.map((s) => (
              <li key={s.id} className="flex items-center gap-2 text-micro">
                <span
                  aria-hidden
                  className="h-2 w-2 shrink-0 rounded-[2px]"
                  style={{ background: s.color }}
                />
                <span
                  className="min-w-0 flex-1 truncate text-dim"
                  // The residual row is the one number the harness cannot
                  // itemize, so it is the one that needs saying: tool
                  // schemas and provider framing live outside every
                  // estimate the breakdown makes.
                  data-tip={
                    s.id === 'tools' ? t('chat.contextToolsHint') : undefined
                  }
                >
                  {s.label}
                </span>
                <span className="tabular-nums text-fg">
                  {formatCompact(s.tokens, { trim: true })}
                </span>
                <span className="w-9 text-right tabular-nums text-faint">
                  {percentLabel(s.tokens, total)}
                </span>
              </li>
            ))}
          </ul>
          {estimated && (
            <p className="mt-2 border-t border-edge pt-2 text-micro leading-snug text-faint">
              {t('chat.contextEstimated')}
            </p>
          )}
          {capped && (
            <p className="mt-2 border-t border-edge pt-2 text-micro leading-snug text-faint">
              {t('chat.contextConversationCapped')}
            </p>
          )}
        </Popover>
      )}
    </>
  );
}
