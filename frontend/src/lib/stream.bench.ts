import { bench, describe } from 'vitest';
import { groupToolCalls } from './stream';
import type { AssistantItem } from './store';

describe('stream', () => {
  const items: AssistantItem[] = [];
  for (let i = 0; i < 500; i++) {
    items.push({
      kind: 'tool_call',
      id: `t-${i}`,
      tool: { id: `t-${i}`, name: 'exec_command', args: '{}', status: 'done' },
    });
    items.push({ kind: 'text', id: `x-${i}`, text: 'ok' });
  }

  bench('groupToolCalls 1000 items', () => {
    groupToolCalls(items);
  });
});
