import { memo, useState } from 'react';
import { ChevronDown, ChevronUp } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { itemText, useStore, type AssistantItem } from '../lib/store';
import { markdownPreview } from '../lib/markdownPreview';
import { Markdown } from './Markdown';
import { ApplyPatchView, ToolCard, WriteView } from './ToolCard';
import { ICON } from './ui/icon';

// BLOCK_FOLD_CHARS is how much of one settled block the transcript renders
// before folding the rest behind a control.
//
// An answer costs the renderer per character, not per message: each inline
// span, list item and code line becomes its own element and text node
// (~0.09 and ~0.12 per character, measured on a 10k-character answer), and
// those objects are what made a scrolled-back session expensive in WebKit.
// Folding bounds a block by what is on screen instead of by what the model
// wrote; the archive still holds every character, the copy action copies
// the whole answer, and one click shows the rest. The value is deliberately
// generous: a block that fits a screen or two never folds, so the control
// only appears on answers long enough to be worth it.
const BLOCK_FOLD_CHARS = 2 << 10;

// AssistantText renders one assistant text block. The block the model is
// still writing stays plain text: a half-written `#` or `**` would
// otherwise be parsed as a heading/mark on every delta and make the font
// size jump around. A block that has stopped growing renders markdown —
// parsed once, and the memo below keeps it out of every later delta of
// the block that followed it.
//
// A settled block longer than BLOCK_FOLD_CHARS renders its first section
// and a control instead of the whole answer, unless it is the answer the
// reader is on: the transcript's newest reply. That block is whole
// whether its deltas arrived in this window, came back from the archive
// on a reload, or landed while another conversation was on screen -
// folding it would hide the answer to the question just asked, and the
// history of what this window happened to watch is not something the
// reader can see. Everything a newer answer has pushed above is history,
// and history folds: the reader reaches it by scrolling back, and one
// click opens it. The boundary is the position, not the turn - a steer
// row below the answer does not move it (an interjection goes into the
// live turn rather than opening a new one), while the next answer does.
const AssistantText = memo(function AssistantText({
  text,
  streaming,
  atTail,
}: {
  text: string;
  streaming: boolean;
  // atTail says the block is still the reader's position: the transcript's
  // last row, or the newest answer (an interjection below it does not end
  // it).
  atTail: boolean;
}) {
  const { t, i18n } = useTranslation();
  const openFileTarget = useStore((s) => s.openFileTarget);
  // Expanded is one-shot, on purpose: the row unmounts when the reader
  // scrolls far enough for the transcript's windowing to drop it, and
  // remembering which blocks were opened would take a set of item ids that
  // grows for the life of the document - the state this window is trying
  // to bound - to save one click.
  const [expanded, setExpanded] = useState(false);
  if (streaming) {
    return <div className="prose-chat whitespace-pre-wrap text-sm">{text}</div>;
  }
  // null means "show everything": either the block is short, or the reader
  // asked for it.
  const preview =
    atTail || expanded ? null : markdownPreview(text, BLOCK_FOLD_CHARS);
  return (
    <div className="prose-chat text-sm">
      <Markdown
        text={preview ? preview.shown : text}
        onOpen={(href, base) => void openFileTarget(href, base ?? '')}
      />
      {preview ? (
        <button
          type="button"
          aria-expanded={expanded}
          onClick={() => setExpanded(true)}
          className="mt-1.5 flex items-center gap-1 rounded-tight text-xs text-dim transition-colors hover:text-accent"
        >
          <ChevronDown size={ICON.xs} className="shrink-0" />
          {t('chat.foldAnswerShow', {
            // Formatted for the UI language, not the system locale, and
            // handed over as the count it is. An unset language is a
            // RangeError to Intl, and this is a render: undefined asks
            // for the runtime's default instead.
            chars: new Intl.NumberFormat(i18n.language || undefined).format(
              text.length,
            ),
          })}
        </button>
      ) : expanded ? (
        <button
          type="button"
          aria-expanded
          onClick={() => setExpanded(false)}
          className="mt-1.5 flex items-center gap-1 rounded-tight text-xs text-dim transition-colors hover:text-fg"
        >
          <ChevronUp size={ICON.xs} className="shrink-0" />
          {t('chat.foldAnswerHide')}
        </button>
      ) : null}
    </div>
  );
});

// StreamItemView renders one assistant stream item in the full chat
// transcript style. streaming marks the one item the model is still
// writing (the message's trailing block, while its turn runs); every
// other item is settled and renders its final form. That is what makes
// a text block a tool call ended flip to markdown as the call arrives
// instead of at the turn end.
export const StreamItemView = memo(function StreamItemView({
  item,
  streaming = false,
  atTail = true,
}: {
  item: AssistantItem;
  streaming?: boolean;
  atTail?: boolean;
}) {
  switch (item.kind) {
    case 'reasoning':
      return null;
    case 'text':
      return (
        <AssistantText
          text={itemText(item)}
          streaming={streaming}
          atTail={atTail}
        />
      );
    case 'tool_call':
      return item.tool.name === 'apply_patch' ? (
        <ApplyPatchView key={item.id} tool={item.tool} />
      ) : item.tool.name === 'write_file' ? (
        <WriteView key={item.id} tool={item.tool} />
      ) : (
        <ToolCard key={item.id} tool={item.tool} />
      );
  }
});
