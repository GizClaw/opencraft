import { bench, describe } from 'vitest';
import { emptyConv, settleConversation, type MessageView } from './model';

// F-02's bench: every stream flush settles the conversation, so the call
// must not walk the 800 messages it is not changing. The second case adds
// one row before settling - that is the shape a live turn has, and it is
// the path the running footprint is meant to keep incremental.
describe('settleConversation', () => {
  const row = (i: number): MessageView => ({
    id: `m-${i}`,
    role: 'assistant',
    text: '',
    items: [{ kind: 'text', id: `x-${i}`, text: 'a paragraph of answer text' }],
    attachments: [],
  });

  const full = emptyConv();
  full.messages = Array.from({ length: 800 }, (_, i) => row(i));
  const settled = settleConversation(full);

  bench('800 messages, nothing changed', () => {
    settleConversation(settled);
  });

  bench('800 messages, one row appended', () => {
    settleConversation({
      ...settled,
      messages: [...settled.messages, row(801)],
    });
  });
});
