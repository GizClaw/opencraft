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
    const token = 'sk-' + 'abcdefghijklmnopqrstuvwx';
    const out = redactDiagnostic(
      `Authorization: Bearer ${token}\napi_key=abcd1234`,
      512,
    );
    expect(out).not.toContain(token);
    expect(out).not.toContain('abcd1234');
    expect(out).toContain('Authorization: <redacted>');
    expect(out).toContain('api_key=<redacted>');
  });

  // A header value carries a scheme and often more than one token; the
  // name rule alone left everything after the first token in the log.
  it('drops a Basic or Digest header value whole', () => {
    const basic = btoa('user:password');
    const outBasic = redactDiagnostic(`Authorization: Basic ${basic}`, 512);
    expect(outBasic).not.toContain(basic);
    expect(outBasic).toContain('Authorization: <redacted>');

    const digest = '9c1a'.repeat(4);
    const outDigest = redactDiagnostic(
      `Authorization: Digest username="ada", response="${digest}"`,
      512,
    );
    expect(outDigest).not.toContain(digest);
    expect(outDigest).not.toContain('username="ada"');

    // The scheme alone is a credential too, with no header name in
    // front of it.
    const bare = redactDiagnostic(`proxy said: Basic ${basic}`, 512);
    expect(bare).not.toContain(basic);
  });

  it('drops a PEM private key block', () => {
    const body = btoa('openssh-private-key-body');
    const out = redactDiagnostic(
      `-----BEGIN OPENSSH PRIVATE KEY-----\n${body}\n-----END OPENSSH PRIVATE KEY-----`,
      1024,
    );
    expect(out).not.toContain(body);
    expect(out).not.toContain('PRIVATE KEY');
  });

  it('leaves prose about the schemes alone', () => {
    const out = redactDiagnostic(
      'request failed: Basic authentication required',
      512,
    );
    expect(out).toContain('Basic authentication required');
  });

  it('drops a quoted JSON key value and a multi-word PEM label', () => {
    const json = redactDiagnostic('{"password":"hunter2","user":"ada"}', 512);
    expect(json).not.toContain('hunter2');
    expect(json).toContain('"user":"ada"');

    // An auth header printed from JSON: the value goes to the end of the
    // line, like the header rule does in every other spelling.
    const auth = redactDiagnostic('{"authorization":"Bearer abcdef"}', 512);
    expect(auth).not.toContain('abcdef');

    const pgp = [
      '-----BEGIN PGP PRIVATE KEY BLOCK-----',
      'AAAB',
      '-----END PGP PRIVATE KEY BLOCK-----',
    ].join('\n');
    const out = redactDiagnostic(pgp, 512);
    expect(out).not.toContain('AAAB');
    expect(out).toContain('<redacted-private-key>');
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
