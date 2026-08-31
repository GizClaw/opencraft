import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { HelpCircle, X } from 'lucide-react';
import { Markdown } from './Markdown';
import { useStore } from '../lib/store';
import type { InteractDTO } from '../lib/types';

export function InteractionCard({ spec }: { spec: InteractDTO }) {
  const replyInteract = useStore((s) => s.replyInteract);
  const [text, setText] = useState('');
  const [selected, setSelected] = useState<string[]>([]);
  const [other, setOther] = useState('');
  const { t } = useTranslation();

  const bodyText = spec.body
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
    <div className="rounded-xl border border-warn/40 bg-panel2 p-4 my-3">
      <div className="flex items-center gap-2 text-sm font-medium">
        <HelpCircle size="1.1429rem" className="text-warn" />
        {spec.title || t('interact.needConfirm')}
      </div>
      {bodyText && (
        <div className="prose-chat text-sm mt-2">
          <Markdown text={bodyText} />
        </div>
      )}

      {(spec.kind === 'confirm' || spec.kind === 'select') && (
        <div className="mt-3 space-y-1.5">
          {spec.options.map((opt) => (
            <label
              key={opt.value}
              className="flex items-center gap-2 rounded-lg border border-edge bg-panel px-3 py-2 text-sm cursor-pointer hover:border-accent/50"
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
            <input
              value={other}
              onChange={(e) => setOther(e.target.value)}
              placeholder={t('interact.otherPlaceholder')}
              className="w-full rounded-lg border border-edge bg-panel px-3 py-2 text-sm outline-none focus:border-accent"
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
          className="mt-3 w-full rounded-lg border border-edge bg-panel px-3 py-2 text-sm outline-none focus:border-accent resize-y"
        />
      )}

      <div className="mt-3 flex gap-2">
        <button
          onClick={submit}
          className="rounded-lg bg-accent text-white px-4 py-1.5 text-sm hover:opacity-90"
        >
          {t('interact.submit')}
        </button>
        <button
          onClick={() =>
            void replyInteract(spec.id, { text: '', cancel: true })
          }
          className="flex items-center gap-1 rounded-lg border border-edge px-3 py-1.5 text-sm text-dim hover:text-fg"
        >
          <X size="0.9286rem" /> {t('interact.cancel')}
        </button>
      </div>
    </div>
  );
}
