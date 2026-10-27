package memory

import (
	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"
)

// boardSource is the read-only board surface turn extraction needs. A
// live [agent.Board] and a serialized [agent.BoardSnapshot] both answer
// it, so one rule decides what a turn is whether it runs on the board a
// run just left behind or on the board a checkpoint persisted.
type boardSource interface {
	channel(name string) []message.Message
	varValue(name string) (any, bool)
}

// liveBoard reads the board a run left behind. ChannelView keeps the
// board's storage out of the caller's hands, exactly like the
// persistence paths that call it.
type liveBoard struct {
	board *agent.Board
}

func (b liveBoard) channel(name string) []message.Message {
	return b.board.ChannelView(name)
}

func (b liveBoard) varValue(name string) (any, bool) {
	return b.board.Var(name)
}

// snapshotBoard reads a board that went through a checkpoint. The
// snapshot is immutable by contract, so the channels are handed out
// without the copy ChannelView makes.
type snapshotBoard struct {
	snap *agent.BoardSnapshot
}

func (b snapshotBoard) channel(name string) []message.Message {
	return b.snap.Channels[name]
}

func (b snapshotBoard) varValue(name string) (any, bool) {
	v, ok := b.snap.Vars[name]
	return v, ok
}

// TurnSnapshot returns the smallest board snapshot that still yields the
// same turn as snap under [ExtractTurnMessages], or ok=false when snap
// carries no turn boundary to trust (the world node never ran, or the
// turn has no messages left after filtering).
//
// It exists for checkpoints. A wave checkpoint is the crash-recovery log
// of one turn, and the graph stamps one per completed wave with the
// whole board attached — including the world-state sections and the
// full-history replay prefix, which for a long conversation is tens of
// megabytes written hundreds of times for a record nothing reads back.
// Recovery only ever asks for the turn ([ExtractTurnMessages] plus the
// request annotation), so the snapshot keeps exactly that: the extracted
// messages on MainChannel and both boundary counters zeroed. Restoring
// the trimmed board and extracting again is a fixpoint — that is the
// property the tests pin, and the reason no second slicing rule exists
// here.
//
// The returned channel aliases snap's messages: treat it as read-only,
// like the result of [ExtractTurnMessages].
func TurnSnapshot(
	snap *agent.BoardSnapshot, req *agent.Request,
) (*agent.BoardSnapshot, bool) {
	if snap == nil {
		return nil, false
	}
	src := snapshotBoard{snap: snap}
	// The section counter is the world node's record that the turn
	// boundary exists at all. Without it (a wave that has not reached
	// the world node yet, a custom graph) the snapshot is left alone:
	// it is small, and extraction would fall back to the request alone.
	if _, ok := sectionCount(src); !ok {
		return nil, false
	}
	msgs := extractTurnMessages(src, req, nil)
	if len(msgs) == 0 {
		return nil, false
	}
	return &agent.BoardSnapshot{
		Vars: map[string]any{
			// Both counts stay present and zero: a missing counter
			// means "no turn boundary", which drops extraction to the
			// request-plus-tail fallback and loses the tool results.
			worldSectionsCountVar: 0,
			worldHistoryCountVar:  0,
		},
		Channels: map[string][]message.Message{agent.MainChannel: msgs},
	}, true
}
