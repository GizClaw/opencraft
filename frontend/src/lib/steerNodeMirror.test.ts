// Node-level gate for the steer node.
//
// The host queues what the user submits while a turn runs (flowcraft's
// Turn.Steer, surfaced to scripts as host.drainSteer by
// core/graph/nodes/script/bridge_host.go) and the node
// (internal/foundation/config/assets/graphs/nodes/steer.js) is the
// boundary that appends it to the MainChannel. Two of its rules are
// silent when broken — appending at a boundary that is not a tool
// result, and draining while a compaction fold owns the channel tail —
// so the real node runs here against a stubbed board and host, the same
// way worldNodeMirror.test.ts runs the world node against Go's fixture.
import { readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
const NODES_DIR = join(
  REPO_ROOT,
  'internal/foundation/config/assets/graphs/nodes',
);
const NODE_SOURCE = join(NODES_DIR, 'steer.js');
const COMPACT_SOURCE = join(NODES_DIR, 'compact.js');

// The fold-in-flight gate is one bare string shared by two nodes: the
// compact node raises it while a fold owns the channel tail and clears
// it once the fold settled, and the steer node must read it — appending
// mid-fold would read as a failed fold and cut the live messages out of
// the channel. Both sides are derived from the node sources instead of
// restated here, so a rename on either side fails the extraction rather
// than silently opening the guard.
const compactSource = readFileSync(COMPACT_SOURCE, 'utf8');
const flagNames = (source: string, pattern: RegExp) =>
  new Set([...source.matchAll(pattern)].map((m) => m[1]));
const raised = flagNames(
  compactSource,
  /board\.setVar\(\s*"([^"]+)"\s*,\s*true\s*\)/g,
);
const cleared = flagNames(
  compactSource,
  /board\.setVar\(\s*"([^"]+)"\s*,\s*false\s*\)/g,
);
const readBack = flagNames(compactSource, /board\.getVar\(\s*"([^"]+)"\s*\)/g);
// A gate that marks a span is raised, cleared and read back again; the
// compact node has exactly one such flag.
const foldGates = [...raised].filter(
  (name) => cleared.has(name) && readBack.has(name),
);
const compactGate = foldGates.length === 1 ? foldGates[0] : undefined;
const steerGate = /board\.getVar\(\s*"([^"]+)"\s*\)/.exec(
  readFileSync(NODE_SOURCE, 'utf8'),
)?.[1];

interface WireMessage {
  role: string;
  content: { parts: unknown[] };
}

interface RunOptions {
  vars?: Record<string, unknown>;
  channel?: WireMessage[];
  steered?: WireMessage[];
}

function textMessage(role: string, text: string): WireMessage {
  return { role, content: { parts: [{ type: 'text', text }] } };
}

function textOf(message: WireMessage): string {
  const part = message.content.parts[0] as { text?: string };
  return part?.text ?? '';
}

/** Board + host stub running the real node, like the runtime's bridges. */
function runSteerNode({ vars = {}, channel = [], steered = [] }: RunOptions) {
  let queue = [...steered];
  let drains = 0;
  const board = {
    MAIN_CHANNEL: 'main',
    getVar: (key: string) => vars[key],
    channel: (_kind: string) => channel,
    appendChannel: (_kind: string, message: WireMessage) => {
      channel.push(message);
    },
  };
  const host = {
    // Take-all, like the runtime's Turn.DrainSteer: never the same
    // message twice, an empty array when nothing is queued.
    drainSteer: () => {
      drains += 1;
      const out = queue;
      queue = [];
      return out;
    },
  };
  new Function('board', 'host', readFileSync(NODE_SOURCE, 'utf8'))(board, host);
  return { channel, drains: () => drains, left: () => queue };
}

describe('steer node delivers at the round boundary', () => {
  it('reads the same fold-in-flight gate the compact node writes', () => {
    // Both extractions above are renames-sensitive on purpose: if
    // either node stops naming the gate the other reads, this fails
    // instead of leaving the guard switched off.
    expect(compactGate).toBeTruthy();
    expect(steerGate).toBe(compactGate);
  });

  it('appends every drained message in order after the tool result', () => {
    const { channel, drains } = runSteerNode({
      channel: [
        textMessage('user', 'ask'),
        { role: 'assistant', content: { parts: [{ type: 'tool_call' }] } },
        { role: 'tool', content: { parts: [{ type: 'tool_result' }] } },
      ],
      steered: [
        textMessage('user', 'first correction'),
        textMessage('user', 'second correction'),
      ],
    });

    expect(channel.map((m) => m.role)).toEqual([
      'user',
      'assistant',
      'tool',
      'user',
      'user',
    ]);
    expect(textOf(channel[3])).toBe('first correction');
    expect(textOf(channel[4])).toBe('second correction');
    expect(drains()).toBe(1);
  });

  it('leaves the channel alone when nothing was steered', () => {
    const before: WireMessage[] = [
      textMessage('user', 'ask'),
      { role: 'tool', content: { parts: [] } },
    ];
    const { channel, drains } = runSteerNode({ channel: before });

    expect(channel).toEqual(before);
    expect(drains()).toBe(1);
  });

  it('keeps the queue while a compaction fold is in flight', () => {
    const { channel, drains, left } = runSteerNode({
      vars: { [compactGate as string]: true },
      channel: [
        textMessage('user', 'ask'),
        { role: 'tool', content: { parts: [] } },
      ],
      steered: [textMessage('user', 'late correction')],
    });

    expect(channel).toHaveLength(2);
    expect(drains()).toBe(0);
    expect(left()).toHaveLength(1);
  });

  it('keeps the queue when the boundary is not a tool result', () => {
    const { channel, drains, left } = runSteerNode({
      channel: [textMessage('user', 'ask')],
      steered: [textMessage('user', 'too early')],
    });

    expect(channel).toHaveLength(1);
    expect(drains()).toBe(0);
    expect(left()).toHaveLength(1);
  });
});
