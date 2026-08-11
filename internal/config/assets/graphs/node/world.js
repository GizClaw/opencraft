var sections = JSON.parse(board.getVar("world.sections") || "[]");
var msgs = [];
for (var i = 0; i < sections.length; i++) {
  msgs.push({
    role: sections[i].role,
    content: { parts: [{ type: "text", text: sections[i].text }] }
  });
}
board.setChannel("main", msgs.concat(board.channel("main") || []));
