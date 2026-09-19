// Compaction node: runs before every LLM round. It sizes the MainChannel
// against the selected model's input cap and, when over budget, folds the
// older rounds into a summary via the internal compact tool (executed by
// the tools node). The summary is appended at the end of the conversation
// as a marked user message (codex-style), replacing any previous summary.
// Lifecycle hooks recognize the marker and keep it out of the persisted
// conversation.
//
// Folding actually shrinks MainChannel: the folded prefix moves to a
// side channel (ARCHIVE_CHANNEL, mirrored by
// config.CompactArchiveChannel) before it is removed, so the turn
// archive and memory still persist every message the model no longer
// sees. The side channel is not part of the model request.
//
// Sizing prefers a measurement over an estimate. Two measured anchors are
// available, in this order:
//
//   1. This turn's previous call: the llm node records its usage under
//      llm_usage, and this node stamped world.compact.anchor_len with the
//      channel length that call saw, so the prompt is that measurement
//      plus the token estimate of everything appended since. A fold
//      rewrites the channel prefix, so this one holds only while the fold
//      generation it was taken in still stands.
//   2. The previous turn's own last call, persisted in the session store
//      and injected as world.usage.anchor by the world-state prepare hook.
//      Only full-replay deployments receive it: the cover check below
//      ("is the measured prefix still a prefix of this channel?") is sound
//      while the prefix grows, and a sliding memory window cannot promise
//      that (see usageAnchorBoardValue in worldstate/anchor.go). Where it
//      applies it measures the bulk of the prompt, which is exactly what
//      folding a long conversation needs before the turn's first call.
//
// With neither, the character estimate is used — but an estimate alone
// does not fold: this node waits for the next round's real number (the
// turn is at least one round old by then), because a character estimate is
// crude in both directions and a fold costs a summarization call plus a
// prompt-cache invalidation. An estimate at or past the whole window is
// not deferred: that request would simply fail.
var cfg = config || {};
var PRESERVE = cfg.preserve_recent || 10;
var BUDGET = cfg.budget_chars || 4096;
var RATIO = cfg.threshold_ratio || 0.85;
// Consecutive failed folds before folding stops for the turn. A failure
// costs a summarization call and tells us nothing about the next attempt,
// so the streak resets on success; the old absolute cap stopped folding
// even on success, which could leave the prompt over the window with no
// way out.
var MAX_FAILURES = cfg.max_consecutive_failures || 3;
// Successful folds this turn. Each one costs a summarization call, and a
// long turn can legitimately need several (fold, run more rounds, fold
// again), so the cap is per-turn rather than per-conversation.
var MAX_FOLDS = cfg.max_folds_per_turn || 6;
var MAX_INPUT = cfg.max_input_tokens || 0;
// ARCHIVE_CHANNEL mirrors config.CompactArchiveChannel in Go
// (internal/foundation/config/graph_contract.go).
var ARCHIVE_CHANNEL = "opencraft.compact_archive";
// NOTICE_PREFIX mirrors summarytext.ContextNoticePrefix in Go
// (internal/foundation/utils/summarytext). A notice carrying it is kept
// out of the persisted conversation exactly like a summary.
var NOTICE_PREFIX = "Context notice:";
// When neither the router nor the node config reports a model input
// window (azure and manually-entered model rows), compaction falls back
// to this conservative budget. Without a fallback, full-history replay
// would keep growing past the provider's real cap with compaction
// silently disabled.
var FALLBACK_MAX_INPUT_TOKENS = 128000;
// The default graph carries no static system_prompt: base instructions
// are world-state sections counted below. system_prompt_tokens is kept
// for custom graphs that still configure a static prompt.
var SYS_PROMPT_TOKENS = cfg.system_prompt_tokens || 0;
var channel = board.channel(board.MAIN_CHANNEL) || [];
var count = Number(board.getVar("world.sections.count") || 0);
var compactCount = Number(board.getVar("world.compact.count") || 0);
// epoch counts folds applied in THIS turn. A fold rewrites the channel
// prefix, so a measurement taken before one no longer describes the
// channel in front of it; the generation is what the in-turn anchor check
// compares. It is relative to the conversation's history only for the
// record (world.compact.epoch_total, which the turn-end hook stores with
// the measurement) — the fold itself is what decides comparability.
var epoch = 0;
var failStreak = Number(board.getVar("world.compact.fail_streak") || 0);
var epochBase = 0;

