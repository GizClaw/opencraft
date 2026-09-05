import { memo } from 'react';
import type { AssistantItem } from '../lib/store';
import { Markdown } from './Markdown';
import { ApplyPatchView, ToolCard, WriteView } from './ToolCard';

// AssistantText renders one assistant text block. While the message is
// still streaming it stays plain text: a half-written `#` or `**` would
// otherwise be parsed as a heading/mark on every delta and make the font
// size jump around. Completed messages render markdown once.
const AssistantText = memo(function AssistantText({
  text,
  streaming,
}: {
  text: string;
  streaming: boolean;
}) {
  if (streaming) {
    return <div className="prose-chat whitespace-pre-wrap text-sm">{text}</div>;
  }
  return (
    <div className="prose-chat text-sm">
      <Markdown text={text} />
    </div>
  );
});

// StreamItemView renders one assistant stream item in the full chat
// transcript style.
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
      return <AssistantText text={item.text} streaming={streaming} />;
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
