package worldstate

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"go.opentelemetry.io/otel/attribute"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

// foldSeqsDepth bounds how many conversations keep the row pairing this
// service recorded for the last replay it rendered.
//
// The pairing is what turns a fold — which the graph expresses as a channel
// index range — into transcript rows. It is only needed until the fold is
// applied, which happens inside the turn that rendered it.
const foldSeqsDepth = 4

// foldDocument is the durable record of one conversation's compaction: the
// transcript rows a fold replaced with a summary.
//
// It is the read half of a design whose write half already existed here: the
// compact tool keeps a content-addressed copy of what it folded
// (sessions.DocumentCompact), and replay_full_history ignores both, so every
// turn replays the whole transcript and folds it again. This document is keyed
// by transcript row instead of content hash — rows are what the archive, the
// search index and the summary nodes already use as identity, and two
// identical messages must not be covered by one of them.
//
// Nothing reads it yet: writing it first (and checking it against the folds
// this process makes) is how the read side gets switched on without guessing.
type foldDocument struct {
	// Version is the shape of this document; 1 is the row-range form.
	Version int `json:"version"`
	// AtSeq is the newest transcript row the fold saw.
	AtSeq int64 `json:"at_seq"`
	// CoveredFrom/CoveredTo bound the replaced rows. A fold covers a
	// contiguous span of the replayed history, so the range is two numbers
	// rather than a set.
	CoveredFrom int64 `json:"covered_from"`
	CoveredTo   int64 `json:"covered_to"`
	// Rows is how many transcript rows the range names, which is what makes
	// a mis-mapped range visible without reading the archive back.
	Rows int `json:"rows"`
	// Summary is the text the model will see in place of those rows.
	Summary string `json:"summary"`
}

// foldEntry is one turn's replay pairing, waiting for the fold that may name
// part of it.
type foldEntry struct {
	seqs  []int64
	store *sessions.Store
}

// foldStash hands the replay pairing to the script bridge the way
// pendingHistory hands it the rendered messages: keyed by the run's board, so
// the bridge (which has the board and nothing else) can reach it without the
// service being threaded through the script-bindings resource.
var foldStash = newFoldStash(foldSeqsDepth)

type foldStashMap struct {
	depth int
	mu    sync.Mutex
	order []*agent.Board
	items map[*agent.Board]*foldEntry
}

func newFoldStash(depth int) *foldStashMap {
	return &foldStashMap{depth: depth, items: map[*agent.Board]*foldEntry{}}
}

// stashReplaySeqs records this turn's replay pairing for one board.
func stashReplaySeqs(board *agent.Board, store *sessions.Store, seqs []int64) {
	if board == nil || len(seqs) == 0 {
		return
	}
	foldStash.mu.Lock()
	defer foldStash.mu.Unlock()
	if _, ok := foldStash.items[board]; !ok {
		foldStash.order = append(foldStash.order, board)
	}
	foldStash.items[board] = &foldEntry{seqs: seqs, store: store}
	for len(foldStash.order) > foldStash.depth {
		oldest := foldStash.order[0]
		foldStash.order = foldStash.order[1:]
		delete(foldStash.items, oldest)
	}
}

func (c *foldStashMap) get(board *agent.Board) (*foldEntry, bool) {
	if c == nil || board == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.items[board]
	return entry, ok
}

// recordFold writes the durable fold document for the board's conversation.
//
// start and end are the channel indexes compact.js folded, counted from the
// channel's first message; the world sections sit in front of the replayed
// history, so the history index is start-sectionsCount. The range must line up
// with a contiguous span of the replay this turn rendered — anything else is
// refused and logged rather than shadowing the wrong rows on a later read.
func recordFold(
	ctx context.Context,
	board *agent.Board,
	conversationID string,
	sectionsCount, start, end int,
	summary string,
) error {
	entry, ok := foldStash.get(board)
	if !ok || entry.store == nil {
		// A deployment whose replay does not come from rows (bounded memory
		// mode) has nothing to record.
		return nil
	}
	if start < sectionsCount || end <= start {
		return fmt.Errorf(
			"worldstate: fold range [%d,%d) does not start inside the replayed history (sections %d)",
			start, end, sectionsCount)
	}
	from, to := start-sectionsCount, end-sectionsCount
	if to > len(entry.seqs) || to <= from {
		return fmt.Errorf(
			"worldstate: fold range [%d,%d) is outside the %d replayed rows",
			from, to, len(entry.seqs))
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return fmt.Errorf("worldstate: fold of %d rows has no summary", to-from)
	}
	doc := foldDocument{
		Version:     1,
		AtSeq:       entry.seqs[len(entry.seqs)-1],
		CoveredFrom: entry.seqs[from],
		CoveredTo:   entry.seqs[to-1],
		Rows:        to - from,
		Summary:     summary,
	}
	if err := entry.store.WriteState(
		conversationID, sessions.DocumentCompactFold, doc); err != nil {
		return fmt.Errorf("worldstate: record fold for %s: %w", conversationID, err)
	}
	telemetry.Info(ctx, "worldstate: recorded a durable fold",
		attribute.String("conversation.id", conversationID),
		attribute.Int64("covered_from", doc.CoveredFrom),
		attribute.Int64("covered_to", doc.CoveredTo),
		attribute.Int("rows", doc.Rows),
		attribute.Int("summary_chars", len(doc.Summary)))
	return nil
}
