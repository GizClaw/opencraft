import { memo } from 'react';
import { itemText, useStore, type AssistantItem } from '../lib/store';
import { Markdown } from './Markdown';
import { ApplyPatchView, ToolCard, WriteView } from './ToolCard';

// AssistantText renders one assistant text block. The block the model is
// still writing stays plain text: a half-written `#` or `**` would
// otherwise be parsed as a heading/mark on every delta and make the font
// size jump around. A block that has stopped growing renders markdown —
// parsed once, and the memo below keeps it out of every later delta of
// the block that followed it.
const AssistantText = memo(function AssistantText({
  text,
  streaming,
}: {
  text: string;
  streaming: boolean;
}) {
  const openFileTarget = useStore((s) => s.openFileTarget);
  if (streaming) {
    return <div className="prose-chat whitespace-pre-wrap text-sm">{text}</div>;
  }
  return (
    <div className="prose-chat text-sm">
      <Markdown
        text={text}
        onOpen={(href, base) => void openFileTarget(href, base ?? '')}
      />
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
}: {
  item: AssistantItem;
  streaming?: boolean;
}) {
  switch (item.kind) {
    case 'reasoning':
      return null;
    case 'text':
      return <AssistantText text={itemText(item)} streaming={streaming} />;
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
