import { bench, describe } from 'vitest';
import { mergeAppend } from './stream';
import type { MessageView } from './model';

// F-01's bench: a long turn appends each delta to the block it belongs to,
// so the per-append cost must not grow with the block. The two cases run
// the same number of appends into blocks of very different length - with
// the running length the numbers stay in the same league, with a join per
// append they diverge by the ratio of the two block lengths.
describe('mergeAppend', () => {
  const DELTAS = 12000;
  const DELTA = 'the quick brown fox jumps over ';

  const block = (seed: number): MessageView => {
    const msg: MessageView = {
      id: 'm-1',
      role: 'assistant',
      text: '',
      items: [],
      attachments: [],
    };
    for (let i = 0; i < seed; i++) mergeAppend(msg, 'text', DELTA);
    return msg;
  };

  const cold = block(0);
  const hot = block(DELTAS);

  bench(`${DELTAS} deltas into a fresh block`, () => {
    for (let i = 0; i < DELTAS; i++) mergeAppend(cold, 'text', DELTA);
  });

  bench(`${DELTAS} deltas into a block already at ${DELTAS}`, () => {
    for (let i = 0; i < DELTAS; i++) mergeAppend(hot, 'text', DELTA);
  });
});
