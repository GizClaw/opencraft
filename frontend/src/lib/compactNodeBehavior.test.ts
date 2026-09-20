// Behaviour gate for the compaction node.
//
// The estimate mirror (compactMirror.test.ts) only pins the pure
// functions. This file runs the real node script
// (internal/foundation/config/assets/graphs/nodes/compact.js) against a
// stub board and checks what it does to the channels: the folded prefix
// must move to the side channel and leave MainChannel, the turn's user
// message must survive every fold, tool call/result pairs must never be
// split, and a failed condensation must keep the conversation exactly
// as it was.
import { readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { COMPACT_SUMMARY_PREFIX } from './compact';

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
const NODE_SOURCE = join(
  REPO_ROOT,
  'internal/foundation/config/assets/graphs/nodes/compact.js',
);
const MAIN = '__main_channel';
const ARCHIVE = 'opencraft.compact_archive';

interface BoardMessage {
  role: string;
  content: { parts: unknown[] };
}

type Vars = Record<string, unknown>;
type Channels = Record<string, BoardMessage[]>;

function textMessage(role: string, text: string): BoardMessage {
  return { role, content: { parts: [{ type: 'text', text }] } };
}

function toolRound(id: string): BoardMessage[] {
  return [
    {
      role: 'assistant',
      content: {
        parts: [{ type: 'tool_call', call: { id, name: 'exec_command' } }],
      },
    },
    {
      role: 'tool',
      content: {
        parts: [{ type: 'tool_result', result: { call_id: id } }],
      },
    },
  ];
}

/** Board stub recording every channel and var the node touches. */
function boardStub(main: BoardMessage[], vars: Vars = {}) {
  const store: Vars = {
    'world.sections.count': 1,
    'world.history.count': 2,
    ...vars,
  };
  const channels: Channels = { [MAIN]: [...main] };
  const board = {
    MAIN_CHANNEL: MAIN,
    getVar: (key: string) => (key in store ? store[key] : undefined),
    setVar: (key: string, value: unknown) => {
      store[key] = value;
    },
    channel: (name: string) => channels[name] ?? [],
    setChannel: (name: string, next: BoardMessage[]) => {
      channels[name] = next;
    },
    appendChannel: (name: string, msg: BoardMessage) => {
      channels[name] = [...(channels[name] ?? []), msg];
    },
  };
  return { board, store, channels };
}

function runNode(board: unknown, maxInputTokens = 1): void {
  const source = readFileSync(NODE_SOURCE, 'utf8');
  // The node's own knobs: `max_compactions` was replaced by a consecutive
  // failure streak plus a per-turn fold budget (see compactNode.test.ts for
  // the stop conditions themselves). This file pins the fold geometry, so
  // it sets the budget explicitly instead of riding the defaults.
  const config = {
    preserve_recent: 2,
    max_consecutive_failures: 3,
    max_folds_per_turn: 3,
    budget_chars: 4096,
  };
  const run = { get_context_id: () => 's-test' };
  const inference = {
    // The default window is absurdly small on purpose: every fixture is
    // past it, so check mode folds without waiting for a measurement.
    routeExplain: () => ({ limits: { max_input_tokens: maxInputTokens } }),
  };
  new Function('board', 'config', 'run', 'inference', source)(
    board,
    config,
    run,
    inference,
  );
}

/** Appends the compact tool result the tools node would have written. */
function appendCompactResult(
  channels: Channels,
  content: string,
  isError = false,
): void {
  channels[MAIN] = [
    ...channels[MAIN],
    {
      role: 'tool',
      content: {
        parts: [
          {
            type: 'tool_result',
            result: { call_id: 'compact-1', content, is_error: isError },
          },
        ],
      },
    },
  ];
}

function patchFor(summary: string): string {
  return JSON.stringify({
    message: textMessage('user', `${COMPACT_SUMMARY_PREFIX}\n${summary}`),
  });
}

/** The conversation the turn exchanged, in order, ignoring summaries. */
function conversationOf(channels: Channels): BoardMessage[] {
  const side = channels[ARCHIVE] ?? [];
  const main = channels[MAIN] ?? [];
  const summary = [COMPACT_SUMMARY_PREFIX];
  return [...side, ...main.slice(1)].filter(
    (m) =>
      !(
        m.role === 'user' &&
        typeof (m.content.parts[0] as { text?: string } | undefined)?.text ===
          'string' &&
        ((m.content.parts[0] as { text: string }).text ?? '').startsWith(
          summary[0] + '\n',
        )
      ),
  );
}

function fixture(): BoardMessage[] {
  return [
    textMessage('system', 'world section'),
    textMessage('user', 'old question'),
    textMessage('assistant', 'old answer'),
    textMessage('user', 'do it'),
    ...toolRound('c1'),
    textMessage('assistant', 'done'),
  ];
}

describe('compact node channel handling', () => {
  it('moves folded history to the side channel and keeps the ask', () => {
    const original = fixture();
    const { board, store, channels } = boardStub(original);
    runNode(board); // check mode: fold the replayed history only

    expect(store['world.compact.pending']).toBe(true);
    expect(store['world.compact.fold_start']).toBe(1);
    expect(store['world.compact.fold_end']).toBe(3); // stops before the ask
    appendCompactResult(channels, patchFor('history summary'));
    runNode(board); // apply mode

    // The history left MainChannel for the side channel.
    expect(channels[MAIN].map((m) => m.content.parts[0])).toEqual([
      { type: 'text', text: 'world section' },
      { type: 'text', text: 'do it' },
      expect.objectContaining({ type: 'tool_call' }),
      expect.objectContaining({ type: 'tool_result' }),
      { type: 'text', text: 'done' },
      { type: 'text', text: `${COMPACT_SUMMARY_PREFIX}\nhistory summary` },
    ]);
    expect(channels[ARCHIVE]).toHaveLength(2);
    expect(store['world.compact.turn_start']).toBe(1);
    expect(store['world.compact.failed_end']).toBe(-1);
    expect(store['world.compact.count']).toBe(1);
  });

  it('folds later rounds without splitting the ask or a tool pair', () => {
    const original = fixture();
    const { board, store, channels } = boardStub(original);
    runNode(board);
    appendCompactResult(channels, patchFor('history summary'));
    runNode(board);
    runNode(board); // second check: the turn's own early round is next

    expect(store['world.compact.fold_start']).toBe(2); // after the ask
    expect(store['world.compact.fold_end']).toBe(4); // assistant call + result
    appendCompactResult(channels, patchFor('combined summary'));
    runNode(board);

    // Side channel: history, then the folded round, tool pair intact.
    expect(channels[ARCHIVE].map((m) => m.role)).toEqual([
      'user',
      'assistant',
      'assistant',
      'tool',
    ]);
    // Main: world section, the protected ask, the kept reply, one summary.
    expect(channels[MAIN].map((m) => m.role)).toEqual([
      'system',
      'user',
      'assistant',
      'user',
    ]);
    // Nothing the turn exchanged was lost or duplicated.
    const conversation = conversationOf(channels);
    for (const m of original.slice(1)) {
      expect(
        conversation.some((c) => JSON.stringify(c) === JSON.stringify(m)),
      ).toBe(true);
    }
    expect(conversation).toHaveLength(original.length - 1);
  });

  it('keeps everything and skips the repeat when condensation fails', () => {
    const original = fixture();
    // A measured prompt inside the window: over the fold threshold but not
    // past the model's limit (the shape where deferring is not an option).
    const { board, store, channels } = boardStub(original, {
      llm_usage: { input_tokens: 95 },
      'world.compact.anchor_len': original.length,
      'world.compact.anchor_epoch': 0,
    });
    runNode(board, 100);
    const foldEnd = store['world.compact.fold_end'];
    appendCompactResult(channels, '', true);
    runNode(board);

    // Conversation and summary unchanged, only the synthetic tail dropped.
    expect(channels[MAIN]).toEqual(original);
    expect(channels[ARCHIVE]).toBeUndefined();
    expect(store['world.compact.failed_end']).toBe(foldEnd);

    // The unchanged boundary is not retried while the request still fits:
    // waiting for the next message moves it and makes a retry meaningful.
    runNode(board);
    expect(store['world.compact.pending']).toBe(false);
    expect(store['world.compact.count']).toBe(1);
    expect(store['tool_pending']).toBe(false);
    expect(channels[MAIN]).toEqual(original);
  });

  it('retries the failed boundary once the prompt is past the window', () => {
    const original = fixture();
    const { board, store, channels } = boardStub(original);
    runNode(board); // check mode, window far too small for this fixture
    const foldEnd = store['world.compact.fold_end'];
    appendCompactResult(channels, '', true);
    runNode(board); // apply mode: the fold failed
    expect(store['world.compact.failed_end']).toBe(foldEnd);

    // Past the window the request cannot be sent as it stands, so the
    // suppression above inverts: the retry is the only way back inside,
    // and the compact tool degrades to a mechanical digest when no model
    // summary arrives, which is what makes the retry worth attempting.
    runNode(board);
    expect(store['world.compact.pending']).toBe(true);
    expect(store['world.compact.fold_end']).toBe(foldEnd);
    // The retry is a fresh synthetic call, not a replay of the failed one.
    const tail = channels[MAIN][channels[MAIN].length - 1];
    expect((tail.content.parts[0] as { call?: { id?: string } }).call?.id).toBe(
      'compact-2',
    );
  });
});
