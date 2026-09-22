// Cross-language gate for the compaction node.
//
// Go renders the prompt and the harness measures it; the graph's compact
// node decides whether to fold it into a summary. The node cannot import
// Go, so the board-var contract is pinned here against a stub board:
//
//   - llm_usage + world.compact.anchor_len/anchor_epoch: this turn's
//     previous call measured the prompt this node is sizing;
//   - world.usage.anchor: the previous turn's last call, persisted in the
//     session store and re-injected by the world-state prepare hook;
//   - world.compact.{count,epoch,fail_streak}: the fold bookkeeping that
//     decides when folding stops and the model gets told.
//
// The behaviors worth failing a build over: never fold on a character
// estimate alone, never fold past a fold that just failed, and never stop
// folding silently.
import { readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
const NODE_SOURCE = join(
  REPO_ROOT,
  'internal/foundation/config/assets/graphs/nodes/compact.js',
);
const NOTICE = 'Context notice:';
/** The budget the node is judged against in every case below. */
const MAX_INPUT = 10000;
/** World-state sections the world node prepends (stubs: only the count matters). */
const SECTIONS = 3;

interface BoardMessage {
  role: string;
  content: { parts: unknown[] };
}

/**
 * The routing var the graph's edges test (`tool_pending == false` /
 * `== true` in assistant.yaml). A node that writes it under any other
 * name routes the turn into the graph's default branch and the run ends
 * before the model is ever called.
 */
const TOOL_PENDING = 'tool_pending';

interface NodeResult {
  vars: Record<string, unknown>;
  channel: BoardMessage[];
  /** The side channel folded messages move onto before leaving the model view. */
  archive: BoardMessage[];
}

function text(role: string, body: string): BoardMessage {
  return { role, content: { parts: [{ type: 'text', text: body }] } };
}

/** A tool result big enough that the character estimate is over budget. */
function bigToolResult(): BoardMessage {
  return text('tool', 'x'.repeat(60_000));
}

/**
 * A tool result whose estimate lands between the fold threshold (85%) and
 * the whole window: the case where a measurement decides, and a character
 * estimate alone is not allowed to.
 */
function mediumToolResult(): BoardMessage {
  return text('tool', 'x'.repeat(36_000));
}

function sections(): BoardMessage[] {
  return [
    text('system', 'rules'),
    text('system', 'environment'),
    text('user', 'AGENTS.md'),
  ];
}

/**
 * A mid-turn channel: world sections, `history` replayed rounds, the
 * turn's ask, the call it made, and the tool result that came back. That
 * is the shape the node sees from the second round of a turn on.
 */
function midTurn(
  history: number,
  toolResult = bigToolResult(),
): BoardMessage[] {
  const msgs: BoardMessage[] = [...sections()];
  for (let i = 0; i < history; i++) {
    msgs.push(text('user', `question ${i}`));
    msgs.push(text('assistant', `answer ${i}`));
  }
  msgs.push(text('user', 'the current ask'));
  msgs.push({
    role: 'assistant',
    content: {
      parts: [
        {
          type: 'tool_call',
          call: {
            id: 'call-1',
            name: 'read_file',
            arguments: '{"path":"a.go"}',
          },
        },
      ],
    },
  });
  msgs.push(toolResult);
  return msgs;
}

/** The synthetic pair the node appends when it asks for a fold. */
function syntheticFold(failed: boolean): BoardMessage[] {
  // The compact tool answers with a Patch whose text content is the JSON
  // message to insert (compact.encodePatch in Go).
  const summary = 'folded into a summary';
  const patch = JSON.stringify({
    message: {
      role: 'user',
      content: {
        parts: [{ type: 'text', text: `Summary prefix\n${summary}` }],
      },
    },
  });
  return [
    {
      role: 'assistant',
      content: {
        parts: [
          {
            type: 'tool_call',
            call: { id: 'compact-1', name: 'compact', arguments: {} },
          },
        ],
      },
    },
    {
      role: 'tool',
      content: {
        parts: [
          {
            type: 'tool_result',
            result: failed
              ? { call_id: 'compact-1', is_error: true, content: { parts: [] } }
              : {
                  call_id: 'compact-1',
                  content: { parts: [{ type: 'text', text: patch }] },
                },
          },
        ],
      },
    },
  ];
}

interface RunOptions {
  channel: BoardMessage[];
  history?: number;
  vars?: Record<string, unknown>;
}

function runNode(opts: RunOptions): NodeResult {
  const vars: Record<string, unknown> = {
    'world.sections.count': SECTIONS,
    'world.history.count': opts.history ?? 8,
    // Seeded so the node does not ask the router: the test states the
    // budget it wants to be judged against.
    'world.compact.max_input_tokens': MAX_INPUT,
    ...(opts.vars ?? {}),
  };
  const channels: Record<string, BoardMessage[]> = {
    main: structuredClone(opts.channel),
  };
  const board = {
    MAIN_CHANNEL: 'main',
    getVar: (key: string) => vars[key],
    setVar: (key: string, value: unknown) => {
      vars[key] = value;
    },
    channel: (name: string) => channels[name] ?? [],
    setChannel: (name: string, next: BoardMessage[]) => {
      channels[name] = next;
    },
    // Mirrors core's appendChannel bridge: one message object, or an
    // array of them appended as one batch.
    appendChannel: (name: string, msg: BoardMessage | BoardMessage[]) => {
      const batch = Array.isArray(msg) ? msg : [msg];
      channels[name] = [...(channels[name] ?? []), ...batch];
    },
  };
  const run = { get_context_id: () => 's-test' };
  new Function('board', 'run', 'config', readFileSync(NODE_SOURCE, 'utf8'))(
    board,
    run,
    {},
  );
  return {
    vars,
    channel: channels.main,
    archive: channels['opencraft.compact_archive'] ?? [],
  };
}

function foldRequested(result: NodeResult): boolean {
  for (const msg of result.channel) {
    for (const part of msg.content.parts) {
      const typed = part as { type?: string; call?: { name?: string } };
      if (typed.type === 'tool_call' && typed.call?.name === 'compact') {
        return true;
      }
    }
  }
  return false;
}

function noticeText(result: NodeResult): string {
  for (const msg of result.channel) {
    const body = msg.content.parts
      .map((p) => (p as { text?: string }).text ?? '')
      .join('');
    if (body.startsWith(NOTICE)) return body;
  }
  return '';
}

describe('compaction node', () => {
  it('does not fold a prompt that fits', () => {
    const result = runNode({
      channel: midTurn(4, text('tool', 'short output')),
    });
    expect(foldRequested(result)).toBe(false);
    expect(result.vars['world.compact.pending']).toBeUndefined();
    // The routing var the graph edges read is cleared, under the name the
    // graph definition tests.
    expect(result.vars[TOOL_PENDING]).toBe(false);
    expect(noticeText(result)).toBe('');
  });

  it('defers to the provider measurement instead of folding on an estimate', () => {
    // The character estimate is over the threshold, but nothing has
    // measured this prompt yet: the node records the shape and waits for
    // the provider's own number next round.
    const channel = midTurn(4, mediumToolResult());
    const result = runNode({ channel });
    expect(foldRequested(result)).toBe(false);
    expect(result.vars['world.compact.anchor_len']).toBe(result.channel.length);
    expect(result.vars['world.compact.anchor_epoch']).toBe(0);
  });

  it('folds when this turn already measured the prompt over budget', () => {
    const channel = midTurn(4, mediumToolResult());
    const result = runNode({
      channel,
      vars: {
        // The call before this one saw a two-message-shorter channel and
        // was billed 9500 tokens: over 85% of the window.
        llm_usage: { input_tokens: 9500 },
        'world.compact.anchor_len': channel.length - 2,
        'world.compact.anchor_epoch': 0,
      },
    });
    expect(foldRequested(result)).toBe(true);
    expect(result.vars['world.compact.pending']).toBe(true);
    expect(result.vars[TOOL_PENDING]).toBe(true);
    expect(result.vars['world.compact.fold_start']).toBe(SECTIONS);
    // The newest rounds stay raw (preserve_recent), so the fold covers the
    // older part of the replayed history and stops before the ask.
    expect(result.vars['world.compact.fold_end']).toBe(channel.length - 10);
  });

  it('uses the previous turn anchor for the prompt it still covers', () => {
    const channel = midTurn(4, mediumToolResult());
    // The measurement covered the channel's first `anchored_messages`
    // messages (the shape a full-history replay keeps growing), so the
    // prompt is that measurement plus the estimated tail.
    const covered = runNode({
      channel,
      vars: {
        'world.usage.anchor': {
          input_tokens: 9500,
          anchored_messages: channel.length - 2,
          compact_count: 3,
        },
      },
    });
    expect(foldRequested(covered)).toBe(true);

    // A measurement that does NOT fit inside this channel (a bounded
    // memory window slid forward, so the measured prefix is gone) is not a
    // floor: the character estimate decides, and an estimate alone waits
    // for the provider's own number.
    const slid = runNode({
      channel,
      vars: {
        'world.usage.anchor': {
          input_tokens: 9500,
          anchored_messages: channel.length + 40,
          compact_count: 3,
        },
      },
    });
    expect(foldRequested(slid)).toBe(false);
  });

  it('stops folding after consecutive failures and tells the model', () => {
    const channel = midTurn(4);
    const result = runNode({
      channel,
      vars: {
        llm_usage: { input_tokens: 9900 },
        'world.compact.anchor_len': channel.length - 2,
        'world.compact.anchor_epoch': 0,
        'world.compact.fail_streak': 3,
      },
    });
    expect(foldRequested(result)).toBe(false);
    expect(noticeText(result)).toContain(NOTICE);
    expect(noticeText(result)).toContain('consecutive failures');
    expect(result.vars['world.compact.notice_sent']).toBe(true);
  });

  it('stops folding when the per-turn fold budget is spent', () => {
    const channel = midTurn(4);
    const result = runNode({
      channel,
      vars: {
        llm_usage: { input_tokens: 9900 },
        'world.compact.anchor_len': channel.length - 2,
        'world.compact.anchor_epoch': 0,
        'world.compact.count': 6,
      },
    });
    expect(foldRequested(result)).toBe(false);
    expect(noticeText(result)).toContain(NOTICE);
  });

  it('sends the notice once per turn and only after a non-user message', () => {
    const channel = midTurn(4);
    const vars = {
      llm_usage: { input_tokens: 9900 },
      'world.compact.anchor_len': channel.length - 2,
      'world.compact.anchor_epoch': 0,
      'world.compact.fail_streak': 3,
    };
    const first = runNode({ channel, vars });
    expect(noticeText(first)).not.toBe('');

    // Already sent this turn: the channel must not grow a second notice.
    const second = runNode({
      channel: first.channel,
      vars: { ...vars, 'world.compact.notice_sent': true },
    });
    expect(noticeText(second)).toBe(noticeText(first));

    // Right after the user's own message (the turn's first round) a
    // notice would be two user messages in a row, so it waits.
    const onUserTail = runNode({
      channel: [...sections(), text('user', 'the current ask')],
      history: 0,
      vars,
    });
    expect(noticeText(onUserTail)).toBe('');

    // Right after an assistant message whose tool calls have no result yet
    // a notice would separate the call from its answer, so it waits too.
    const onPendingCall = runNode({
      channel: [...midTurn(4).slice(0, -1)],
      history: 8,
      vars,
    });
    expect(noticeText(onPendingCall)).toBe('');
  });

  it('rewrites the conversation on a successful fold and bumps the generation', () => {
    const channel = [
      ...sections(),
      text('user', 'q0'),
      text('assistant', 'a0'),
      text('user', 'the current ask'),
      ...syntheticFold(false),
    ];
    const result = runNode({
      channel,
      history: 0,
      vars: {
        'world.compact.pending': true,
        'world.compact.fold_start': 3,
        'world.compact.fold_end': 5,
        'world.compact.fail_streak': 2,
        'world.compact.count': 1,
        'world.compact.summary_text': '',
      },
    });
    // The folded prefix is gone, the summary took its place at the end,
    // the fold generation moved (any measurement taken before it is
    // stale), this turn's fold count is reported for the UI, and the
    // failure streak is back to zero.
    expect(result.vars['world.compact.folds_turn']).toBe(1);
    expect(result.vars['world.compact.epoch_total']).toBe(1);
    expect(result.vars['world.compact.fail_streak']).toBe(0);
    expect(result.vars['world.compact.failed_end']).toBe(-1);
    expect(result.channel.map((m) => m.role)).toEqual([
      'system',
      'system',
      'user',
      'user',
      'user',
    ]);
    const summary = result.channel[result.channel.length - 1];
    expect(summary.role).toBe('user');
    expect((summary.content.parts[0] as { text: string }).text).toContain(
      'summary',
    );
    // The folded messages are on the side channel, not lost: the memory
    // hooks union it back in when persisting the turn.
    expect(
      result.archive.map((m) => (m.content.parts[0] as { text: string }).text),
    ).toEqual(['q0', 'a0']);
    // The next round's measurement starts from the channel as it stands.
    expect(result.vars['world.compact.anchor_len']).toBe(result.channel.length);
    expect(result.vars['world.compact.anchor_epoch']).toBe(1);
  });
});
