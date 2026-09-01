import { describe, expect, it } from 'vitest';
import { sanitizeToolResult, stripAnsi } from './ansi';

const ESC = '\u001b';

describe('stripAnsi', () => {
  it('strips SGR color codes from real escape bytes', () => {
    expect(stripAnsi(`${ESC}[1m${ESC}[30mRUN${ESC}[0m`)).toBe('RUN');
  });

  it('strips SGR color codes from JSON-escaped text', () => {
    expect(stripAnsi('\\u001b[1mRUN\\u001b[0m')).toBe('RUN');
  });

  it('strips cursor movement and terminal title sequences', () => {
    expect(stripAnsi(`${ESC}[?25lhi${ESC}[2K${ESC}]0;title${ESC}\\`)).toBe(
      'hi',
    );
  });
});

describe('sanitizeToolResult', () => {
  it('cleans stdout inside an exec result envelope', () => {
    const raw =
      '{"exit_code":0,"stdout":"\\u001b[1mRUN\\u001b[0m","stderr":""}';
    const out = sanitizeToolResult(raw);
    expect(out).not.toContain('\\u001b');
    expect(JSON.parse(out)).toEqual({
      exit_code: 0,
      stdout: 'RUN',
      stderr: '',
    });
  });

  it('cleans nested strings inside arbitrary result JSON', () => {
    const raw = '{"matches":[{"line":"\\u001b[32mok\\u001b[0m"}]}';
    const out = sanitizeToolResult(raw);
    expect(JSON.parse(out).matches[0].line).toBe('ok');
  });

  it('falls back to text stripping for non-JSON results', () => {
    expect(sanitizeToolResult(`${ESC}[31mfailed${ESC}[0m`)).toBe('failed');
    expect(sanitizeToolResult('\\u001b[31mfailed\\u001b[0m')).toBe('failed');
  });

  it('leaves clean output untouched', () => {
    const raw = '{"exit_code":0,"stdout":"ok","stderr":""}';
    expect(sanitizeToolResult(raw)).toBe(raw);
  });
});
