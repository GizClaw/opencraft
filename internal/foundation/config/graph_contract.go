package config

// CompactArchiveChannel is the board channel the assistant graph's
// compact node moves folded conversation into before it shrinks the
// MainChannel. It is a contract between the graph asset
// (assets/graphs/nodes/compact.js) and the memory hooks that archive a
// turn: the hooks concatenate this channel with MainChannel so a
// compacted turn still persists every message it exchanged. The name
// deliberately avoids the engine's "__" namespace.
const CompactArchiveChannel = "opencraft.compact_archive"

// The board vars the assistant graph's script nodes exchange with Go. They
// are the same kind of contract as CompactArchiveChannel: a script cannot
// import Go, so the literal lives in the asset and the name lives here, and
// graph_contract_test.go pins the two against each other. Renaming one side
// is otherwise silent — the anchor loses its measurement, and the UI never
// learns that a fold happened.
const (
	// BoardVarLLMUsage is where the graph's llm node records the last
	// call's inference.Usage (the node's usage_key). The compaction node
	// reads it; the turn-end hook turns it into the usage anchor.
	BoardVarLLMUsage = "llm_usage"
	// BoardVarUsageAnchor carries the previous turn's measurement into the
	// prepare hook's board, for the compaction node (full-replay
	// deployments only; see worldstate/anchor.go).
	BoardVarUsageAnchor = "world.usage.anchor"
	// BoardVarAnchorLen is the MainChannel length the compaction node last
	// sent to the llm node; with BoardVarLLMUsage it describes exactly one
	// request.
	BoardVarAnchorLen = "world.compact.anchor_len"
	// BoardVarAnchorEpoch is the fold generation that measurement belongs
	// to, stamped next to the length.
	BoardVarAnchorEpoch = "world.compact.anchor_epoch"
	// BoardVarEpochTotal is the conversation's fold generation as of the
	// last recorded measurement (diagnostic; the anchor's coverage check is
	// what decides comparability).
	BoardVarEpochTotal = "world.compact.epoch_total"
	// BoardVarFoldsPerTurn counts successful folds in the current turn, for
	// the turn_end event the UI renders.
	BoardVarFoldsPerTurn = "world.compact.folds_turn"
	// BoardVarFailStreak counts consecutive failed condensations standing
	// at the end of the turn.
	BoardVarFailStreak = "world.compact.fail_streak"
	// BoardVarNoticeSent reports whether the model was told that compaction
	// is out of options while the prompt is still over budget.
	BoardVarNoticeSent = "world.compact.notice_sent"
	// BoardVarTailBlock carries the per-turn context block (plan, skills,
	// caller extras) that the world node appends to the turn's own message
	// instead of turning into messages of its own — a block that changes
	// every turn must not sit in front of the conversation, because a
	// provider's prompt cache reuses the longest common prefix.
	BoardVarTailBlock = "world.tail_block"
)

// CompactionBoardVars lists every board var the compaction node and the Go
// side exchange, for the contract test. Order matches the declaration above.
var CompactionBoardVars = []string{
	BoardVarLLMUsage,
	BoardVarUsageAnchor,
	BoardVarAnchorLen,
	BoardVarAnchorEpoch,
	BoardVarEpochTotal,
	BoardVarFoldsPerTurn,
	BoardVarFailStreak,
	BoardVarNoticeSent,
}
