import type { ContentWire } from './types';

// ToolImage is one renderable image part of a tool result: the bytes
// the model was shown, ready for an <img src>. view_image is the only
// built-in tool that returns image parts — it is the difference
// between "the model read this file" and "the model looked at it".
export interface ToolImage {
  data_url: string;
  media_type?: string;
}

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

// toolResultImages extracts the inline image parts of a tool result as
// data URLs, so the card can show exactly what the model saw instead of
// a caption about it. Source-url parts are skipped: a viewer resolves
// those from the file path, and an inline part is the only form whose
// bytes survive independently of the file on disk.
export function toolResultImages(
  content: ContentWire | undefined,
): ToolImage[] {
  const out: ToolImage[] = [];
  for (const part of content?.parts ?? []) {
    if (part.type !== 'image') continue;
    const source = part.source;
    if (source?.kind !== 'inline' || !source.data) continue;
    const mediaType = source.media_type || 'image/jpeg';
    out.push({
      data_url: `data:${mediaType};base64,${source.data}`,
      media_type: mediaType,
    });
  }
  return out;
}
