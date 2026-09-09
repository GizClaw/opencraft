import * as Diagnostics from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/diagnostics';

// originalConsoleError is captured before any console.error patch is applied
// so devtools output stays readable without re-entering the report hook.
const originalConsoleError = console.error.bind(console);

let consolePatched = false;

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

let sending = false;

async function send(source: string, message: string, stack: string) {
  // Guard against re-entrancy: a failed binding call makes the Wails runtime
  // console.error, which would otherwise trigger this reporter again and
  // loop while the binding is unavailable.
  if (sending) return;
  sending = true;
  try {
    await Diagnostics.ReportFrontendError(source, message, stack);
  } catch {
    // The diagnostic channel is best-effort: a dead binding must never
    // crash or spam the UI.
  } finally {
    sending = false;
  }
}

// reportFrontendError prints through the real console for devtools and
// forwards one structured record to the Go telemetry pipeline.
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
  void send(source, parts.join('\n'), stackOf(error));
}

// installConsoleErrorCapture wraps console.error so errors emitted by the
// Wails runtime or third-party code (which only console.error) are still
// recorded. App code should use reportFrontendError directly instead: it
// prints via originalConsoleError, so nothing is reported twice.
export function installConsoleErrorCapture(): void {
  if (consolePatched) return;
  consolePatched = true;
  const original = console.error.bind(console);
  console.error = (...args: unknown[]) => {
    original(...args);
    const error = args.find((arg): arg is Error => arg instanceof Error);
    const message = args.map(describe).filter(Boolean).join(' ');
    void send('console.error', message, error ? stackOf(error) : '');
  };
}
