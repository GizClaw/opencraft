import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { AlertTriangle, HelpCircle, ShieldAlert, X } from 'lucide-react';
import { Markdown } from './Markdown';
import { useStore } from '../lib/store';
import type { InteractDTO, InteractSeverity } from '../lib/types';
import { Button } from './ui/Button';
import { Input } from './ui/Input';
import { ICON } from './ui/icon';

// One prompt used to look the same whatever it asked: a sandbox
// escalation that hands a command the whole host wore the same amber
// border as "which rollout strategy do you prefer?". The host tags each
// prompt with a severity; this is the only place that turns it into
// chrome, so the ladder stays in one place.
const SEVERITY: Record<
  InteractSeverity,
  { border: string; icon: string; Icon: typeof HelpCircle }
> = {
  info: {
    border: 'border-accent/40',
    icon: 'text-accent',
    Icon: HelpCircle,
  },
  notice: {
    border: 'border-warn/40',
    icon: 'text-warn',
    Icon: AlertTriangle,
  },
  danger: {
    border: 'border-err/50',
    icon: 'text-err',
    Icon: ShieldAlert,
  },
};

export function InteractionCard({ spec }: { spec: InteractDTO }) {
  const replyInteract = useStore((s) => s.replyInteract);
  const openFileTarget = useStore((s) => s.openFileTarget);
  const [text, setText] = useState('');
  const [selected, setSelected] = useState<string[]>([]);
  const [other, setOther] = useState('');
  const { t } = useTranslation();

  // An unknown severity renders as notice: a prompt that arrived
  // without a tag is a decision, not small talk.
  const tone = SEVERITY[spec.severity] ?? SEVERITY.notice;
  const { Icon } = tone;
  const bodyText = (spec.body ?? [])
    .map((p) => (p.type === 'text' ? (p.text ?? '') : ''))
    .join('\n');

  const toggle = (value: string) => {
    setSelected((prev) =>
      prev.includes(value)
        ? prev.filter((v) => v !== value)
        : spec.multi
          ? [...prev, value]
          : [value],
    );
  };

  const submit = () => {
    const choices = spec.multi
      ? selected
      : selected.length > 0
        ? [selected[0]]
        : [];
    const finalText =
      spec.kind === 'text'
        ? text
        : spec.allow_other && other.trim()
          ? other
          : '';
    void replyInteract(spec.id, {
      text: finalText,
      option: !spec.multi && selected[0] ? selected[0] : null,
      options: spec.multi ? choices : undefined,
    });
  };

  return (
    <div className={`my-3 rounded-card border bg-panel2 p-4 ${tone.border}`}>
      <div className="flex items-center gap-2 text-sm font-medium">
        <Icon size={ICON.md} className={`shrink-0 ${tone.icon}`} />
        {spec.title || t('interact.needConfirm')}
      </div>
      {bodyText && (
        <div className="prose-chat mt-2 text-sm">
          <Markdown
            text={bodyText}
            onOpen={(href, base) => void openFileTarget(href, base ?? '')}
          />
        </div>
      )}

      {(spec.kind === 'confirm' || spec.kind === 'select') && (
        <div className="mt-3 space-y-1.5">
          {spec.options.map((opt) => (
            <label
              key={opt.value}
              className="flex cursor-pointer items-center gap-2 rounded-control border border-edge bg-panel px-3 py-2 text-sm hover:border-accent/50"
            >
              <input
                type={spec.multi ? 'checkbox' : 'radio'}
                name={`interact-${spec.id}`}
                checked={selected.includes(opt.value)}
                onChange={() => toggle(opt.value)}
                className="accent-[var(--color-accent)]"
              />
              {opt.label}
            </label>
          ))}
          {spec.allow_other && (
            <Input
              value={other}
              onChange={(e) => setOther(e.target.value)}
              placeholder={t('interact.otherPlaceholder')}
            />
          )}
        </div>
      )}

      {spec.kind === 'text' && (
        <textarea
          value={text}
          onChange={(e) => setText(e.target.value)}
          rows={3}
          autoFocus
          placeholder={t('interact.answerPlaceholder')}
          className="mt-3 w-full resize-y rounded-control border border-edge bg-panel px-3 py-2 text-sm outline-none focus:border-accent"
        />
      )}

      <div className="mt-3 flex gap-2">
        <Button variant="primary" onClick={submit}>
          {t('interact.submit')}
        </Button>
        <Button
          variant="quiet"
          onClick={() =>
            void replyInteract(spec.id, { text: '', cancel: true })
          }
        >
          <X size={ICON.sm} /> {t('interact.cancel')}
        </Button>
      </div>
    </div>
  );
}
