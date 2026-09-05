import { describe, expect, it } from 'vitest';
import { coalesceStreamEvents, groupToolCalls, isPlanCall } from './stream';
import type { AssistantItem } from './store';
import type { UIEvent } from './types';

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

describe('groupToolCalls', () => {
  it('returns an empty list for empty input', () => {
    expect(groupToolCalls([])).toEqual([]);
  });

  it('drops reasoning traces and passes visible non-tool items through', () => {
    const items = [reasoning('r1'), text('t1')];
    expect(groupToolCalls(items)).toEqual([text('t1')]);
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

  it('merges tool calls separated only by hidden reasoning', () => {
    const a = tool('a', 'exec_command');
    const b = tool('b', 'exec_command');
    expect(groupToolCalls([a, reasoning('r1'), b])).toEqual([[a, b]]);
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

describe('coalesceStreamEvents', () => {
  function textEvent(
    conversationID: string,
    runID: string,
    text: string,
  ): UIEvent {
    return {
      type: 'stream',
      data: {
        conversation_id: conversationID,
        run_id: runID,
        delta: {
          type: 'part',
          part: { type: 'text', text },
        },
      },
    };
  }

  it('keeps text/reasoning/tool phase order inside one run', () => {
    const events: UIEvent[] = [
      textEvent('s-1', 'r-1', 'a'),
      textEvent('s-1', 'r-1', 'b'),
      {
        type: 'stream',
        data: {
          conversation_id: 's-1',
          run_id: 'r-1',
          delta: {
            type: 'part',
            part: { type: 'reasoning', text: 'think' },
          },
        },
      },
      {
        type: 'stream',
        data: {
          conversation_id: 's-1',
          run_id: 'r-1',
          delta: {
            type: 'part',
            part: {
              type: 'tool_call',
              call: { id: 'c-1', name: 'exec_command', arguments: {} },
            },
          },
        },
      },
      textEvent('s-1', 'r-1', 'c'),
      textEvent('s-1', 'r-1', 'd'),
    ];

    const out = coalesceStreamEvents(events);
    const kinds = out.map((ev) => {
      const part = (ev.data as { delta: { part: { type: string } } }).delta
        .part;
      return part.type;
    });
    expect(kinds).toEqual(['text', 'reasoning', 'tool_call', 'text']);
    const textParts = out.filter((ev) => {
      const part = (ev.data as { delta: { part: { type: string } } }).delta
        .part;
      return part.type === 'text';
    });
    expect(
      textParts.map(
        (ev) =>
          (ev.data as { delta: { part: { text: string } } }).delta.part.text,
      ),
    ).toEqual(['ab', 'cd']);
  });

  it('stress-coalesces thousands of interleaved deltas without loss', () => {
    const conversations = ['s-1', 's-2', 's-3', 's-4'];
    const runs = ['r-1', 'r-2', 'r-3', 'r-4'];
    const events: UIEvent[] = [];
    const expected = new Map<string, string>();
    const rounds = 20;
    const chunksPerRun = 25;
    for (let round = 0; round < rounds; round++) {
      for (let i = 0; i < conversations.length; i++) {
        const conversationID = conversations[i];
        const runID = runs[i];
        for (let c = 0; c < chunksPerRun; c++) {
          const text = `${round}:${conversationID}:${c},`;
          events.push(textEvent(conversationID, runID, text));
          expected.set(
            conversationID,
            (expected.get(conversationID) ?? '') + text,
          );
        }
      }
    }

    const out = coalesceStreamEvents(events);
    const got = new Map<string, string>();
    const order: string[] = [];
    for (const ev of out) {
      const data = ev.data as {
        conversation_id: string;
        delta: { type: string; part?: { type?: string; text?: string } };
      };
      if (data.delta.type !== 'part' || data.delta.part?.type !== 'text') {
        continue;
      }
      const conversationID = data.conversation_id;
      got.set(
        conversationID,
        (got.get(conversationID) ?? '') + (data.delta.part.text ?? ''),
      );
      order.push(conversationID);
    }

    expect(out).toHaveLength(rounds * conversations.length);
    expect(order).toEqual(
      Array.from({ length: rounds }, () => conversations).flat(),
    );
    for (const conversationID of conversations) {
      expect(got.get(conversationID)).toBe(expected.get(conversationID));
    }
  });
});
