// Stand-in for occompact.estimate in the compaction node's tests.
//
// In production the node asks Go for the prompt footprint of
// MainChannel[from:] (capabilities/compaction, backed by
// summarytext.EstimateTokens): the rendered text at roughly four
// characters per token plus a per-message overhead. These tests pin when
// the node folds, not how tokens are counted, so they inject the
// measurement — but it still has to be an honest function of the slice:
// the policy compares a measurement's coverage against the channel it
// was taken on, so an empty tail must cost nothing.
export interface EstimateMessage {
  role: string;
  content?: { parts?: unknown[] };
}

export function estimateTokensOf(msgs: EstimateMessage[]): number {
  return msgs.reduce(
    (total, m) => total + Math.ceil(renderedText(m).length / 4) + 8,
    0,
  );
}

/** Text parts plus a tool result's text, which is what the fold counts. */
function renderedText(m: EstimateMessage): string {
  const parts = m.content?.parts ?? [];
  let text = '';
  for (const part of parts) {
    const p = part as {
      type?: string;
      text?: string;
      result?: { content?: unknown };
    };
    if (p.type === 'text') text += p.text ?? '';
    else if (p.type === 'tool_result' && p.result)
      text += contentText(p.result.content);
  }
  return text;
}

function contentText(content: unknown): string {
  if (typeof content === 'string') return content;
  const parts = (content as { parts?: unknown[] } | undefined)?.parts ?? [];
  let text = '';
  for (const part of parts) {
    const p = part as { type?: string; text?: string };
    if (p.type === 'text') text += p.text ?? '';
  }
  return text;
}
