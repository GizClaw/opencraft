// The text and id primitives the conversation's two halves share: the
// block caps, the chunk-aware text readers, the id counter and the tool
// argument normalizer. They live apart from `model.ts` because both
// `model.ts` and `stream.ts` need them - keeping them in model.ts made
// the two import each other (S4a), which is the cycle S4's exit
// criterion rules out. The declarations themselves are unchanged.

// MAX_ITEMS_PER_MESSAGE bounds how many blocks one assistant message
// keeps while a turn streams. A long turn appends a reasoning block, a
// text block and a tool call per round; past the cap the oldest blocks
// fold into a counter, which is what keeps the message (and every
// re-render that walks it) bounded. The archive keeps the full turn.
export const MAX_ITEMS_PER_MESSAGE = 400;

// MAX_REASONING_CHARS keeps only the tail of a reasoning trace: the
// transcript never renders it, so everything it holds is overhead.
export const MAX_REASONING_CHARS = 16 << 10;

// MAX_ITEM_TEXT_CHARS caps one visible text block. The tail is what the
// reader is watching while streaming, so head and tail are kept with a
// marker between them; the archive still holds the full text.
export const MAX_ITEM_TEXT_CHARS = 256 << 10;

// MAX_TOOL_RESULT_CHARS caps the text one tool result keeps in the
// store. A result is a JSON envelope a card renders (a file dump, a
// command's output) and nothing re-reads it; the archive keeps the whole
// text, and the head/tail marker says it was cut. Without a bound one
// chatty command wrote megabytes into every re-render of its turn.
export const MAX_TOOL_RESULT_CHARS = 256 << 10;

// MAX_TOOL_ARGS_CHARS caps the stored argument text of one tool call.
// write_file carries whole files, and the store keeps args as a compact
// JSON string for the card to parse.
export const MAX_TOOL_ARGS_CHARS = 512 << 10;

// itemText returns the display text of a streaming block, joining the
// accumulated chunks. Callers that render or copy the text use this
// instead of reading item.text directly.
export function itemText(item: { text: string; chunks?: string[] }): string {
  if (!item.chunks || item.chunks.length === 0) return item.text;
  if (item.chunks.length === 1) return item.text + item.chunks[0];
  return item.text + item.chunks.join('');
}

// itemChars is itemText's length without building the string: the
// streaming paths run it on every delta, where a join would cost the
// whole block. It falls back to measuring the parts for blocks that
// arrived from history and carry no running total.
export function itemChars(item: {
  text: string;
  chunks?: string[];
  chars?: number;
}): number {
  if (item.chars !== undefined) return item.chars;
  let total = item.text.length;
  for (const chunk of item.chunks ?? []) total += chunk.length;
  return total;
}

export let msgSeq = 0;

export const newID = (prefix: string) => `${prefix}-${Date.now()}-${msgSeq++}`;

// normalizeArgs coerces the wire form of tool arguments to a string:
// arguments is a json.RawMessage, so the frontend receives a parsed
// object/array rather than text, and rendering it raw crashes React.
export function normalizeArgs(args: unknown): string {
  if (typeof args === 'string') return args;
  try {
    // Compact, not pretty-printed: the card parses this string and
    // pretty-prints it for display itself, so indentation here would only
    // enlarge what the store holds (a written file can be megabytes).
    const raw = JSON.stringify(args);
    if (raw.length > MAX_TOOL_ARGS_CHARS) {
      return `${raw.slice(0, MAX_TOOL_ARGS_CHARS)}…[args truncated]`;
    }
    return raw;
  } catch {
    return String(args);
  }
}
