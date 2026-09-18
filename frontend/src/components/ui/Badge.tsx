import type { ReactNode } from 'react';

// Badge — a small bordered chip for counts and statuses (provider
// names, "2 servers", model tags). Kept in one place so the border,
// radius and type size of a chip stop drifting per call site.
export type BadgeTone =
  'neutral' | 'accent' | 'ok' | 'warn' | 'err' | 'subagent' | 'yolo';

const TONES: Record<BadgeTone, string> = {
  neutral: 'border-edge bg-panel text-dim',
  accent: 'border-accent/40 bg-accent/10 text-accent',
  ok: 'border-ok/40 bg-ok/10 text-ok',
  warn: 'border-warn/40 bg-warn/10 text-warn',
  err: 'border-err/40 bg-err/10 text-err',
  subagent: 'border-subagent/40 bg-subagent/10 text-subagent',
  yolo: 'border-yolo/40 bg-yolo/10 text-yolo',
};

export function Badge({
  tone = 'neutral',
  className = '',
  children,
}: {
  tone?: BadgeTone;
  className?: string;
  children: ReactNode;
}) {
  return (
    <span
      className={`inline-flex shrink-0 items-center gap-1 rounded-tight border px-1.5 py-0.5 text-micro leading-tight ${TONES[tone]} ${className}`}
    >
      {children}
    </span>
  );
}