// parseAnchor accepts what the board carries for an anchor: a Go string
// (the prepare hook) or an already-parsed object (tests, custom hosts).
function parseAnchor(value) {
  if (!value) return null;
  var parsed = value;
  if (typeof value === "string") {
    try {
      parsed = JSON.parse(value);
    } catch (e) {
      return null;
    }
  }
  if (!parsed) return null;
  var tokens = Number(parsed.input_tokens || 0);
  var anchored = Number(parsed.anchored_messages || 0);
  if (!(tokens > 0) || !(anchored > 0)) return null;
  return {
    tokens: tokens,
    messages: anchored,
    epoch: Number(parsed.compact_count || 0),
    model: parsed.model || ""
  };
}

var previousAnchor = parseAnchor(board.getVar("world.usage.anchor"));
if (previousAnchor) epochBase = previousAnchor.epoch;

// cumulativeFolds is the conversation's fold generation: how many folds
// have been applied to the channel prefix in total.
function cumulativeFolds() {
  return epochBase + epoch;
}

// messageText concatenates a message's text parts. It is how this node
// recognizes the previous summary: the compact tool returns the exact
// message it produced, so comparing text identifies it regardless of
// where it currently sits on the channel.
function messageText(m) {
  var parts = (m && m.content && m.content.parts) || [];
  var text = "";
  for (var i = 0; i < parts.length; i++) {
    var p = parts[i];
    if (p && p.type === "text") text += p.text || "";
  }
  return text;
}

// isSummary mirrors the Go summary marker: any message whose text
// starts with the same marker line as the previous summary is derived
// context, not conversation.
function isSummary(m, prevSummary) {
  if (!prevSummary || !m || m.role !== "user") return false;
  var prefix = prevSummary.split("\n")[0] + "\n";
  return messageText(m).indexOf(prefix) === 0;
}

// renderText estimates one MainChannel message's prompt footprint.
// It mirrors summarytext.RenderMessage
// (internal/foundation/utils/summarytext): text parts keep their content
// and tool activity is rendered as tool_call / tool_result lines, so
// the estimate covers what the model will actually pay for. The fold
// itself is passed to the compact tool as full messages (role + content
// parts) — rendering happens in Go.
function renderText(m) {
  var parts = (m && m.content && m.content.parts) || [];
  var text = "";
  var lines = [];
  for (var i = 0; i < parts.length; i++) {
    var p = parts[i];
    if (!p) continue;
    if (p.type === "text") {
      text += p.text || "";
    } else if (p.type === "tool_call" && p.call) {
      // JSON.stringify matches the Go side's compacted arguments
      // (summarytext.compactJSON). Both sides cover the same text; only
      // an exotic spelling (an explicit \uXXXX escape, an HTML-escaped
      // character) can be written out differently, by a few characters.
      lines.push("tool_call: " + p.call.name + " " + JSON.stringify(p.call.arguments));
    } else if (p.type === "tool_result" && p.result) {
      lines.push("tool_result: " + resultText(p.result.content));
    }
  }
  if (lines.length > 0) {
    var trimmed = text.trim();
    if (trimmed === "") return lines.join("\n");
    return trimmed + "\n" + lines.join("\n");
  }
  return text;
}

// resultText mirrors the Go side's Content.Text() for a tool result:
// text parts are concatenated in order and every other part kind is
// skipped. A result whose content was read as an opaque value would
// make the estimate cover the wrong text entirely.
function resultText(content) {
  var parts = (content && content.parts) || [];
  var text = "";
  for (var i = 0; i < parts.length; i++) {
    var p = parts[i];
    if (p && p.type === "text") text += p.text || "";
  }
  return text;
}

// resultPayload returns the text a tool result carries, accepting both
// shapes the board bridge can hand over: a content object (the wire form,
// {parts:[{type:"text",text:...}]}) and a raw string (a custom host).
function resultPayload(content) {
  if (typeof content === "string") return content;
  return resultText(content);
}

