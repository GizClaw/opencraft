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

  it('drops a quoted JSON key value', () => {
    const json = redactDiagnostic('{"password":"hunter2","user":"ada"}', 512);
    expect(json).not.toContain('hunter2');
    expect(json).toContain('"user":"ada"');

    // An auth header printed from JSON: the value goes to the end of the
    // line, like the header rule does in every other spelling.
    const auth = redactDiagnostic('{"authorization":"Bearer abcdef"}', 512);
    expect(auth).not.toContain('abcdef');
  });

  it('removes a multi-word PEM label with its block', () => {
    // The label is built from its words and the body is computed, so the
    // assertions cannot be satisfied by an input carrying neither - the
    // two halves have to meet.
    const label = 'PGP PRIVATE KEY BLOCK';
    const body = btoa('pgp-private-key-body');
    const pem = [
      `-----BEGIN ${label}-----`,
      body,
      `-----END ${label}-----`,
    ].join('\n');

    const out = redactDiagnostic(pem, 512);

    expect(out).not.toContain(body);
    expect(out).not.toContain(label);
    expect(out).toContain('<redacted-private-key>');
  });

  it('redacts a lower-case scheme with no upper-case in the token', () => {
    // Servers and curl print the scheme's name in lower case, and an
    // opaque token need not carry an upper-case letter or a digit.
    const out = redactDiagnostic('proxy said: bearer abcdef', 512);
    expect(out).not.toContain('abcdef');
    expect(out).toContain('bearer <redacted>');
  });

  it('redacts the scheme in every spelling', () => {
    for (const scheme of ['Bearer', 'bearer', 'BEARER', 'BaSiC', 'basic']) {
      const out = redactDiagnostic(`${scheme} 4f9c2a1b8e7d`, 512);
      expect(out).not.toContain('4f9c2a1b8e7d');
      expect(out).toContain(`${scheme} <redacted>`);
    }
  });

  it('leaves the word a sentence puts after the scheme', () => {
    for (const text of [
      'Basic authentication required',
      'Basic auth required',
      'Bearer token missing',
      'Basic credentials expired',
    ]) {
      expect(redactDiagnostic(text, 512)).toBe(text);
    }
  });

  it('takes an escaped quote with the JSON value it belongs to', () => {
    // A `"` inside a JSON string is escaped, so a value arm that stops
    // at the first quote leaves the rest of the value behind - and the
    // keys that follow are a different field, not the tail of this one.
    const out = redactDiagnostic('{"password":"a\\"bcd","user":"ada"}', 512);
    expect(out).not.toContain('bcd');
    expect(out).toContain('"user":"ada"');
  });

  it('lets a path go as a path, not as the scheme token', () => {
    // The drive letter is a token-shaped word; if the scheme rule took
    // it, the rest of the path would have nothing left to recognise it
    // by. Windows spells the separator both ways.
    for (const path of [
      'C:\\private\\key.pem',
      'C:/private/key.pem',
      '/Users/ada/key.pem',
    ]) {
      const out = redactDiagnostic(`basic ${path}`, 512);
      expect(out).not.toContain('key.pem');
      expect(out).toContain('basic <path>');
    }
  });

  it('redacts a scheme token the shape rules would only partly rewrite', () => {
    // A 40-char hex run with more token behind it: the hex rule would
    // replace the run and leave the tail, which no longer looks like a
    // token to the scheme rule. The scheme rule therefore runs before
    // the shape rules.
    const out = redactDiagnostic(
      'Bearer ABCDEF0123456789abcdef0123456789abcdef.b3f1c2',
      512,
    );
    expect(out).not.toContain('b3f1c2');
    expect(out).toContain('Bearer <redacted>');
  });

  it('leaves a WWW-Authenticate answer readable', () => {
    const header =
      'WWW-Authenticate: Bearer error="invalid_token", error_description="expired", error_uri="https://api.example/errors#invalid_token"';
    expect(redactDiagnostic(header, 512)).toBe(header);
  });

  it('folds case when it decides a word is prose', () => {
    for (const text of [
      'BEARER AUTHENTICATION REQUIRED',
      'Basic TOKEN',
      'bearer Credentials',
    ]) {
      expect(redactDiagnostic(text, 512)).toBe(text);
    }
  });

  it('redacts a credential written as name=value behind a scheme', () => {
    // The word after the scheme is a name here, not prose: the value
    // behind the `=` is the credential, and the name rule above does not
    // know these names. Keeping `=` in the token class is what makes the
    // two one token.
    for (const text of [
      'Basic credentials=dXNlcjpwYXNz',
      'Basic auth=dXNlcjpwYXNz',
      'Bearer access=dXNlcjpwYXNz',
    ]) {
      const out = redactDiagnostic(text, 512);
      expect(out).not.toContain('dXNlcjpwYXNz');
    }
  });

  it('drops the padding with the token it belongs to', () => {
    expect(redactDiagnostic('Bearer dXNlcjpwYXNz==', 512)).toBe(
      'Bearer <redacted>',
    );
  });

  it('records the costs the rules choose', () => {
    // Not accidents: the scan resumes after a prose word, so a credential
    // that follows one at a space is not seen again (the rules above ran
    // already, and the shape rules match shapes); a spaced separator is
    // the same gap (the name rule does not know `auth`); and an adjective
    // `basic` in prose takes the next word with it. Each would fail here
    // if someone changed the trade, which is the point.
    expect(
      redactDiagnostic('sent without Bearer header, token 4f9c2a1b8e7d', 512),
    ).toContain('4f9c2a1b8e7d');
    expect(redactDiagnostic('Basic auth = 4f9c2a1b8e7d', 512)).toContain(
      '4f9c2a1b8e7d',
    );
    expect(redactDiagnostic('basic usage', 512)).toBe('basic <redacted>');
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
