import { bench, describe } from 'vitest';
import { parseUnifiedDiff } from './diff';

describe('diff', () => {
  const lines: string[] = [
    'diff --git a/a.go b/a.go',
    '--- a/a.go',
    '+++ b/a.go',
  ];
  for (let i = 0; i < 200; i++) {
    lines.push(
      `@@ -${i},8 +${i},8 @@`,
      ' context line',
      '-removed line',
      '+added line',
      ' context tail',
    );
  }
  const patch = lines.join('\n');

  bench('parseUnifiedDiff 200 hunks', () => {
    parseUnifiedDiff(patch);
  });
});
