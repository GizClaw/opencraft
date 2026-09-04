// Package summary ports the deterministic buffer-fold and LLM layered
// compaction algorithms from flowcraft's exp/recall-graph-ledger branch.
// Only the algorithms are migrated; the view/framework layers are not.
package summary

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/GizClaw/flowcraft/core/message"
)

// Policy configures summarization. Zero values select defaults.
type Policy struct {
	MaxRawMessages  int // default 32: raw window size
	PreserveRecent  int // default 4: newest messages kept raw
	MaxSummaryBytes int // default 4096
	CondenseFanout  int // default 4: LLM condensed-group size
	MaxLeafChars    int // default 6000: leaf chunk char cap
	MaxDepth        int // default 4: condense level cap
}

const (
	DefaultMaxRawMessages  = 32
	DefaultPreserveRecent  = 4
	DefaultMaxSummaryBytes = 4096
	DefaultCondenseFanout  = 4
	DefaultMaxLeafChars    = 6000
	DefaultMaxDepth        = 4
)

// Normalize returns a policy with defaults applied.
func (p Policy) Normalize() Policy {
	if p.MaxRawMessages <= 0 {
		p.MaxRawMessages = DefaultMaxRawMessages
	}
	if p.PreserveRecent <= 0 {
		p.PreserveRecent = DefaultPreserveRecent
	}
	if p.MaxRawMessages < p.PreserveRecent {
		p.MaxRawMessages = p.PreserveRecent
	}
	if p.MaxSummaryBytes <= 0 {
		p.MaxSummaryBytes = DefaultMaxSummaryBytes
	}
	if p.CondenseFanout <= 0 {
		p.CondenseFanout = DefaultCondenseFanout
	}
	if p.MaxLeafChars <= 0 {
		p.MaxLeafChars = DefaultMaxLeafChars
	}
	if p.MaxDepth <= 0 {
		p.MaxDepth = DefaultMaxDepth
	}
	return p
}

// SummaryNode is a node in a thread's summary DAG.
type SummaryNode struct {
	ID        string
	ThreadID  string
	Level     int
	ParentIDs []string
	SourceIDs []string
	Content   message.Content // multimodal-capable; buffer fold emits text
	CreatedAt time.Time
	UpdatedAt time.Time
	Metadata  map[string]any
}

// foldMsg pairs a canonical sdk message with its original index (used to
// derive the stable source ID) and the precomputed stable source ID.
type foldMsg struct {
	index int
	id    string
	msg   message.Message
}

// BufferFold folds messages older than the raw window into the thread's
// level-0 rolling summary. It never calls an LLM and is a pure function of
// its inputs. Returns nil when there is nothing new to fold.
//
// Design notes (fixes over the original chain-prev + tail-truncate fold):
//
//   - Rolling window: the summary keeps the NEWEST foldable messages that
//     fit MaxSummaryBytes and drops the OLDEST when full. The old code
//     prepended the previous summary and tail-truncated, so once the summary
//     reached the byte cap every new fold was truncated away and the memory
//     froze — the middle of long conversations was permanently lost. The
//     rolling window guarantees the most recent information always survives.
//
//   - Honest coverage: SourceIDs list exactly the messages rendered in the
//     summary text. Messages dropped by the rolling window are not covered,
//     so the summary never claims to contain text it does not.
//
//   - Stable node ID: the level-0 buffer fold is a single rolling summary
//     per thread, so its ID derives from (thread, level, policy) only and
//     the store Upsert replaces the previous node in place. The old ID
//     embedded the source IDs, so every fold inserted a new row and
//     summary_nodes accumulated without bound.
//
//   - PreserveRecent is real: the fold boundary keeps the last
//     MaxRawMessages + PreserveRecent messages raw, so the newest messages
//     get extra protection from folding.
//
// sdk/message.Message has no identity field, so each folded message gets a
// stable ID derived from (thread, original index, role, text). The same turn
// committed twice produces the same IDs, which keeps folding idempotent.
func BufferFold(policy Policy, threadID string, messages []message.Message, prev *SummaryNode, now time.Time) (*SummaryNode, error) {
	p := policy.Normalize()
	candidates := foldCandidates(threadID, messages)
	return bufferFoldCandidates(p, threadID, candidates, len(candidates), prev, now)
}

