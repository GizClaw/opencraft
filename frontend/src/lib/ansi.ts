// ANSI escape sequences appear in tool output whenever the command
// thinks it is writing to a terminal (vitest, cargo, git, ...). The UI
// has no terminal emulator, so those sequences are stripped before a
// tool result is stored or rendered.

// Patterns for real ESC bytes and for sequences that survived as the
// JSON-escaped text "\u001b[...]".
const CSI_PATTERNS = [
  /\u001b\[[0-9;?]*[ -\/]*[@-~]/g,
  /\\u001b\[[0-9;?]*[ -\/]*[@-~]/g,
];

const OSC_PATTERNS = [
  /\u001b\][^\u0007]*(?:\u0007|\u001b\\)/g,
  /\\u001b\][^\\u0007]*(?:\\u0007|\\u001b\\)/g,
];

const ESC_PATTERNS = [/\u001b[@-_]/g, /\\u001b[@-_]/g];

export function stripAnsi(text: string): string {
  let out = text;
  for (const pattern of [...OSC_PATTERNS, ...CSI_PATTERNS, ...ESC_PATTERNS]) {
    out = out.replace(pattern, '');
  }
  return out;
}

function cleanValue(value: unknown): unknown {
  if (typeof value === 'string') return stripAnsi(value);
  if (Array.isArray(value)) return value.map(cleanValue);
  if (value !== null && typeof value === 'object') {
    return Object.fromEntries(
      Object.entries(value).map(([key, item]) => [key, cleanValue(item)]),
    );
  }
  return value;
}

// sanitizeToolResult cleans ANSI codes out of a tool result string.
// Most results are JSON envelopes, so the strings inside the envelope
// (stdout/stderr/content/...) are cleaned and the JSON is rebuilt.
// Non-JSON results fall back to plain text stripping.
export function sanitizeToolResult(raw: string): string {
  try {
    const parsed = JSON.parse(raw);
    if (parsed !== null && typeof parsed === 'object') {
      return JSON.stringify(cleanValue(parsed));
    }
  } catch {
    // Not JSON; fall through to the text-only cleanup below.
  }
  return stripAnsi(raw);
}