function estimateTokens(msgs) {
  var tokens = 0;
  for (var i = 0; i < msgs.length; i++) {
    var s = renderText(msgs[i]);
    var cjk = 0;
    for (var j = 0; j < s.length; j++) {
      var c = s.charCodeAt(j);
      if ((c >= 0x4e00 && c <= 0x9fff) ||
          (c >= 0x3400 && c <= 0x4dbf) ||
          (c >= 0x3040 && c <= 0x30ff)) {
        cjk++;
      }
    }
    tokens += cjk + Math.ceil((s.length - cjk) / 4) + 8;
  }
  return tokens;
}

// parseUsage reads the llm node's usage record (inference.Usage). It is
// only the token count: which prompt that count measured is stamped by
// this node in world.compact.anchor_len.
function parseUsage(value) {
  if (!value) return null;
  var parsed = value;
  if (typeof value === "string") {
    try {
      parsed = JSON.parse(value);
    } catch (e) {
      return null;
    }
  }
  if (!parsed) return null;
  var tokens = Number(parsed.input_tokens || 0);
  return tokens > 0 ? { tokens: tokens } : null;
}

// measuredPrompt returns the provider-measured prompt size in tokens for
// the prompt this round is about to send, or 0 when no usable measurement
// exists. Every path covers the whole channel: the measurement plus the
// estimate of the messages appended since it was taken.
//
// plainEstimate is this round's character estimate. It is the floor for
// the previous turn's measurement: that measurement describes a channel
// shape the memory window may have slid away from, so the two are combined
// rather than swapped.
function measuredPrompt(plainEstimate) {
  var usage = parseUsage(board.getVar("llm_usage"));
  var anchored = Number(board.getVar("world.compact.anchor_len") || 0);
  var anchoredEpoch = Number(board.getVar("world.compact.anchor_epoch") || 0);
  if (usage && anchored > 0 && anchored <= channel.length &&
      anchoredEpoch === cumulativeFolds()) {
    // Exact: the measured call saw exactly this channel's first `anchored`
    // messages, and nothing was folded since.
    return usage.tokens + estimateTokens(channel.slice(anchored));
  }
  var previous = previousAnchor;
  if (previous && previous.messages <= channel.length) {
    var covered =
      previous.tokens + estimateTokens(channel.slice(previous.messages));
    return Math.max(covered, plainEstimate);
  }
  return 0;
}

// Resolve the selected model's max input tokens once per turn via the
// inference bridge (Router.ExplainGenerate — local, no provider I/O)
// and cache the result on the board for later rounds. Falls back to
// the node config, then to a conservative default, when the router is
// unavailable or does not declare a window, so compaction always has a
// budget to decide against.
function resolveMaxInputTokens() {
  var cached = Number(board.getVar("world.compact.max_input_tokens") || 0);
  if (cached > 0) {
    return cached;
  }
  try {
    var res = inference.routeExplain({
      input: {
        role: "user",
        content: {
          content: { parts: [{ type: "text", text: "hi" }] },
          intent: { text: {} }
        }
      }
    });
    var limit = res && res.limits
      ? Number(res.limits.max_input_tokens || 0)
      : 0;
    if (limit > 0) {
      board.setVar("world.compact.max_input_tokens", limit);
      return limit;
    }
    return MAX_INPUT > 0 ? MAX_INPUT : FALLBACK_MAX_INPUT_TOKENS;
  } catch (e) {
    return MAX_INPUT > 0 ? MAX_INPUT : FALLBACK_MAX_INPUT_TOKENS;
  }
}

// stampAnchor records the prompt shape the llm node is about to send.
// Paired with the usage that node records, it is what makes the next
// round's measurement exact.
function stampAnchor() {
  board.setVar("world.compact.anchor_len", channel.length);
  board.setVar("world.compact.anchor_epoch", cumulativeFolds());
  // The two counts the turn-end hook reads: the conversation's fold
  // generation (so a later turn can tell whether a stored measurement is
  // still comparable) and this turn's own fold count (what the UI reports).
  board.setVar("world.compact.epoch_total", cumulativeFolds());
  board.setVar("world.compact.folds_turn", epoch);
}

