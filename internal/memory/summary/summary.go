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

// foldMsg pairs a stable source ID with a canonical sdk message.
type foldMsg struct {
	id  string
	msg message.Message
}

// BufferFold folds messages older than the raw window into a level-0
// summary node. It never calls an LLM and is a pure function of its
// inputs. Returns nil when there is nothing new to fold.
//
// sdk/message.Message has no identity field, so each folded message gets a
// stable ID derived from (thread, original index, role, text). The same turn
// committed twice produces the same IDs, which keeps folding idempotent.
func BufferFold(policy Policy, threadID string, messages []message.Message, prev *SummaryNode, now time.Time) (*SummaryNode, error) {
	p := policy.Normalize()
	textMsgs := foldCandidates(threadID, messages)
	if len(textMsgs) <= p.MaxRawMessages {
		return nil, nil
	}
	foldBoundary := len(textMsgs) - p.MaxRawMessages
	if foldBoundary <= 0 {
		return nil, nil
	}

	covered := map[string]struct{}{}
	if prev != nil {
		for _, id := range prev.SourceIDs {
			covered[id] = struct{}{}
		}
	}

	var folded []foldMsg
	var foldedIDs []string
	for _, fm := range textMsgs[:foldBoundary] {
		if _, ok := covered[fm.id]; ok {
			continue
		}
		folded = append(folded, fm)
		foldedIDs = append(foldedIDs, fm.id)
	}
	if len(folded) == 0 {
		return nil, nil
	}

	var b strings.Builder
	if prev != nil && prev.Content.Text() != "" {
		b.WriteString(prev.Content.Text())
		b.WriteString("\n")
	}
	for i, fm := range folded {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(renderMessage(fm.msg))
	}

	sourceIDs := make([]string, 0, len(prevSourceIDs(prev))+len(foldedIDs))
	sourceIDs = append(sourceIDs, prevSourceIDs(prev)...)
	sourceIDs = append(sourceIDs, foldedIDs...)
	sourceIDs = dedupSorted(sourceIDs)

	parentIDs := []string(nil)
	if prev != nil {
		parentIDs = []string{prev.ID}
	}

	node := SummaryNode{
		ID:        stableID(threadID, 0, parentIDs, sourceIDs, p),
		ThreadID:  threadID,
		Level:     0,
		ParentIDs: parentIDs,
		SourceIDs: sourceIDs,
		Content: message.Content{Parts: []message.Part{
			message.TextPart{Text: truncateRunes(b.String(), p.MaxSummaryBytes)},
		}},
		CreatedAt: now,
		UpdatedAt: now,
		Metadata: map[string]any{
			"algorithm":            "summary_buffer",
			"max_raw_messages":     p.MaxRawMessages,
			"preserve_recent":      p.PreserveRecent,
			"max_summary_bytes":    p.MaxSummaryBytes,
			"folded_message_count": len(folded),
		},
	}
	return &node, nil
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
			id:  stableMessageID(threadID, i, msg),
			msg: msg,
		})
	}
	return out
}

func renderMessage(msg message.Message) string {
	role := string(msg.Role)
	if role == "" {
		role = "unknown"
	}
	return role + ": " + msg.Content.Text()
}

func prevSourceIDs(prev *SummaryNode) []string {
	if prev == nil {
		return nil
	}
	return prev.SourceIDs
}

func dedupSorted(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	slices.Sort(ids)
	return slices.Compact(ids)
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
	fmt.Fprintf(h, "%s|%d|%s|%s", threadID, index, msg.Role, msg.Content.Text())
	return hex.EncodeToString(h.Sum(nil))
}

func stableID(threadID string, level int, parentIDs, sourceIDs []string, p Policy) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%d|%v|%v|%d:%d:%d:%d",
		threadID, level, parentIDs, sourceIDs,
		p.MaxRawMessages, p.PreserveRecent, p.MaxSummaryBytes, p.CondenseFanout)
	return hex.EncodeToString(h.Sum(nil))
}
