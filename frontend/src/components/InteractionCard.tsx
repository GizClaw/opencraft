import { useLayoutEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { AlertTriangle, HelpCircle, ShieldAlert, X } from 'lucide-react';
import { Markdown } from './Markdown';
import { useStore } from '../lib/store';
import { overlayLayerOpen, useOverlayOpen } from '../lib/overlay';
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

export function InteractionCard({
  spec,
  onAnswered,
}: {
  spec: InteractDTO;
  /**
   * The prompt is finished (answered or dismissed). The page uses it to
   * hand the caret back to the composer, where the user goes next.
   */
  onAnswered?: () => void;
}) {
  const replyInteract = useStore((s) => s.replyInteract);
  const openFileTarget = useStore((s) => s.openFileTarget);
  const [text, setText] = useState('');
  const [selected, setSelected] = useState<string[]>([]);
  const [other, setOther] = useState('');
  const cardRef = useRef<HTMLDivElement | null>(null);
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
    onAnswered?.();
  };

  // What Enter is allowed to send: an answer the user actually made.
  // Nothing is preselected, so Enter can never approve a prompt on its
  // own — it only saves the click on Submit.
  const hasAnswer =
    spec.kind === 'text'
      ? text.trim() !== ''
      : selected.length > 0 || (spec.allow_other && other.trim() !== '');

  // The host is waiting on this prompt, so the card takes the keyboard
  // as it arrives: the first choice (or the answer field) is focused,
  // and Enter answers from there. The first control in DOM order is the
  // same one a mouse user would reach for first.
  //
  // A layer that is already open (the ⌘K palette, a settings dialog)
  // owns the keyboard, though: pulling the caret into a card the user
  // cannot see would send their next keystrokes into the prompt. The
  // card stays passive for as long as that layer is open and takes the
  // keyboard the moment it closes.
  const overlayOpen = useOverlayOpen();
  useLayoutEffect(() => {
    // Both reads matter: the snapshot above covers a layer that was
    // already open when this render happened, the live read covers one
    // that registered earlier in this same commit.
    if (overlayOpen || overlayLayerOpen()) return;
    const card = cardRef.current;
    if (card === null) return;
    (card.querySelector<HTMLElement>('input, textarea') ?? card).focus();
  }, [overlayOpen]);

  const onKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if (event.key !== 'Enter') return;
    // An Enter that is still confirming an IME candidate belongs to the
    // field, not to us. 229 is the keyCode WebKit reports while a
    // composition is in flight.
    if (event.nativeEvent.isComposing || event.nativeEvent.keyCode === 229) {
      return;
    }
    // Shift+Enter is the answer field's newline, and the modifier
    // chords belong to the composer (⌘Enter interrupts the turn).
    if (event.shiftKey || event.altKey || event.metaKey || event.ctrlKey) {
      return;
    }
    // The buttons answer on their own Enter; submitting here as well
    // would send the prompt twice.
    if (
      event.target instanceof Element &&
      event.target.closest('button') !== null
    ) {
      return;
    }
    if (!hasAnswer) return;
    event.preventDefault();
    submit();
  };

  return (
    <div
      ref={cardRef}
      tabIndex={-1}
      onKeyDown={onKeyDown}
      className={`my-3 rounded-card border bg-panel2 p-4 ${tone.border}`}
    >
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
          onClick={() => {
            void replyInteract(spec.id, { text: '', cancel: true });
            onAnswered?.();
          }}
        >
          <X size={ICON.sm} /> {t('interact.cancel')}
        </Button>
        <span className="ml-auto self-center text-micro text-faint">
          {spec.kind === 'text'
            ? t('interact.enterNewlineHint')
            : t('interact.enterHint')}
        </span>
      </div>
    </div>
  );
}
