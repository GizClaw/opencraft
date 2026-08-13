package summary

import (
	"context"
	"errors"
	"time"

	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"
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
	assembly *inference.Assembly // nil => buffer fold only (P1: LLM compaction)
	model   inference.ModelRef
	now     func() time.Time
}

// AssemblyOption configures an Assembly.
type AssemblyOption func(*Assembly)

// WithAssemblyPolicy overrides the fold/compaction policy.
func WithAssemblyPolicy(p Policy) AssemblyOption {
	return func(a *Assembly) { a.policy = p }
}

// WithGenerateAssembly enables the LLM layered compaction stage via the
// canonical core/inference assembly.
func WithGenerateAssembly(asm *inference.Assembly, model inference.ModelRef) AssemblyOption {
	return func(a *Assembly) {
		a.assembly = asm
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
	items := make([]memory.ContextItem, 0, len(nodes)+8)
	covered := make(map[string]struct{}, 32)
	totalChars := 0
	truncated := false
	for i, node := range nodes {
		text := node.Content.Text()
		if !fitsBudget(req.Budget, len(items), totalChars, len(text)) {
			truncated = true
			break
		}
		sources := make([]memory.SourceRef, 0, len(node.SourceIDs))
		for _, id := range node.SourceIDs {
			sources = append(sources, memory.SourceRef{Kind: memory.SourceMessage, ID: id})
			covered[id] = struct{}{}
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

	// Recent raw messages carry the conversation between folds. The
	// newest messages are kept first so a tight budget preserves the
	// most relevant tail; the appended chunk is reversed afterwards to
	// keep chronological order.
	raw, err := a.store.LoadMessages(ctx, req.ConversationID)
	if err != nil {
		return memory.ContextResult{}, memory.NewError(memory.KindInternal, "context", err)
	}
	type rawCandidate struct {
		id   string
		msg  message.Message
		seq  int
	}
	candidates := make([]rawCandidate, 0, len(raw))
	for i, msg := range raw {
		if msg.Content.Text() == "" {
			continue
		}
		id := stableMessageID(req.ConversationID, i, msg)
		if _, ok := covered[id]; ok {
			continue
		}
		candidates = append(candidates, rawCandidate{id: id, msg: msg, seq: i})
	}
	rawCount := len(candidates)
	appended := make([]memory.ContextItem, 0, rawCount)
	for k := len(candidates) - 1; k >= 0; k-- {
		c := candidates[k]
		if !fitsBudget(req.Budget, len(items)+len(appended), totalChars, len(c.msg.Content.Text())) {
			truncated = true
			break
		}
		appended = append(appended, memory.ContextItem{
			ID:          c.id,
			Kind:        memory.ContextRawMessage,
			SourceClass: memory.ContextSourceRecent,
			Content:     c.msg.Content.Clone(),
			Score:       1,
			Sources:     []memory.SourceRef{{Kind: memory.SourceMessage, ID: c.id}},
			MessageRole: c.msg.Role,
			Sequence:    uint64(c.seq),
		})
		totalChars += len(c.msg.Content.Text())
	}
	for i, j := 0, len(appended)-1; i < j; i, j = i+1, j-1 {
		appended[i], appended[j] = appended[j], appended[i]
	}
	items = append(items, appended...)

	return memory.ContextResult{
		Items:      items,
		TokenCount: 0,
		Truncated:  truncated || len(appended) < rawCount,
	}, nil
}

func fitsBudget(b memory.Budget, itemCount, chars, textLen int) bool {
	if b.MaxItems > 0 && itemCount >= b.MaxItems {
		return false
	}
	if b.MaxChars > 0 && chars+textLen > b.MaxChars {
		return false
	}
	return true
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
