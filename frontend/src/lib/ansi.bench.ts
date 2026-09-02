import { bench, describe } from 'vitest';
import { sanitizeToolResult, stripAnsi } from './ansi';

describe('ansi', () => {
  const noisy =
    '\u001b[31mred\u001b[0m \u001b[1mbold\u001b[0m\n' +
    '\u001b]0;title\u0007' +
    '\u001b[2Jclear\u001b[?25l'.repeat(50);

  bench('stripAnsi', () => {
    stripAnsi(noisy);
  });

  bench('sanitizeToolResult', () => {
    sanitizeToolResult(JSON.stringify({ stdout: noisy, stderr: noisy }));
  });
});
