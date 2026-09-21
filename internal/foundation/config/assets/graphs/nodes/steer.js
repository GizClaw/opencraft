// Steer node: hands the user's mid-turn corrections to the running turn.
//
// The host queues what the user submits while a turn runs (Turn.Steer in
// the session runtime, drained through agent.SteerSource) and this node
// is the boundary that delivers it. The graph places it
// tools -> steer -> compact: the round's tool results are on the channel,
// the next inference round has not read it yet, and
// assistant(tool_calls) -> tool -> user is the transition every provider
// accepts. Draining anywhere else would either put two user messages in a
// row (before the first round, where the tail is the turn's own ask) or
// separate a tool call from its result.
//
// host.drainSteer is take-all and never latches: it returns whatever
// arrived since the last boundary (an empty array when nothing did), so a
// message is never delivered twice, and the queue keeps whatever a closed
// boundary could not take.
//
// Everything a boundary takes lands as ONE user message. Two user
// messages in a row are the shape the placement above avoids, so a batch
// of corrections must not recreate it; and one message keeps what a
// single round injects bounded by what the host allowed into the queue
// (maxSteerQueuedBytes in the host's steer surface). The host, not this
// node, is where a batch that would be too big is refused: draining is
// destructive, so a boundary that took messages has to deliver them.

// A fold in flight owns the channel tail: the compact node reads the
// fold's tool result as the last message and rebuilds the channel around
// it. A steer line appended between that result and the application would
// read as a failed fold, and that recovery path slices the last two
// messages out of the channel. This boundary stays closed and the queue
// keeps its messages for the next round.
if (board.getVar("world.compact.pending")) return;

// An injected message may only follow a tool result — the tail the tools
// node just wrote. The guard keeps a rewired document from injecting user
// text where providers reject it, and it runs before the drain because
// draining is destructive: a boundary that cannot append must not take
// the messages out of the queue.
var channel = board.channel(board.MAIN_CHANNEL) || [];
var last = channel[channel.length - 1];
if (!last || last.role !== "tool") return;

var pending = host.drainSteer();
if (!pending.length) return;

// A single message is appended as it arrived: there is nothing to merge
// and no rebuild that could lose a field this node does not know about.
if (pending.length === 1) {
  board.appendChannel(board.MAIN_CHANNEL, pending[0]);
  return;
}

// A batch becomes one user message carrying every drained message's
// parts in order. The parts keep their identity, so a message that
// carried something other than text (a steer is text today) is not
// dropped, and two corrections stay two blocks the model reads apart.
var parts = [];
for (var i = 0; i < pending.length; i++) {
  var message = pending[i];
  if (!message || !message.content || !message.content.parts) continue;
  for (var j = 0; j < message.content.parts.length; j++) {
    parts.push(message.content.parts[j]);
  }
}
if (!parts.length) return;
board.appendChannel(board.MAIN_CHANNEL, {
  role: "user",
  content: { parts: parts },
});
