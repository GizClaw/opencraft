package worldstate

import (
	"sync"

	"github.com/GizClaw/flowcraft/core/message"
)

// replayChars bounds the rendered replay this service keeps across all its
// conversations, counted in the text the messages carry.
//
// The rendered form is what the world node puts on the channel, so a cache of
// it replaces a cache of memory items rather than joining one: the same
// transcript used to be resident twice (decoded items plus the rendered
// messages), and a 45 MB / 15,356-message conversation alone accounted for
// 61 MB of a 118 MB live heap in the profile that motivated this.
const replayChars = 8 << 20

// replayDepth bounds how many conversations keep a rendered replay at once.
const replayDepth = 4

// renderedReplay is one conversation's rendered history and the transcript
// position it describes.
type renderedReplay struct {
	// seq is the newest transcript row the messages cover.
	seq   int64
	chars int
	msgs  []message.Message
}

// replayCache keeps the rendered full-history replay per conversation, keyed by
// the transcript position it was rendered from.
//
// Full-history replay means the transcript reaches the model every turn, and
// the engine's world node merges it onto the channel each round. Rendering it
// from the store decodes the whole conversation first (731 MB of allocation
// per turn on the 45 MB conversation this was measured on), so a turn that has
// not appended anything reuses the previous render. The check is a scalar
// (sessions.Store.MaxSeq), not a decode.
type replayCache struct {
	mu    sync.Mutex
	order []string
	items map[string]*renderedReplay
	chars int
}

func newReplayCache() *replayCache {
	return &replayCache{items: map[string]*renderedReplay{}}
}

// get returns the replay rendered for one conversation when it still describes
// the newest transcript row.
func (c *replayCache) get(conversationID string, seq int64) ([]message.Message, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.items[conversationID]
	if !ok || entry.seq != seq {
		return nil, false
	}
	return entry.msgs, true
}

// put records one rendered replay, evicting the oldest entries while the cache
// is over its conversation or character budget.
func (c *replayCache) put(conversationID string, seq int64, msgs []message.Message) {
	if c == nil {
		return
	}
	// An empty replay is cached like any other: a conversation with nothing
	// archived has not "no render", it has a render of nothing, and the
	// cursor is what decides when that stops being true.
	entry := &renderedReplay{seq: seq, chars: messageChars(msgs), msgs: msgs}
	c.mu.Lock()
	defer c.mu.Unlock()
	if previous, ok := c.items[conversationID]; ok {
		c.chars -= previous.chars
	} else {
		c.order = append(c.order, conversationID)
	}
	c.items[conversationID] = entry
	c.chars += entry.chars
	for len(c.order) > 1 && (len(c.order) > replayDepth || c.chars > replayChars) {
		oldest := c.order[0]
		c.order = c.order[1:]
		if evicted, ok := c.items[oldest]; ok {
			c.chars -= evicted.chars
			delete(c.items, oldest)
		}
	}
}

// messageChars sums the text one rendered replay carries, without building any
// of it: the same accounting the store budgets with.
func messageChars(msgs []message.Message) int {
	total := 0
	for _, msg := range msgs {
		for _, part := range msg.Content.Parts {
			normalized, err := message.NormalizePart(part)
			if err != nil {
				continue
			}
			switch typed := normalized.(type) {
			case message.TextPart:
				total += len(typed.Text)
			case message.ToolCallPart:
				total += len(typed.Call.Name) + len(typed.Call.Arguments)
			case message.ToolResultPart:
				total += messageChars([]message.Message{{Content: typed.Result.Content}})
			}
		}
	}
	return total
}
