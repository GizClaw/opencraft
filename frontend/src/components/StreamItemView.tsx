import { memo } from 'react';
import { Wrench } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { AssistantItem } from '../lib/store';
import { Markdown } from './Markdown';
import { ApplyPatchView, ToolCard, WriteView } from './ToolCard';

// Reasoning is the shared chat-style collapsible reasoning block.
function Reasoning({ text }: { text: string }) {
  const { t } = useTranslation();
  return (
    <details className="mb-1.5">
      <summary className="cursor-pointer text-xs text-dim select-none">
        {t('chat.reasonCollapse')}
      </summary>
      <div className="mt-1 rounded-lg bg-panel2 border border-edge p-3 text-xs text-dim whitespace-pre-wrap">
        {text}
      </div>
    </details>
  );
}

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

// SidebarReasoning / SidebarToolCall are the compact violet styles used
// inside the subagent sidebar stream.
function SidebarReasoning({ text }: { text: string }) {
  const { t } = useTranslation();
  return (
    <details className="text-xs">
      <summary className="cursor-pointer text-violet-300/90 select-none">
        {t('subagent.reasoning')}
      </summary>
      <div className="mt-1 whitespace-pre-wrap text-dim max-h-32 overflow-y-auto">
        {text}
      </div>
    </details>
  );
}

function SidebarToolCall({
  item,
}: {
  item: Extract<AssistantItem, { kind: 'tool_call' }>;
}) {
  return (
    <div>
      <div
        className="flex items-center gap-1.5 font-mono text-xs"
        title={item.tool.args}
      >
        <Wrench size="0.7857rem" className="text-dim shrink-0" />
        <span className="text-fg truncate">{item.tool.name}</span>
        {item.tool.status === 'running' ? (
          <span className="text-accent animate-pulse shrink-0">…</span>
        ) : item.tool.status === 'error' ? (
          <span className="text-err shrink-0">⛔</span>
        ) : (
          <span className="text-ok shrink-0">✓</span>
        )}
      </div>
      {item.tool.result !== undefined && (
        <div
          className={`ml-4 border-l pl-2 text-[0.7857rem] whitespace-pre-wrap max-h-28 overflow-y-auto ${
            item.tool.status === 'error' ? 'text-err' : 'text-dim'
          }`}
        >
          {item.tool.result}
        </div>
      )}
    </div>
  );
}

// StreamItemView renders one assistant stream item in one of two
// variants: the compact sidebar style ("sidebar") or the full chat
// transcript style ("chat"). Both render paths share this component so
// item visibility and per-kind dispatch never drift again.
export const StreamItemView = memo(function StreamItemView({
  item,
  variant,
  streaming = false,
}: {
  item: AssistantItem;
  variant: 'sidebar' | 'chat';
  streaming?: boolean;
}) {
  switch (item.kind) {
    case 'reasoning':
      return variant === 'sidebar' ? (
        <SidebarReasoning text={item.text} />
      ) : (
        <Reasoning text={item.text} />
      );
    case 'text':
      return variant === 'sidebar' ? (
        <p className="text-xs text-fg whitespace-pre-wrap">{item.text}</p>
      ) : (
        <AssistantText text={item.text} streaming={streaming} />
      );
    case 'tool_call':
      if (variant === 'sidebar') {
        return <SidebarToolCall item={item} />;
      }
      return item.tool.name === 'apply_patch' ? (
        <ApplyPatchView key={item.id} tool={item.tool} />
      ) : item.tool.name === 'write_file' ? (
        <WriteView key={item.id} tool={item.tool} />
      ) : (
        <ToolCard key={item.id} tool={item.tool} />
      );
  }
});
