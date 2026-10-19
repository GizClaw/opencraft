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
// and a control instead of the whole answer, unless the reader is still
// on it: a block whose text arrived through this window's live stream
// (TextItemFields.streamed) and which is still the transcript's last row.
// That block the reader watched grow, and folding it under them the moment
// the turn ends would take away what they were reading. The exemption ends
// with the position: once a later turn lands below it, the block is
// history like the ones above it, and the scene the fold exists for - a
// reader scrolling back through a long session - is exactly where it
// applies.
const AssistantText = memo(function AssistantText({
  text,
  streaming,
  streamed,
  atTail,
}: {
  text: string;
  streaming: boolean;
  streamed: boolean;
  // atTail says the block is in the transcript's last row.
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
    (streamed && atTail) || expanded
      ? null
      : markdownPreview(text, BLOCK_FOLD_CHARS);
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
            // handed over as the count it is.
            chars: new Intl.NumberFormat(i18n.language).format(text.length),
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
          streamed={item.streamed === true}
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
