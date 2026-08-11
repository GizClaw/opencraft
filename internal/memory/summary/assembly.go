package summary

import (
	"context"
	"errors"
	"time"

	"github.com/GizClaw/flowcraft/sdk/inference"
	"github.com/GizClaw/flowcraft/sdk/memory"
	"github.com/GizClaw/flowcraft/sdk/message"
)

// TurnStore is the storage surface the summary assembly needs. Messages use
// the canonical sdk/message.Message type. Implement it with internal/state
// (SQLite) or a test double.
type TurnStore interface {
	AppendMessages(ctx context.Context, conversationID, turnID string, msgs []message.Message) error
	LoadMessages(ctx context.Context, conversationID string) ([]message.Message, error)
	UpsertSummaryNode(ctx context.Context, node SummaryNode) error
	ListSummaryNodes(ctx context.Context, conversationID string) ([]SummaryNode, error)
}

// Assembly implements flowcraft's sdk/memory.Assembly on top of the summary
// algorithms. Agent lifecycle hooks consume it as a TurnSink (commit) and
// ContextProvider (inject), so memory is wired through hooks and
// configuration rather than called ad hoc.
type Assembly struct {
	store   TurnStore
	policy  Policy
	runtime *inference.Runtime // nil => buffer fold only (P1: LLM compaction)
	model   inference.ModelRef
	now     func() time.Time
}

// AssemblyOption configures an Assembly.
type AssemblyOption func(*Assembly)

// WithAssemblyPolicy overrides the fold/compaction policy.
func WithAssemblyPolicy(p Policy) AssemblyOption {
	return func(a *Assembly) { a.policy = p }
}

// WithGenerateRuntime enables the LLM layered compaction stage via the
// canonical sdk/inference runtime.
func WithGenerateRuntime(rt *inference.Runtime, model inference.ModelRef) AssemblyOption {
	return func(a *Assembly) {
		a.runtime = rt
		a.model = model
	}
}

// NewAssembly builds a summary memory assembly. store is required.
func NewAssembly(store TurnStore, opts ...AssemblyOption) *Assembly {
	a := &Assembly{
		store:  store,
		policy: Policy{},
		now:    time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(a)
		}
	}
	return a
}

var _ memory.Assembly = (*Assembly)(nil)

// CommitTurn implements memory.TurnSink: persists canonical messages and
// folds the buffer when the raw window is exceeded. It is the hook entry
// point (agent Committer / SessionObserver.OnTurnFinished).
func (a *Assembly) CommitTurn(ctx context.Context, turn memory.Turn) error {
	if err := turn.Validate(); err != nil {
		return err
	}
	if err := a.store.AppendMessages(ctx, turn.ConversationID, turn.IdempotencyKey, turn.Messages); err != nil {
		return memory.NewError(memory.KindInternal, "turn", err)
	}
	return a.fold(ctx, turn.ConversationID)
}

// fold loads the conversation and folds old messages into a level-0 node.
func (a *Assembly) fold(ctx context.Context, conversationID string) error {
	messages, err := a.store.LoadMessages(ctx, conversationID)
	if err != nil {
		return memory.NewError(memory.KindInternal, "turn", err)
	}
	nodes, err := a.store.ListSummaryNodes(ctx, conversationID)
	if err != nil {
		return memory.NewError(memory.KindInternal, "turn", err)
	}
	node, err := BufferFold(a.policy, conversationID, messages, latestNode(nodes), a.now())
	if err != nil {
		return memory.NewError(memory.KindInternal, "turn", err)
	}
	if node == nil {
		return nil
	}
	if err := a.store.UpsertSummaryNode(ctx, *node); err != nil {
		return memory.NewError(memory.KindInternal, "turn", err)
	}
	return nil
}

// Context implements memory.ContextProvider: packs summary nodes into model
// context under the request budget.
func (a *Assembly) Context(ctx context.Context, req memory.ContextRequest) (memory.ContextResult, error) {
	if err := req.Validate(); err != nil {
		return memory.ContextResult{}, err
	}
	nodes, err := a.store.ListSummaryNodes(ctx, req.ConversationID)
	if err != nil {
		return memory.ContextResult{}, memory.NewError(memory.KindInternal, "context", err)
	}
	items := make([]memory.ContextItem, 0, len(nodes))
	totalChars := 0
	for i, node := range nodes {
		text := node.Content.Text()
		if req.Budget.MaxItems > 0 && len(items) >= req.Budget.MaxItems {
			break
		}
		if req.Budget.MaxChars > 0 && totalChars+len(text) > req.Budget.MaxChars {
			break
		}
		sources := make([]memory.SourceRef, 0, len(node.SourceIDs))
		for _, id := range node.SourceIDs {
			sources = append(sources, memory.SourceRef{Kind: memory.SourceMessage, ID: id})
		}
		items = append(items, memory.ContextItem{
			ID:          node.ID,
			Kind:        memory.ContextSummary,
			SourceClass: memory.ContextSourceSummary,
			Content:     node.Content.Clone(),
			Score:       1,
			Sources:     sources,
			Level:       node.Level,
			Sequence:    uint64(i),
			Timestamp:   node.CreatedAt,
		})
		totalChars += len(text)
	}
	return memory.ContextResult{
		Items:      items,
		TokenCount: 0,
		Truncated:  len(items) < len(nodes),
	}, nil
}

// PutDocument implements memory.DocumentSink. The summary assembly does not
// store documents.
func (a *Assembly) PutDocument(context.Context, memory.Document) error {
	return memory.NewError(memory.KindNotConfigured, "document",
		errors.New("summary assembly does not support documents"))
}

func latestNode(nodes []SummaryNode) *SummaryNode {
	var latest *SummaryNode
	for i := range nodes {
		if latest == nil || nodes[i].CreatedAt.After(latest.CreatedAt) {
			latest = &nodes[i]
		}
	}
	return latest
}
