// Compaction node: runs before every LLM round. It estimates the
// MainChannel's token footprint against the selected model's input cap
// and, when over budget, folds the older rounds into a summary via the
// internal compact tool (executed by the tools node). The summary is
// appended at the end of the conversation as a marked user message
// (codex-style), replacing any previous summary. Lifecycle hooks
// recognize the marker and keep it out of the persisted conversation.
var cfg = config || {};
var PRESERVE = cfg.preserve_recent || 10;
var BUDGET = cfg.budget_chars || 4096;
var RATIO = cfg.threshold_ratio || 0.85;
var MAX_COMPACTIONS = cfg.max_compactions || 3;
var MAX_INPUT = cfg.max_input_tokens || 0;
var SYS_PROMPT_TOKENS = cfg.system_prompt_tokens || 2000;
// Must match compact.SummaryPrefix in internal/tools/compact/compact.go:
// the marker is what lifecycle hooks filter on when persisting a turn.
var SUMMARY_PREFIX = "Another language model started to solve this problem and produced a summary of its thinking process.";

var channel = board.channel(board.MAIN_CHANNEL) || [];
var count = Number(board.getVar("world.sections.count") || 0);
var compactCount = Number(board.getVar("world.compact.count") || 0);

// renderText estimates one MainChannel message's prompt footprint.
// It mirrors compact.RenderMessage (internal/tools/compact/compact.go):
// text parts keep their content and tool activity is rendered as
// tool_call / tool_result lines, so the estimate covers what the model
// will actually pay for. The fold itself is passed to the compact tool
// as full messages (role + content parts) — rendering happens in Go.
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
      lines.push("tool_call: " + p.call.name + " " + JSON.stringify(p.call.arguments));
    } else if (p.type === "tool_result" && p.result) {
      lines.push("tool_result: " + p.result.content);
    }
  }
  if (lines.length > 0) {
    if (text.trim() !== "") text += "\n";
    text += lines.join("\n");
  }
  return text;
}

// isSummaryMessage reports whether m is a compaction summary injected
// by this node (a marked user message). Old summaries are removed on
// every new compaction so only the latest one stays in context.
function isSummaryMessage(m) {
  if (!m || m.role !== "user") return false;
  var parts = (m.content && m.content.parts) || [];
  var text = "";
  for (var i = 0; i < parts.length; i++) {
    if (parts[i] && parts[i].type === "text") text += parts[i].text || "";
  }
  return text.indexOf(SUMMARY_PREFIX + "\n") === 0;
}

function estimateTokens(msgs) {
  var tokens = SYS_PROMPT_TOKENS;
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
// the node config when the router is unavailable.
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
    }
    return limit;
  } catch (e) {
    return MAX_INPUT;
  }
}

// Apply mode: the compact tool just ran. Replace any previous summary
// with the new one at the end of the conversation and drop the
// synthetic call + result messages.
if (board.getVar("world.compact.pending")) {
  var summary = "";
  var last = channel[channel.length - 1];
  if (last && last.role === "tool") {
    var parts = last.content.parts || [];
    for (var i = 0; i < parts.length; i++) {
      var p = parts[i];
      if (p.type === "tool_result" && p.result && !p.result.is_error) {
        summary = p.result.content;
      }
    }
  }
  // Tail is [synthetic assistant, compact result]; everything before
  // them is the conversation this node last saw.
  var base = channel.slice(0, Math.max(count, channel.length - 2));
  var rebuilt = base.slice(0, count);
  for (var i = count; i < base.length; i++) {
    if (!isSummaryMessage(base[i])) rebuilt.push(base[i]);
  }
  if (summary) {
    rebuilt.push({
      role: "user",
      content: { parts: [{ type: "text", text: SUMMARY_PREFIX + "\n" + summary }] }
    });
  }
  board.setChannel(board.MAIN_CHANNEL, rebuilt);
  board.setVar("world.compact.pending", false);
  board.setVar("world.compact.count", compactCount + 1);
  board.setVar("tool_pending", false);
  return;
}

// Check mode: compact only when estimated usage exceeds the model cap.
var conversation = channel.slice(count);
var maxTokens = resolveMaxInputTokens() || MAX_INPUT;
var shouldCompact = maxTokens > 0 &&
  estimateTokens(conversation) > Math.floor(maxTokens * RATIO) &&
  compactCount < MAX_COMPACTIONS;

if (shouldCompact) {
  var fold = conversation.slice(0, Math.max(0, conversation.length - PRESERVE));
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
    return;
  }
}

board.setVar("tool_pending", false);
