// Package summary is the memory assembly. There is one history policy: the
// replay, which projects the transcript with the durable fold's summary
// standing in for the range it covered (see assembly.go's replayContext).
//
// The bounded window, the rolling summary tree and the LLM condensation that
// used to live here were removed once the graph's compact tool took over
// folding from a budget it measures itself. What remains is the transcript's
// row shape and its coordinate, which the replay reads.
package summary

import (
	"strconv"

	"github.com/GizClaw/flowcraft/core/message"
)

// StoredMessage is one replayable transcript row: the canonical message
// plus its immutable transcript coordinate. The session store assigns
// Seq on append and never reuses or reorders it, so a row's identity is
// its Seq — the model window and the context item ids are both expressed in
// transcript coordinates. Nothing here is derived from a load position: an
// inserted, skipped or removed row no longer shifts the identity of every
// row after it.
type StoredMessage struct {
	Seq     int64
	Role    message.Role
	Content message.Content
}

// Message lowers the row into the canonical message shape.
func (m StoredMessage) Message() message.Message {
	return message.Message{Role: m.Role, Content: m.Content}
}

// SourceID renders a transcript coordinate in the string form context item
// ids and the durable fold's ranges carry.
func SourceID(seq int64) string {
	return strconv.FormatInt(seq, 10)
}

// Metadata identity scheme: the coordinate system a stored coverage list is
// expressed in. Older rows carry positions in a load order; see
// compat/migrations for how those are retired.
const (
	// IdentityKey names the coordinate system a node's source ids are
	// expressed in.
	IdentityKey = "identity"
	// IdentitySeqV1 is transcript coordinates: source ids are the Seq of
	// the rows the summary covers.
	IdentitySeqV1 = "seq-v1"
)
