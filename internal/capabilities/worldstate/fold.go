package worldstate

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"go.opentelemetry.io/otel/attribute"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/utils/summarytext"
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

// applyFold replaces the rows a durable fold covers with the fold's summary.
//
// The model-visible history becomes "summary, then everything the fold did not
// touch" — the same view on every turn, whether or not this turn appended
// anything. That is what stops the transcript being folded again each turn: the
// rows are still in the archive (search, export and the timeline read them),
// they are simply behind the summary for the model.
//
// The summary sits at the first covered row's position, so a preserved message
// that came after the folded span keeps its order, and it carries that row's
// number: a later fold that covers the summary records the same start.
func applyFold(
	ctx context.Context,
	store *sessions.Store,
	conversationID string,
	raws []memory.ContextItem,
) []memory.ContextItem {
	if store == nil || len(raws) == 0 {
		return raws
	}
	var doc foldDocument
	if err := store.ReadState(conversationID, sessions.DocumentCompactFold, &doc); err != nil {
		return raws
	}
	if doc.Version != 1 || doc.CoveredTo < doc.CoveredFrom || doc.Summary == "" {
		telemetry.Warn(ctx, "worldstate: ignoring a malformed fold document",
			attribute.String("conversation.id", conversationID),
			attribute.Int("version", doc.Version))
		return raws
	}
	covered := func(seq int64) bool {
		return seq >= doc.CoveredFrom && seq <= doc.CoveredTo
	}
	out := make([]memory.ContextItem, 0, len(raws)+1)
	inserted := false
	removed := 0
	for _, item := range raws {
		seq := int64(item.Sequence)
		if covered(seq) {
			if !inserted {
				out = append(out, foldSummaryItem(doc))
				inserted = true
			}
			removed++
			continue
		}
		if !inserted && seq > doc.CoveredTo {
			// The covered rows are behind this one (a replay that starts
			// after them): the summary still belongs in front.
			out = append(out, foldSummaryItem(doc))
			inserted = true
		}
		out = append(out, item)
	}
	if !inserted && removed == 0 {
		return raws
	}
	telemetry.Info(ctx, "worldstate: replayed a folded history",
		attribute.String("conversation.id", conversationID),
		attribute.Int64("covered_from", doc.CoveredFrom),
		attribute.Int64("covered_to", doc.CoveredTo),
		attribute.Int("rows_hidden", removed))
	return out
}

// foldSummaryItem renders one fold's summary as the user-side context the model
// reads in place of the covered rows. It uses the same marker as the bounded
// memory mode and the compaction result itself (summarytext.SummaryPrefix), so
// a summary reads the same whichever path produced it.
func foldSummaryItem(doc foldDocument) memory.ContextItem {
	return memory.ContextItem{
		Kind:        memory.ContextRawMessage,
		SourceClass: memory.ContextSourceSummary,
		MessageRole: message.RoleUser,
		Content: message.NewTextContent(
			summarytext.SummaryPrefix + "\n" + doc.Summary),
		// The row the covered span starts at: ordering for this replay, and
		// the start a later fold records when it covers the summary in turn.
		Sequence: uint64(doc.CoveredFrom),
	}
}
