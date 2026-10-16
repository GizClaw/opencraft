import { beforeEach, describe, expect, it, vi } from 'vitest';

const bindingMock = vi.hoisted(() => ({ ReportFrontendError: vi.fn() }));
const consoleMock = vi.hoisted(() => ({ error: vi.fn() }));

vi.mock(
  '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/diagnostics',
  () => bindingMock,
);

import {
  installConsoleErrorCapture,
  redactDiagnostic,
  reportFrontendError,
  setConsoleErrorCaptureGate,
} from './frontendErrors';

// Installing the capture replaces console.error for the rest of the
// file and prints through the console it captured, so the stub goes in
// first and stays.
console.error = consoleMock.error;
installConsoleErrorCapture();

beforeEach(() => {
  vi.clearAllMocks();
  bindingMock.ReportFrontendError.mockResolvedValue(undefined);
  setConsoleErrorCaptureGate(() => true);
});

// F-32: whatever a third-party console.error prints travels to user.db
// and to any configured OTLP exporter, so it is redacted by shape before
// it leaves the page.
describe('redactDiagnostic', () => {
  it('drops absolute paths', () => {
    const out = redactDiagnostic(
      'failed to read /Users/ada/Code/opencraft/notes.md',
      512,
    );
    expect(out).not.toContain('/Users/ada');
    expect(out).toContain('<path>');
  });

  it('drops credentials by name and by shape', () => {
    const out = redactDiagnostic(
      'Authorization: Bearer sk-abcdefghijklmnopqrstuvwx\napi_key=abcd1234',
      512,
    );
    expect(out).not.toContain('sk-abcdefghijklmnopqrstuvwx');
    expect(out).not.toContain('abcd1234');
  });

  it('truncates a long payload', () => {
    const out = redactDiagnostic('x'.repeat(900), 100);
    expect(out.length).toBeLessThan(200);
    expect(out).toContain('+800 chars');
  });
});

describe('console.error capture', () => {
  it('keeps every concurrent error instead of dropping the ones behind the first', async () => {
    let release: () => void = () => {};
    bindingMock.ReportFrontendError.mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          release = resolve;
        }),
    );

    console.error('first failure');
    console.error('second failure /Users/ada/secret.txt');
    console.error('third failure');
    release();

    await vi.waitFor(() =>
      expect(bindingMock.ReportFrontendError).toHaveBeenCalledTimes(3),
    );
    expect(
      bindingMock.ReportFrontendError.mock.calls.map((call) => call[1]),
    ).toEqual(['first failure', 'second failure <path>', 'third failure']);
  });

  it('sends nothing while the sampling switch has not installed the gate', () => {
    setConsoleErrorCaptureGate(null);
    console.error('leaked /Users/ada/secret.txt');

    expect(bindingMock.ReportFrontendError).not.toHaveBeenCalled();
  });

  it('still forwards the app own errors, redacted, with the gate closed', async () => {
    setConsoleErrorCaptureGate(null);
    reportFrontendError('window-error', new Error('boom /Users/ada/x.ts'));

    await vi.waitFor(() =>
      expect(bindingMock.ReportFrontendError).toHaveBeenCalledTimes(1),
    );
    expect(bindingMock.ReportFrontendError.mock.calls[0][1]).toBe(
      'boom <path>',
    );
  });
});
