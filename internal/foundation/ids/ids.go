// Package ids owns the identifier vocabulary opencraft and the
// flowcraft core agree on: the prefixes that tell a stored conversation
// from an ephemeral context from an engine run, the one constructor
// opencraft mints ids with, and the checks that classify a string.
//
// Every "is this one of ours" question reads from here instead of
// re-spelling a prefix. The values themselves are wire values: `run-`
// and `ctx-` ids are minted by core (graph runs, delegation contexts),
// `s-` names the session directories every build so far has written,
// and the session store accepts a conversation id exactly when
// IsSession says so. Renaming one is a migration, not a refactor;
// TestPrefixesAreWireValues pins them.
package ids

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

// The identifier prefixes.
const (
	// SessionPrefix marks a stored conversation: the id the session
	// store keys conversations by, and the session directory named
	// after it.
	SessionPrefix = "s-"
	// ContextPrefix marks an ephemeral context — a delegated subagent
	// run or a review run. Core mints one per delegation; nothing is
	// ever archived under it, so the session store rejects it.
	ContextPrefix = "ctx-"
	// RunPrefix marks an engine run id. Core mints one per graph
	// execution, and opencraft keys its crash-recovery checkpoint rows
	// by the same id: a `run-` row in agent_checkpoints means "a live
	// or unarchived run", any other row is core's own session state.
	RunPrefix = "run-"
)

// NewSession returns a fresh random session id: SessionPrefix followed
// by 16 hex characters. Eight random bytes is plenty for a
// per-workspace key space and keeps the id short enough to name a
// directory with.
func NewSession() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return SessionPrefix + hex.EncodeToString(b[:])
}

// IsSession reports whether id is a conversation id the session store
// accepts: the session prefix and no path separator. The separator
// guard belongs to the store — a conversation id names a directory
// under the workspace's session root, and legacy ids adopted from disk
// never passed through NewSession.
func IsSession(id string) bool {
	return strings.HasPrefix(id, SessionPrefix) &&
		!strings.ContainsAny(id, `/\`)
}

// IsContext reports whether id names an ephemeral context. Such ids
// never reach the store (it rejects them), so callers use this to skip
// work instead of collecting a validation error every turn.
func IsContext(id string) bool {
	return strings.HasPrefix(id, ContextPrefix)
}

// IsRun reports whether id names an engine run: a graph execution id,
// or one of the checkpoint rows keyed by it.
func IsRun(id string) bool {
	return strings.HasPrefix(id, RunPrefix)
}