// appendNotice hands the model the one piece of information it can act
// on: this turn cannot be compacted any further. It is a user-role
// message because a tool result needs a call to answer, and lifecycle
// hooks drop it by prefix, exactly like a summary. Role alternation
// decides whether it can be sent at all: after a user message it would be
// two user messages in a row, and after an assistant message whose tool
// calls have no result it would separate the call from its answer (which
// providers reject). Both wait for the next round.
function appendNotice(text) {
  if (board.getVar("world.compact.notice_sent") === true) return;
  var last = channel[channel.length - 1];
  if (!last || last.role === "user" || hasUnansweredToolCall(last)) return;
  board.appendChannel(board.MAIN_CHANNEL, {
    role: "user",
    content: { parts: [{ type: "text", text: NOTICE_PREFIX + " " + text }] }
  });
  board.setVar("world.compact.notice_sent", true);
}

// hasUnansweredToolCall reports whether m is an assistant message carrying
// tool calls whose results are not on the channel yet. Appending anything
// user-side between such a call and its result breaks the tool pairing every
// provider requires.
function hasUnansweredToolCall(m) {
  if (!m || m.role !== "assistant") return false;
  var parts = (m.content && m.content.parts) || [];
  for (var i = 0; i < parts.length; i++) {
    if (parts[i] && parts[i].type === "tool_call") return true;
  }
  return false;
}

// Apply mode: the compact tool just ran. Move the folded prefix onto
// the side channel, drop it from MainChannel, and append the new
// summary in place of the previous one. A failed condensation moves
// nothing: the conversation and the previous summary stay intact, and
// the failed boundary is remembered so the next round does not retry
// the same fold.
if (board.getVar("world.compact.pending")) {
  var patch = null;
  var last = channel[channel.length - 1];
  if (last && last.role === "tool") {
    var parts = last.content.parts || [];
    for (var i = 0; i < parts.length; i++) {
      var p = parts[i];
      if (p.type === "tool_result" && p.result && !p.result.is_error) {
        try {
          // The compact tool answers with its patch as the result's text
          // (compact.Patch): the content is a parts array, and the JSON
          // string is the text part inside it.
          patch = JSON.parse(resultPayload(p.result.content));
        } catch (e) {
          patch = null;
        }
      }
    }
  }
  // Tail is [synthetic assistant, compact result]; everything before
  // them is the conversation this node last saw.
  var base = channel.slice(0, Math.max(count, channel.length - 2));
  var foldStart = Number(board.getVar("world.compact.fold_start") || count);
  var foldEnd = Number(board.getVar("world.compact.fold_end") || -1);
  var prevSummary = board.getVar("world.compact.summary_text") || "";
  var applied =
    patch && patch.message && patch.message.role && patch.message.content &&
    foldStart >= count && foldEnd > foldStart && foldEnd <= base.length;
  if (applied) {
    var moved = [];
    var rebuilt = base.slice(0, foldStart);
    for (var i = foldStart; i < base.length; i++) {
      if (i < foldEnd) {
        // Summary messages are derived context: they never enter the
        // durable side channel.
        if (!isSummary(base[i], prevSummary)) moved.push(base[i]);
        continue;
      }
      if (isSummary(base[i], prevSummary)) continue;
      rebuilt.push(base[i]);
    }
    rebuilt.push(patch.message);
    // Write the side channel first: if the process dies between the two
    // writes the archive sees a duplicate, never a lost message.
    if (moved.length > 0) {
      board.setChannel(
        ARCHIVE_CHANNEL,
        (board.channel(ARCHIVE_CHANNEL) || []).concat(moved)
      );
    }
    board.setChannel(board.MAIN_CHANNEL, rebuilt);
    board.setVar("world.compact.summary_text", messageText(patch.message));
    board.setVar("world.compact.failed_end", -1);
    // The ask's index moves down with the messages that just left: the
    // next fold must still find it where it now sits.
    var shiftedAsk = Number(board.getVar("world.compact.turn_start") || -1);
    if (shiftedAsk >= foldEnd) {
      board.setVar(
        "world.compact.turn_start",
        shiftedAsk - (foldEnd - foldStart)
      );
    }
    epoch = epoch + 1;
    failStreak = 0;
    board.setVar("world.compact.fail_streak", 0);
  } else {
    // Keep the conversation and the previous summary exactly as they
    // were; only the synthetic call + result are dropped.
    board.setChannel(board.MAIN_CHANNEL, base);
    board.setVar("world.compact.failed_end", foldEnd);
    failStreak = failStreak + 1;
    board.setVar("world.compact.fail_streak", failStreak);
  }
  board.setVar("world.compact.pending", false);
  board.setVar("world.compact.count", compactCount + 1);
  board.setVar("world.compact.fold_start", 0);
  board.setVar("world.compact.fold_end", -1);
  board.setVar("tool_pending", false);
  // The channel the llm node is about to see is settled here (a fold just
  // rewrote it, or the failed attempt left it as it was).
  channel = board.channel(board.MAIN_CHANNEL) || [];
  stampAnchor();
  return;
}

