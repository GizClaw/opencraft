var sections = JSON.parse(board.getVar("world.sections") || "[]");
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
board.setChannel(board.MAIN_CHANNEL, msgs.concat(board.channel(board.MAIN_CHANNEL) || []));
