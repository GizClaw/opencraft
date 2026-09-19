// Compaction node: runs before every LLM round. It estimates the
// MainChannel's token footprint against the selected model's input cap
// and, when over budget, folds the older rounds into a summary via the
// internal compact tool (executed by the tools node). The summary is
// appended at the end of the conversation as a marked user message
// (codex-style), replacing any previous summary. Lifecycle hooks
// recognize the marker and keep it out of the persisted conversation.
//
// Folding actually shrinks MainChannel: the folded prefix moves to a
// side channel (ARCHIVE_CHANNEL, mirrored by
// config.CompactArchiveChannel) before it is removed, so the turn
// archive and memory still persist every message the model no longer
// sees. The side channel is not part of the model request.
var cfg = config || {};
var PRESERVE = cfg.preserve_recent || 10;
var BUDGET = cfg.budget_chars || 4096;
var RATIO = cfg.threshold_ratio || 0.85;
var MAX_COMPACTIONS = cfg.max_compactions || 3;
var MAX_INPUT = cfg.max_input_tokens || 0;
// ARCHIVE_CHANNEL mirrors config.CompactArchiveChannel in Go
// (internal/foundation/config/graph_contract.go).
var ARCHIVE_CHANNEL = "opencraft.compact_archive";
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
          patch = JSON.parse(p.result.content);
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
    var turnStart = Number(board.getVar("world.compact.turn_start") || -1);
    if (turnStart >= foldEnd) {
      board.setVar(
        "world.compact.turn_start",
        turnStart - (foldEnd - foldStart)
      );
    }
    board.setVar("world.compact.failed_end", -1);
  } else {
    // Keep the conversation and the previous summary exactly as they
    // were; only the synthetic call + result are dropped.
    board.setChannel(board.MAIN_CHANNEL, base);
    board.setVar("world.compact.failed_end", foldEnd);
  }
  board.setVar("world.compact.pending", false);
  board.setVar("world.compact.count", compactCount + 1);
  board.setVar("world.compact.fold_start", 0);
  board.setVar("world.compact.fold_end", -1);
  board.setVar("tool_pending", false);
  return;
}

// Check mode: compact only when estimated usage exceeds the model cap.
var worldPrefix = channel.slice(0, count);
var conversation = channel.slice(count);
var maxTokens = resolveMaxInputTokens();
var shouldCompact = maxTokens > 0 &&
  SYS_PROMPT_TOKENS + estimateTokens(worldPrefix) +
    estimateTokens(conversation) >
    Math.floor(maxTokens * RATIO) &&
  compactCount < MAX_COMPACTIONS;

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

board.setVar("tool_pending", false);