// Check mode: fold only when the prompt is measured (or, past the whole
// window, estimated) to exceed the model's budget.
var worldPrefix = channel.slice(0, count);
var conversation = channel.slice(count);
var maxTokens = resolveMaxInputTokens();
var plainEstimate = SYS_PROMPT_TOKENS + estimateTokens(worldPrefix) +
  estimateTokens(conversation);
var measured = measuredPrompt(plainEstimate);
var estimate = measured > 0 ? measured : plainEstimate;
var threshold = Math.floor(maxTokens * RATIO);
var overThreshold = maxTokens > 0 && estimate > threshold;
var overWindow = maxTokens > 0 && estimate > maxTokens;
// No measurement yet: wait one round for the provider's own number
// instead of folding on a character estimate.
var deferred = overThreshold && measured <= 0 && !overWindow;
var foldsSpent = compactCount >= MAX_FOLDS;
var failuresSpent = failStreak >= MAX_FAILURES;
var shouldCompact = overThreshold && !deferred && !foldsSpent && !failuresSpent;

// Never fold the current turn's user message: the summary carries the
// older context, but the ask itself stays verbatim. The index is seeded
// from the world node's replay marker and follows every successful move.
var turnStart = Number(board.getVar("world.compact.turn_start") || 0);
if (board.getVar("world.compact.turn_start_seeded") !== true) {
  turnStart = count + Number(board.getVar("world.history.count") || 0);
  board.setVar("world.compact.turn_start", turnStart);
  board.setVar("world.compact.turn_start_seeded", true);
}
if (turnStart < count) turnStart = count;

var foldStart = count;
if (turnStart === foldStart) foldStart = count + 1;
var foldEnd = Math.max(foldStart, channel.length - PRESERVE);
if (turnStart > foldStart && turnStart < foldEnd) {
  // Fold everything before the user message first; later rounds may
  // fold what follows it.
  foldEnd = turnStart;
}
// Keep tool calls with their results: the preserved side must not start
// with a tool message whose call is being folded away.
while (
  foldEnd > foldStart &&
  channel[foldEnd] &&
  channel[foldEnd].role === "tool"
) {
  foldEnd--;
}

// A failed fold is not retried against an unchanged boundary: the next
// new message moves the boundary and makes the retry meaningful.
var failedEnd = Number(board.getVar("world.compact.failed_end") || -1);
if (shouldCompact && failedEnd >= 0 && failedEnd === foldEnd) {
  shouldCompact = false;
}

if (shouldCompact) {
  var fold = channel.slice(foldStart, foldEnd);
  if (fold.length > 0) {
    var args = {
      conversation: fold.map(function (m) {
        return { role: m.role, content: m.content };
      }),
      budget_chars: BUDGET,
      conversation_id: run.get_context_id()
    };
    board.appendChannel(board.MAIN_CHANNEL, {
      role: "assistant",
      content: {
        parts: [{
          type: "tool_call",
          call: {
            id: "compact-" + (compactCount + 1),
            name: "compact",
            arguments: args
          }
        }]
      }
    });
    board.setVar("tool_pending", true);
    board.setVar("world.compact.pending", true);
    board.setVar("world.compact.fold_start", foldStart);
    board.setVar("world.compact.fold_end", foldEnd);
    return;
  }
}

if (!shouldCompact && !deferred && overThreshold &&
    (foldsSpent || failuresSpent)) {
  // Compaction is out of options and the prompt is still over budget:
  // say so once, so the model can land the step in flight instead of
  // running into a provider error.
  appendNotice(
    "this turn's context is at about " + Math.round(estimate / maxTokens * 100) +
    "% of the model's input window and automatic compaction cannot reduce " +
    "it further (" + compactCount + " folds, " + failStreak +
    " consecutive failures). Prefer short tool output, avoid re-reading " +
    "large files, and finish the current step."
  );
}

board.setVar("tool_pending", false);
stampAnchor();
