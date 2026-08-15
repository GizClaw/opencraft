// Package memory wires the summary assembly into opencraft's deploy
// assembly: a SQLite-backed TurnStore adapter plus the deploy factory.
package memory

import (
	"context"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/opencraft/internal/memory/summary"
	"github.com/GizClaw/opencraft/internal/sessions/state"
)

// sqliteTurnStore adapts *state.Store to summary.TurnStore.
type sqliteTurnStore struct {
	s *state.Store
}

func (a *sqliteTurnStore) AppendMessages(
	ctx context.Context, conversationID, turnID string, msgs []message.Message,
) error {
	// NextSeq is a single indexed MAX(seq) lookup instead of loading every
	// item just to size the append: appends stay O(1) as the conversation
	// grows instead of degrading to O(n) per turn.
	seq, err := a.s.NextSeq(ctx, conversationID)
	if err != nil {
		return err
	}
	for i, msg := range msgs {
		if msg.Content.Text() == "" {
			continue
		}
		item := state.Item{
			ID:        conversationID + ":" + turnID + ":" + itoa(int(seq)+i),
			ThreadID:  conversationID,
			TurnID:    turnID,
			Seq:       seq,
			ItemType:  "text",
			Role:      string(msg.Role),
			Payload:   map[string]any{"text": msg.Content.Text()},
			CreatedAt: timeNow(),
		}
		if err := a.s.AppendItem(ctx, item); err != nil {
			return err
		}
		seq++
	}
	return nil
}

func (a *sqliteTurnStore) LoadMessages(
	ctx context.Context, conversationID string,
) ([]message.Message, error) {
	items, err := a.s.LoadItems(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	out := make([]message.Message, 0, len(items))
	for _, item := range items {
		text, _ := item.Payload["text"].(string)
		if text == "" {
			continue
		}
		out = append(out, message.NewTextMessage(message.Role(item.Role), text))
	}
	return out, nil
}

// CountMessages implements summary.TurnStore. All stored items are
// text-bearing (AppendMessages skips empty text), so the item count is the
// text-message count whose index space stable IDs are derived from.
func (a *sqliteTurnStore) CountMessages(
	ctx context.Context, conversationID string,
) (int, error) {
	return a.s.CountItems(ctx, conversationID)
}

// LoadMessagesRange implements summary.TurnStore. It returns the messages
// with original seq in [from, to]; message i of the result has original
// index from+i. No empty-text filtering is applied: the store only ever
// holds text-bearing items (AppendMessages skips empty text), so the index
// space of the range is exactly the seq space.
func (a *sqliteTurnStore) LoadMessagesRange(
	ctx context.Context, conversationID string, from, to int,
) ([]message.Message, error) {
	items, err := a.s.LoadItemsRange(ctx, conversationID, int64(from), int64(to))
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	out := make([]message.Message, 0, len(items))
	for _, item := range items {
		text, _ := item.Payload["text"].(string)
		out = append(out, message.NewTextMessage(message.Role(item.Role), text))
	}
	return out, nil
}

func (a *sqliteTurnStore) UpsertSummaryNode(
	ctx context.Context, node summary.SummaryNode,
) error {
	return a.s.UpsertSummaryNode(ctx, state.SummaryNode{
		ID:        node.ID,
		ThreadID:  node.ThreadID,
		Level:     node.Level,
		ParentIDs: node.ParentIDs,
		SourceIDs: node.SourceIDs,
		Content:   node.Content,
		CreatedAt: node.CreatedAt,
		UpdatedAt: node.UpdatedAt,
		Metadata:  node.Metadata,
	})
}

func (a *sqliteTurnStore) ListSummaryNodes(
	ctx context.Context, conversationID string,
) ([]summary.SummaryNode, error) {
	nodes, err := a.s.ListSummaryNodes(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	out := make([]summary.SummaryNode, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, summary.SummaryNode{
			ID:        node.ID,
			ThreadID:  node.ThreadID,
			Level:     node.Level,
			ParentIDs: node.ParentIDs,
			SourceIDs: node.SourceIDs,
			Content:   node.Content,
			CreatedAt: node.CreatedAt,
			UpdatedAt: node.UpdatedAt,
			Metadata:  node.Metadata,
		})
	}
	return out, nil
}

func (a *sqliteTurnStore) DeleteSummaryNodes(
	ctx context.Context, conversationID string, level int, keepID string,
) error {
	return a.s.DeleteSummaryNodes(ctx, conversationID, level, keepID)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
