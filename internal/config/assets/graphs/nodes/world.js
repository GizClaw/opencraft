var sections = JSON.parse(board.getVar("world.sections") || "[]");
var history = JSON.parse(board.getVar("world.history") || "[]");
var msgs = [];
for (var i = 0; i < sections.length; i++) {
  msgs.push({
    role: sections[i].role,
    content: { parts: [{ type: "text", text: sections[i].text }] }
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
    content: { parts: [{ type: "text", text: history[i].text }] }
  });
}
board.setChannel(board.MAIN_CHANNEL, msgs.concat(board.channel(board.MAIN_CHANNEL) || []));