// bufferFoldCandidates is the rolling fold over an explicit candidate list.
// totalText is the thread's total text-message count (the boundary is a
// count of text messages, not a message index). candidates must be in
// chronological order and cover the NEWEST foldable messages the rolling
// window can keep: either the whole foldable region (positions
// [0, foldBoundary)) or a suffix of it that already overflows the byte
// budget. Anything older than that suffix is provably dropped by the budget,
// so it never needs to be loaded.
func bufferFoldCandidates(
	p Policy,
	threadID string,
	candidates []foldMsg,
	totalText int,
	prev *SummaryNode,
	now time.Time,
) (*SummaryNode, error) {
	// Messages older than the raw window plus the preserve-recent band are
	// fold candidates; the rest stay raw in context.
	foldBoundary := totalText - p.MaxRawMessages - p.PreserveRecent
	if foldBoundary <= 0 {
		return nil, nil
	}
	// Trim defensively to the foldable region (position-based; a caller may
	// pass the full candidate list including raw-window messages). A suffix
	// shorter than the boundary passes through untouched.
	if len(candidates) > foldBoundary {
		candidates = candidates[:foldBoundary]
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	kept, keptIDs := rollingWindow(p, candidates)
	if len(kept) == 0 {
		return nil, nil
	}
	text := renderKept(p, kept)
	// dropped counts every foldable message the budget could not hold,
	// including any older than a loaded suffix: the rolling window keeps
	// the NEWEST foldable messages that fit, so everything older than the
	// kept tail is dropped by construction.
	dropped := foldBoundary - len(kept)
	if dropped < 0 {
		dropped = 0
	}

	// Idempotency: an unchanged rolling window yields the same node; skip
	// the write. SourceIDs always match the rendered text, so nothing
	// silently claims coverage of dropped messages.
	if prev != nil && slices.Equal(prev.SourceIDs, keptIDs) && prev.Content.Text() == text {
		return nil, nil
	}

	return &SummaryNode{
		ID:        stableID(threadID, 0, p),
		ThreadID:  threadID,
		Level:     0,
		ParentIDs: nil,
		SourceIDs: keptIDs,
		Content:   message.Content{Parts: []message.Part{message.TextPart{Text: text}}},
		CreatedAt: now,
		UpdatedAt: now,
		Metadata: map[string]any{
			"algorithm":            "summary_buffer",
			"max_raw_messages":     p.MaxRawMessages,
			"preserve_recent":      p.PreserveRecent,
			"max_summary_bytes":    p.MaxSummaryBytes,
			"folded_message_count": len(kept),
			// How many foldable messages the byte budget dropped. The
			// LLM condensation stage keys off this: buffer fold only
			// needs compaction once it actually loses information.
			"dropped_message_count": dropped,
		},
	}, nil
}

// foldCandidates keeps text-bearing messages and assigns stable IDs. The
// original index is part of the ID so interleaved non-text messages do not
// shift previously folded IDs.
func foldCandidates(threadID string, messages []message.Message) []foldMsg {
	out := make([]foldMsg, 0, len(messages))
	for i, msg := range messages {
		if msg.Content.Text() == "" {
			continue
		}
		out = append(out, foldMsg{
			index: i,
			id:    stableMessageID(threadID, i, msg),
			msg:   msg,
		})
	}
	return out
}

// rollingWindow keeps the newest messages of foldable that fit the byte
// budget. Walking newest→oldest means the most recent information always
// survives: when the summary is full the OLDEST content is dropped instead
// of the newest. If even the newest message alone overflows the budget it
// is still kept and truncated at render time, so a single oversized tool
// output can never freeze the memory. The returned slice is in
// chronological order; SourceIDs correspond exactly to the kept messages;
func rollingWindow(p Policy, foldable []foldMsg) ([]foldMsg, []string) {
	kept := make([]foldMsg, 0, len(foldable))
	used := 0
	for i := len(foldable) - 1; i >= 0; i-- {
		fm := foldable[i]
		size := len(renderMessage(fm.msg)) + 1 // +1 for the "\n" separator
		if used+size > p.MaxSummaryBytes {
			if len(kept) == 0 {
				// The newest foldable message alone overflows: keep it so
				// recent information is never dropped; truncate at render.
				kept = append(kept, fm)
			}
			break
		}
		kept = append(kept, fm)
		used += size
	}
	// kept is newest-first; restore chronological order for rendering.
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	ids := make([]string, len(kept))
	for i := range kept {
		ids[i] = kept[i].id
	}
	return kept, ids
}

// renderKept renders kept messages in chronological order, truncating only
// the overflow tail of the newest message when it alone exceeds the budget.
func renderKept(p Policy, kept []foldMsg) string {
	var b strings.Builder
	for i, fm := range kept {
		if i > 0 {
			b.WriteString("\n")
		}
		text := renderMessage(fm.msg)
		if remaining := p.MaxSummaryBytes - b.Len(); len(text) > remaining {
			text = truncateLines(text, remaining)
		}
		b.WriteString(text)
		if b.Len() >= p.MaxSummaryBytes {
			break
		}
	}
	return b.String()
}

func renderMessage(msg message.Message) string {
	role := string(msg.Role)
	if role == "" {
		role = "unknown"
	}
	return role + ": " + msg.Content.Text()
}

// truncateLines cuts s to maxBytes, preferring a line boundary so code and
// structured output are not split mid-line; it falls back to a rune
// boundary and never splits a UTF-8 character.
func truncateLines(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	if idx := strings.LastIndexByte(s[:maxBytes], '\n'); idx >= 0 {
		return s[:idx+1]
	}
	return truncateRunes(s, maxBytes)
}

func truncateRunes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	end := maxBytes
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}

func stableMessageID(threadID string, index int, msg message.Message) string {
	h := sha256.New()
	if _, err := fmt.Fprintf(h, "%s|%d|%s|%s",
		threadID, index, msg.Role, msg.Content.Text()); err != nil {
		panic(fmt.Sprintf("summary: hash write failed: %v", err))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// stableID returns the stable node id for a thread's summary node at level.
// It deliberately excludes source IDs: the level-0 buffer fold is a single
// rolling summary per thread, so the id must stay constant across folds for
// the store Upsert to replace the node in place instead of accumulating
// rows.
func stableID(threadID string, level int, p Policy) string {
	h := sha256.New()
	if _, err := fmt.Fprintf(h, "%s|%d|%d:%d:%d",
		threadID, level, p.MaxRawMessages, p.PreserveRecent,
		p.MaxSummaryBytes); err != nil {
		panic(fmt.Sprintf("summary: hash write failed: %v", err))
	}
	return hex.EncodeToString(h.Sum(nil))
}
