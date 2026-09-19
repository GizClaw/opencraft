var sections = JSON.parse(board.getVar("world.sections") || "[]");
var history = JSON.parse(board.getVar("world.history") || "[]");
var msgs = [];
for (var i = 0; i < sections.length; i++) {
  msgs.push({
    role: sections[i].role,
    content: sections[i].content
  });
}
// Record how many world-state messages were prepended so lifecycle
// hooks can tell the seeded conversation (user request + assistant /
// tool messages) apart from injected context when persisting a turn.
board.setVar("world.sections.count", msgs.length);
// Full-history replay (memory.replay_full_history): the replayed
// conversation sits after the world sections and before the current
// turn's messages, so the compact node counts it as foldable
// conversation (it is beyond world.sections.count).
for (var i = 0; i < history.length; i++) {
  msgs.push({
    role: history[i].role,
    content: history[i].content
  });
}
// Record how many replayed history messages sit between the world
// sections and the current turn's messages, so lifecycle hooks can
// locate the user's turn message on the channel when persisting.
board.setVar("world.history.count", history.length);
// Per-turn context block (plan, skills, extras). It is appended to the
// turn's own message instead of riding as messages of its own: a
// provider prompt cache only reuses the longest common prefix of the
// previous request, so a block that changes on nearly every turn would
// otherwise sit in front of the whole conversation and make every byte
// behind it a cache miss. The user's message is the one position that
// is expected to differ every turn.
var tail = board.getVar("world.tail_block") || "";
var rest = board.channel(board.MAIN_CHANNEL) || [];
var tailMessage = null;
if (tail !== "") {
  var last = rest.length > 0 ? rest[rest.length - 1] : null;
  if (last && last.role === "user") {
    var parts = (last.content && last.content.parts) || [];
    last.content = {
      parts: parts.concat([{ type: "text", text: tail }])
    };
  } else {
    // The block only rides a message the model can attribute it to. With
    // no turn message on the channel (a custom engine seeding its own
    // board), or a tail that is not the user's message, giving the block
    // its own user message keeps the plan and the skills visible instead
    // of attaching injected context to someone else's turn. It is emitted
    // last, after whatever the channel already held: a per-turn block
    // behind the conversation is what the cache-stable prefix relies on.
    tailMessage = {
      role: "user",
      content: { parts: [{ type: "text", text: tail }] }
    };
  }
}
var out = msgs.concat(rest);
if (tailMessage) out.push(tailMessage);
board.setChannel(board.MAIN_CHANNEL, out);
