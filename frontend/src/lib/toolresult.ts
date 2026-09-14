import type { ContentWire } from './types';

// toolResultText renders the canonical payload of one tool result: the
// ordered parts the tool returned. Every built-in tool answers with a
// single text part holding its JSON envelope, so the text parts are
// what a card shows; a structured data part falls back to its JSON so
// a non-prose result is not silently dropped, and the media kinds stay
// out of the text projection — the same rule Go's summarytext applies
// when it renders tool activity.
export function toolResultText(content: ContentWire | undefined): string {
  const chunks: string[] = [];
  for (const part of content?.parts ?? []) {
    switch (part.type) {
      case 'text':
        chunks.push(part.text);
        break;
      case 'data': {
        const json = part.value === undefined ? '' : JSON.stringify(part.value);
        if (json) chunks.push(json);
        break;
      }
      default:
        break;
    }
  }
  return chunks.join('\n');
}
