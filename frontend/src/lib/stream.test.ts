import { describe, expect, it } from 'vitest';
import { groupToolCalls, isPlanCall, visibleStreamItems } from './stream';
import type { AssistantItem, MessageView } from './store';

function text(id: string, body = 'hello'): AssistantItem {
  return { kind: 'text', id, text: body };
}

function reasoning(id: string): AssistantItem {
  return { kind: 'reasoning', id, text: 'thinking…' };
}

function tool(
  id: string,
  name: string,
  status: 'running' | 'done' | 'error' = 'done',
  args = '{}',
): AssistantItem {
  return { kind: 'tool_call', id, tool: { id, name, args, status } };
}

function planCall(args: string, status: 'running' | 'done' = 'done') {
  return tool('plan', 'update_plan', status, args);
}

function msg(id: string, items: AssistantItem[]): MessageView {
  return { id, role: 'assistant', text: '', items, attachments: [] };
}

describe('groupToolCalls', () => {
  it('returns an empty list for empty input', () => {
    expect(groupToolCalls([])).toEqual([]);
  });

  it('passes non-tool items through unchanged', () => {
    const items = [reasoning('r1'), text('t1')];
    expect(groupToolCalls(items)).toEqual(items);
  });

  it('groups consecutive tool calls into one array', () => {
    const a = tool('a', 'exec_command');
    const b = tool('b', 'exec_command');
    const t = text('t');
    expect(groupToolCalls([a, b, t])).toEqual([[a, b], t]);
  });

  it('does not group tool calls separated by a non-tool item', () => {
    const a = tool('a', 'exec_command');
    const t = text('t');
    const b = tool('b', 'exec_command');
    expect(groupToolCalls([a, t, b])).toEqual([[a], t, [b]]);
  });

  it('keeps a single tool call as a one-element group', () => {
    const a = tool('a', 'exec_command');
    expect(groupToolCalls([a])).toEqual([[a]]);
  });

  it('never groups apply_patch or write_file', () => {
    const patch = tool('p', 'apply_patch');
    const write = tool('w', 'write_file');
    const exec = tool('e', 'exec_command');
    expect(groupToolCalls([patch, write, exec])).toEqual([
      patch,
      write,
      [exec],
    ]);
  });

  it('drops update_plan calls entirely', () => {
    const p = planCall('{"plan":[]}');
    const exec = tool('e', 'exec_command');
    expect(groupToolCalls([p, exec])).toEqual([[exec]]);
  });

  it('bridges tool groups across a dropped plan call', () => {
    const a = tool('a', 'exec_command');
    const p = planCall('{"plan":[]}');
    const b = tool('b', 'exec_command');
    expect(groupToolCalls([a, p, b])).toEqual([[a, b]]);
  });

  it('handles a mixed burst correctly', () => {
    const a = tool('a', 'exec_command');
    const b = tool('b', 'exec_command');
    const patch = tool('p', 'apply_patch');
    const c = tool('c', 'exec_command');
    const d = tool('d', 'exec_command');
    expect(groupToolCalls([a, b, patch, c, d])).toEqual([
      [a, b],
      patch,
      [c, d],
    ]);
  });
});

describe('isPlanCall', () => {
  it('detects update_plan tool calls', () => {
    expect(isPlanCall(planCall('{}'))).toBe(true);
    expect(isPlanCall(tool('x', 'exec_command'))).toBe(false);
    expect(isPlanCall(text('t'))).toBe(false);
  });
});

describe('visibleStreamItems', () => {
  it('flattens messages in arrival order', () => {
    const stream = [
      msg('m1', [text('a'), tool('b', 'exec_command')]),
      msg('m2', [reasoning('c')]),
    ];
    expect(visibleStreamItems(stream).map((i) => i.id)).toEqual([
      'a',
      'b',
      'c',
    ]);
  });

  it('drops update_plan calls', () => {
    const stream = [msg('m1', [planCall('{"plan":[]}'), text('a')])];
    expect(visibleStreamItems(stream).map((i) => i.id)).toEqual(['a']);
  });

  it('returns an empty list for an empty stream', () => {
    expect(visibleStreamItems([])).toEqual([]);
  });
});
