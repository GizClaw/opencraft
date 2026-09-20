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
for (var i = 0; i < pending.length; i++) {
  board.appendChannel(board.MAIN_CHANNEL, pending[i]);
}
