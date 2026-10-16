import * as Diagnostics from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/diagnostics';

// originalConsoleError is captured before any console.error patch is applied
// so devtools output stays readable without re-entering the report hook.
const originalConsoleError = console.error.bind(console);

let consolePatched = false;

/** Cap on the message text the telemetry channel accepts. */
const MESSAGE_LIMIT = 512;
/** Cap on the stack text the telemetry channel accepts. */
const STACK_LIMIT = 2048;

// REDACTIONS are applied to every string that leaves the machine, in
// order. Only the telemetry copy is redacted: devtools still prints the
// values as they were passed, so debugging keeps its detail.
//
// The channel is fed by arbitrary third-party console.error output
// (libraries print request bodies, URLs and file paths), and the Go side
// escapes newlines and truncates but does not sanitise, so shape-based
// redaction here is the only gate before the local log and OTLP export.
const REDACTIONS: [RegExp, string][] = [
  // PEM private key blocks run over many lines; the line-oriented rules
  // below would only see the label. The `$` arm covers a log truncated
  // in the middle of the block.
  [
    /-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)/g,
    '<redacted-private-key>',
  ],
  // An auth header carries a scheme and usually more than one token
  // (`Basic dXNlcjpwYXNz`, `Digest username="u", response="…"`), so the
  // whole value goes, to the end of the line. The name stays: it is the
  // part an operator reads.
  [
    /(\b(?:authorization|proxy-authorization)\b\s*[:=]\s*)[^\n]*/gi,
    '$1<redacted>',
  ],
  // `api_key=…`, `"password": "…"`, `token: abc`: the value behind the
  // name never leaves. A quoted value goes whole; otherwise a scheme
  // plus its argument (`Bearer eyJ…`) or the run of non-space text that
  // follows.
  [
    /(\b(?:bearer|token|api[_-]?key|apikey|secret|password|passwd|passphrase|credential|signature|sig|access[_-]?token|refresh[_-]?token)\b\s*[:=]\s*)("[^"\n]*"|'[^'\n]*'|[A-Za-z][\w-]*\s+[A-Za-z0-9._~+/=-]+|[^\s,;\n]+)/gi,
    '$1<redacted>',
  ],
  // Secrets in a query string keep their name (the useful part) without
  // their value: `?access_token=…`.
  [
    /[?&]([^=&\s]*(?:token|key|secret|password|passwd|sig|signature)[^=&\s]*)=[^&\s"']+/gi,
    '?$1=<redacted>',
  ],
  // Credentials embedded in a URL: https://user:pass@host.
  [/\/\/[^\s/@:]+:[^\s/@]+@/g, '//<redacted>@'],
  // A scheme that names its credential by itself, in text that never
  // spells the header out. The token has to carry an upper-case letter
  // so the prose word "authentication" does not read as one.
  [
    /\b(Bearer|Basic)\s+[A-Za-z0-9._~+/=-]*[A-Z][A-Za-z0-9._~+/=-]*/g,
    '$1 <redacted>',
  ],
  // Digest carries its credential in named parameters instead
  // (`username="u", response="…"`); the scheme takes the rest of the
  // line.
  [/(\b(?:Digest|Negotiate|NTLM)\s+)[^\n]*/gi, '$1<redacted>'],
  // Token prefixes that identify the issuer (OpenAI, GitHub, GitLab,
  // Slack, AWS) and JWTs: recognisable by shape alone.
  [
    /\b(?:sk|pk|rk|ghp|gho|ghs|ghr|glpat|xox[baprs]|AKIA|ASIA)[-_A-Za-z0-9]{16,}\b/g,
    '<redacted-key>',
  ],
  [
    /\beyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\b/g,
    '<redacted-jwt>',
  ],
  // Long hex runs are hashes, ids and secrets; keep the shape, drop the
  // value.
  [/\b[A-Fa-f0-9]{40,}\b/g, '<redacted-hex>'],
  // Absolute paths name the user (and the project) even when nothing
  // secret is in them.
  [/\b[A-Za-z]:\\[^\s"',;]+/g, '<path>'],
  [
    /\/(?:Users|home|private|var|tmp|opt|etc|usr|Volumes|root|srv)\/[^\s"',;]*/g,
    '<path>',
  ],
  [/file:\/\/[^\s"',;]+/g, '<path>'],
];

/**
 * Redacts one diagnostic string by shape and truncates it to `limit`
 * characters. Exported for the tests: this is the last gate before the
 * text reaches user.db and any configured OTLP exporter.
 */
export function redactDiagnostic(text: string, limit: number): string {
  let out = text;
  for (const [pattern, replacement] of REDACTIONS) {
    out = out.replace(pattern, replacement);
  }
  if (out.length > limit) {
    out = `${out.slice(0, limit)}…(+${out.length - limit} chars)`;
  }
  return out;
}

function describe(value: unknown): string {
  if (value instanceof Error) return value.message;
  if (typeof value === 'string') return value;
  try {
    const serialized = JSON.stringify(value);
    return serialized ?? String(value);
  } catch {
    return String(value);
  }
}

function stackOf(value: unknown): string {
  return value instanceof Error ? (value.stack ?? '') : '';
}

interface ErrorReport {
  source: string;
  message: string;
  stack: string;
}

// The reporter can only have one binding call in flight. A bounded queue
// is what keeps a burst of concurrent failures from being dropped: the
// record that arrives during a call used to be discarded outright, so a
// render error followed by a string of unhandled rejections left the log
// with the first entry only. The cap bounds what a stalled channel can
// hold; the oldest record is the one dropped.
const MAX_QUEUE = 16;
const queue: ErrorReport[] = [];

// A failed binding makes the Wails runtime console.error, which this
// module captures, which would enqueue another record — one failure
// would feed itself through the queue forever. After a failure the
// reporter stops sending for a moment and leaves the backlog queued.
const RETRY_DELAY_MS = 5_000;

let retryAfter = 0;
let sending = false;

function pushBounded(report: ErrorReport) {
  if (queue.length >= MAX_QUEUE) queue.shift();
  queue.push(report);
}

function enqueue(report: ErrorReport) {
  pushBounded(report);
  if (sending) return;
  // Not sending: take the head of the queue, which is the oldest record
  // still waiting (an open circuit can leave a backlog behind).
  const next = queue.shift();
  if (next) void drain(next);
}

// drain reports `first` and then everything else that queued up behind
// it, oldest first.
async function drain(first: ErrorReport) {
  sending = true;
  let next: ErrorReport | undefined = first;
  try {
    while (next) {
      if (Date.now() < retryAfter) {
        // The channel is failing: keep this record and the ones behind
        // it for a later attempt instead of spinning on a dead binding.
        pushBounded(next);
        return;
      }
      try {
        await Diagnostics.ReportFrontendError(
          next.source,
          next.message,
          next.stack,
        );
        retryAfter = 0;
      } catch {
        // The diagnostic channel is best-effort: a dead binding must
        // never crash or spam the UI.
        retryAfter = Date.now() + RETRY_DELAY_MS;
      }
      next = queue.shift();
    }
  } finally {
    sending = false;
  }
}

// captureGate decides whether console.error content may leave the
// machine at all. The Diagnostics tab's frontend-sampling switch installs
// it at startup (main.tsx) so the same switch that stops the perf probe
// also stops this channel; until it is installed the channel stays
// closed, which is what keeps a pet-window webview (no switch, no probe)
// from shipping third-party console output.
let captureGate: () => boolean = () => false;

/**
 * Installs the gate for the console.error capture channel; null closes
 * it again. The explicit reportFrontendError path is deliberately not
 * gated: those are the app's own failures, and they are reported
 * redacted.
 */
export function setConsoleErrorCaptureGate(gate: (() => boolean) | null): void {
  captureGate = gate ?? (() => false);
}

// reportFrontendError prints through the real console for devtools and
// forwards one redacted structured record to the Go telemetry pipeline.
export function reportFrontendError(
  source: string,
  error: unknown,
  detail = '',
): void {
  const parts = [describe(error), detail].filter(Boolean);
  originalConsoleError(
    `opencraft frontend error [${source}]:`,
    error,
    detail || undefined,
  );
  enqueue({
    source,
    message: redactDiagnostic(parts.join('\n'), MESSAGE_LIMIT),
    stack: redactDiagnostic(stackOf(error), STACK_LIMIT),
  });
}

// installConsoleErrorCapture wraps console.error so errors emitted by the
// Wails runtime or third-party code (which only console.error) are still
// recorded. App code should use reportFrontendError directly instead: it
// prints via originalConsoleError, so nothing is reported twice — and it
// is not gated by the sampling switch.
export function installConsoleErrorCapture(): void {
  if (consolePatched) return;
  consolePatched = true;
  const original = console.error.bind(console);
  console.error = (...args: unknown[]) => {
    original(...args);
    if (!captureGate()) return;
    const error = args.find((arg): arg is Error => arg instanceof Error);
    const message = args.map(describe).filter(Boolean).join(' ');
    enqueue({
      source: 'console.error',
      message: redactDiagnostic(message, MESSAGE_LIMIT),
      stack: redactDiagnostic(error ? stackOf(error) : '', STACK_LIMIT),
    });
  };
}
