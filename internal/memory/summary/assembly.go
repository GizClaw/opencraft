package summary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/route"
	"github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"
)

// TurnStore is the storage surface the summary assembly needs. Messages use
// the canonical sdk/message.Message type. Implement it with internal/state
// (SQLite) or a test double.
//
// Index-space contract: the store's conversation messages are all
// text-bearing (empty text is skipped at append), so every stored message
// has a stable seq in [0, N) and the N-th message's original index equals
// its seq. LoadMessagesRange returns the messages with original indices in
// [from, to]; message i of the result has original index from+i. Stable
// source IDs are derived from these original indices, so incremental paths
// can load only a bounded tail without shifting IDs.
type TurnStore interface {
	AppendMessages(ctx context.Context, conversationID, turnID string, msgs []message.Message) error
	LoadMessages(ctx context.Context, conversationID string) ([]message.Message, error)
	// CountMessages returns the thread's total text-message count.
	CountMessages(ctx context.Context, conversationID string) (int, error)
	// LoadMessagesRange returns messages with original index in [from, to]
	// (inclusive), ordered by index.
	LoadMessagesRange(ctx context.Context, conversationID string, from, to int) ([]message.Message, error)
	UpsertSummaryNode(ctx context.Context, node SummaryNode) error
	ListSummaryNodes(ctx context.Context, conversationID string) ([]SummaryNode, error)
	// DeleteSummaryNodes removes a thread's nodes at level except the node
	// whose id equals keepID (pass "" to delete all at that level).
	DeleteSummaryNodes(ctx context.Context, conversationID string, level int, keepID string) error
}

// Assembly implements flowcraft's sdk/memory.Assembly on top of the summary
// algorithms. Agent lifecycle hooks consume it as a TurnSink (commit) and
// ContextProvider (inject), so memory is wired through hooks and
// configuration rather than called ad hoc.
type Assembly struct {
	store             TurnStore
	policy            Policy
	generate          generateFunc // nil => buffer fold only
	now               func() time.Time
	replayFullHistory bool
	condenseWG        sync.WaitGroup
}

// generateFunc is the unary generation entry used for LLM condensation.
// WithRouter wires the deployment's inference router, which owns model
// selection and retry/fallback; tests inject a fake to avoid provider I/O.
type generateFunc func(
	ctx context.Context,
	req inference.GenerateRequest,
) (inference.GenerateResponse, error)

// AssemblyOption configures an Assembly.
type AssemblyOption func(*Assembly)

// WithAssemblyPolicy overrides the fold/compaction policy.
func WithAssemblyPolicy(p Policy) AssemblyOption {
	return func(a *Assembly) { a.policy = p }
}

// WithRouter enables the LLM layered compaction stage through the
// deployment's inference router. The router selects the model from the
// user-editable routing policy (router settings in the user config
// layer, opencraft.yaml) and applies its retry /
// fallback policy, so memory compaction follows the exact same routing
// as agent turns and cannot drift from it.
func WithRouter(router *route.Router) AssemblyOption {
	return func(a *Assembly) {
		a.generate = func(
			ctx context.Context,
			req inference.GenerateRequest,
		) (inference.GenerateResponse, error) {
			resp, _, err := router.Generate(ctx, req)
			return resp, err
		}
	}
}

// WithReplayFullHistory switches the context view from "folded summary +
// bounded raw window" to "full history replay". Folding is then left to
// the graph's budget-driven compact node; Context returns every stored
// message (respecting an explicit request budget when one is set).
func WithReplayFullHistory(enabled bool) AssemblyOption {
	return func(a *Assembly) { a.replayFullHistory = enabled }
}

// withGenerate wires a custom generation entry; tests use it to avoid
// provider I/O while exercising the condensation path.
func withGenerate(fn generateFunc) AssemblyOption {
	return func(a *Assembly) {
		a.generate = fn
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

// ReplayFullHistory reports whether the assembly is in full-replay mode.
// The worldstate uses it to decide between memory sections and the
// board-level history replay channel.
func (a *Assembly) ReplayFullHistory() bool { return a.replayFullHistory }

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

// fold loads only what the rolling fold needs and folds old messages into a
// level-0 node. The cost is bounded by the byte budget plus one bounded
// chunk — independent of conversation length — instead of scanning the whole
// thread on every turn.
func (a *Assembly) fold(ctx context.Context, conversationID string) error {
	p := a.policy.Normalize()
	total, err := a.store.CountMessages(ctx, conversationID)
	if err != nil {
		return memory.NewError(memory.KindInternal, "turn", err)
	}
	// Nothing has left the raw window yet: there is nothing to fold.
	if total <= p.MaxRawMessages+p.PreserveRecent {
		return nil
	}
	candidates, err := a.loadFoldTail(ctx, conversationID, total)
	if err != nil {
		return memory.NewError(memory.KindInternal, "turn", err)
	}
	nodes, err := a.store.ListSummaryNodes(ctx, conversationID)
	if err != nil {
		return memory.NewError(memory.KindInternal, "turn", err)
	}
	node, err := bufferFoldCandidates(p, conversationID, candidates, total, latestNode(nodes), a.now())
	if err != nil {
		return memory.NewError(memory.KindInternal, "turn", err)
	}
	if node == nil {
		return nil
	}
	rawText := node.Content.Text()
	var prev *SummaryNode
	if prev = latestNode(nodes); prev != nil {
		// The buffer fold writes its stable node in place. Once the LLM
		// condensed the current fold window, the stored content differs
		// from the raw buffer text, so BufferFold would emit a "new"
		// node every turn; the raw-text hash detects the no-change case
		// and skips a pointless regeneration.
		if hash, ok := prev.Metadata["condense_raw_hash"].(string); ok &&
			hash == hashText(rawText) {
			return nil
		}
	}
	var condense *condenseJob
	if a.shouldCondense(node) {
		// Merge the previous condensation with the current raw window
		// instead of re-compressing only the window. The rolling window
		// drops the OLDEST messages as it advances; without the merge the
		// previous condensation (which holds the facts of those dropped
		// messages) would be overwritten and the oldest needs and
		// decisions would be permanently lost — the same "middle hole"
		// the rolling window fixed at the buffer layer, reappearing one
		// layer up. The hash guard above hashes rawText only, so merging
		// prev cannot create a re-condense loop on unchanged windows.
		condenseInput := rawText
		if prev != nil {
			if prevText := prev.Content.Text(); prevText != "" {
				condenseInput = prevText + "\n" + rawText
			}
		}
		condense = &condenseJob{
			node: node, rawText: rawText, input: condenseInput, policy: p,
		}
	}
	// The level-0 buffer fold is a single rolling summary per thread:
	// retire any other level-0 nodes (e.g. rows written before the
	// stable-id fix) so summary_nodes never accumulates.
	if err := a.store.DeleteSummaryNodes(ctx, conversationID, 0, node.ID); err != nil {
		return memory.NewError(memory.KindInternal, "turn", err)
	}
	if err := a.store.UpsertSummaryNode(ctx, *node); err != nil {
		return memory.NewError(memory.KindInternal, "turn", err)
	}
	if condense != nil {
		// The LLM condensation is derived state: run it off the commit
		// path so a slow generation never delays turn completion. The
		// buffer node above is already persisted; the goroutine swaps
		// in the condensed content when the generation lands.
		a.condenseWG.Add(1)
		go func() {
			defer a.condenseWG.Done()
			a.runCondense(ctx, condense)
		}()
	}
	return nil
}

// condenseJob carries one pending LLM condensation: the buffer-fold
// node whose content should be replaced when the generation lands.
type condenseJob struct {
	node    *SummaryNode
	rawText string
	input   string
	policy  Policy
}

// runCondense executes one memory condensation asynchronously and
// re-upserts the summary node with the condensed content. Failures are
// best-effort: the buffer text stays persisted and the next fold
// retries when the window advances (an unchanged window short-circuits
// via the raw-text hash).
func (a *Assembly) runCondense(ctx context.Context, job *condenseJob) {
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	condensed, err := a.condense(runCtx, job.input, job.policy)
	if err != nil {
		return
	}
	// Cap the generated output to the summary budget so a verbose
	// generation cannot blow past MaxSummaryBytes and crowd the next
	// condensation input.
	if len(condensed) > job.policy.MaxSummaryBytes {
		condensed = truncateLines(condensed, job.policy.MaxSummaryBytes)
	}
	replacement := *job.node
	replacement.Content = message.Content{Parts: []message.Part{
		message.TextPart{Text: condensed},
	}}
	replacement.Metadata = make(map[string]any, len(job.node.Metadata)+2)
	for k, v := range job.node.Metadata {
		replacement.Metadata[k] = v
	}
	replacement.Metadata["algorithm"] = "summary_llm_condense"
	replacement.Metadata["condense_raw_hash"] = hashText(job.rawText)
	if err := a.store.UpsertSummaryNode(runCtx, replacement); err != nil {
		return
	}
}

// foldTailChunk bounds each range query while walking the foldable region
// backward. The total loaded is bounded by the byte budget plus one chunk,
// independent of conversation length.
const foldTailChunk = 64

// loadFoldTail loads only the newest foldable messages the rolling window
// can keep, walking backward from the fold boundary in bounded chunks until
// the loaded foldable content exceeds the byte budget (the budget is then
// provably full within the loaded tail) or the start of the conversation is
// reached. Messages older than the loaded tail are provably dropped by the
// budget, so they never need to be loaded: a fold costs O(budget) instead of
// an O(n) full scan. The returned candidates carry their original indices
// so stable source IDs stay identical to a full load.
func (a *Assembly) loadFoldTail(ctx context.Context, conversationID string, total int) ([]foldMsg, error) {
	p := a.policy.Normalize()
	w := p.MaxRawMessages + p.PreserveRecent
	end := total - w - 1 // newest foldable message index (inclusive)
	if end < 0 {
		return nil, nil
	}
	var tail []message.Message
	startIndex := 0
	foldBytes := 0
	from := end
	for {
		lo := from - foldTailChunk + 1
		if lo < 0 {
			lo = 0
		}
		batch, err := a.store.LoadMessagesRange(ctx, conversationID, lo, from)
		if err != nil {
			return nil, err
		}
		// batch is chronological within [lo, from]; prepend to keep the
		// accumulated tail chronological.
		tail = append(append([]message.Message{}, batch...), tail...)
		// Account rendered size (role prefix + text + separator), the same
		// budget the rolling window applies, so termination is exact.
		for i := lo; i <= from; i++ {
			foldBytes += len(renderMessage(batch[i-lo])) + 1
		}
		startIndex = lo
		if lo == 0 || foldBytes > p.MaxSummaryBytes {
			break
		}
		from = lo - 1
	}
	out := make([]foldMsg, len(tail))
	for i, msg := range tail {
		idx := startIndex + i
		out[i] = foldMsg{index: idx, id: stableMessageID(conversationID, idx, msg), msg: msg}
	}
	return out, nil
}

// shouldCondense reports whether the folded buffer benefits from LLM
// compaction: only once the rolling window actually dropped foldable
// messages (the byte budget is full and the oldest content would be
// lost). Small buffers stay pure buffer fold; CondenseFanout remains
// part of the policy surface for future layered compaction stages.
func (a *Assembly) shouldCondense(node *SummaryNode) bool {
	if a.generate == nil {
		return false
	}
	dropped, _ := node.Metadata["dropped_message_count"].(int)
	return dropped > 0
}

// condense runs one unary generation that rewrites the rolling summary —
// the previous condensation merged with the current raw window — into a
// shorter summary. Merging the previous condensation is what preserves the
// facts of messages the rolling window dropped; without it, each new
// condensation would overwrite the old one and the oldest needs and
// decisions would be permanently lost. The instruction keeps facts,
// decisions, paths, and commands; the response is trimmed and used
// verbatim as the new summary text.
func (a *Assembly) condense(
	ctx context.Context,
	rawText string,
	p Policy,
) (string, error) {
	if a.generate == nil {
		return "", errors.New("summary: condensation not configured")
	}
	maxOut := maxInt(256, p.MaxSummaryBytes/3)
	req := inference.GenerateRequest{
		Input: inference.GenerateInput{
			Role: inference.InputRoleUser,
			Content: inference.InputContent{
				Content: message.Content{Parts: []message.Part{
					message.TextPart{Text: condensePrompt(rawText)},
				}},
				Intent: inference.Intent{
					Text: &inference.TextIntent{MaxOutputTokens: &maxOut},
				},
			},
		},
	}
	resp, err := a.generate(ctx, req)
	if err != nil {
		return "", err
	}
	condensed := strings.TrimSpace(resp.Message.Content.Text())
	if condensed == "" {
		return "", errors.New("summary: condensation returned no text")
	}
	return condensed, nil
}

func condensePrompt(rawText string) string {
	const instruction = `You are a memory compaction assistant. Condense the ` +
		`conversation summary below into a shorter summary that preserves ` +
		`concrete facts, decisions, file paths, commands run, and user ` +
		`preferences. Drop filler and repetition. Output only the condensed ` +
		`summary, with no preamble.`
	return instruction + "\n\n" + rawText
}

func hashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Context implements memory.ContextProvider: packs summary nodes into model
// context under the request budget.
func (a *Assembly) Context(ctx context.Context, req memory.ContextRequest) (memory.ContextResult, error) {
	if err := req.Validate(); err != nil {
		return memory.ContextResult{}, err
	}
	if a.replayFullHistory {
		return a.replayContext(ctx, req)
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

	// Recent raw messages carry the conversation between folds. Only the
	// raw window (the last MaxRawMessages + PreserveRecent text messages)
	// is eligible: messages older than the window that the rolling summary
	// dropped are not covered, so without this bound they would leak back
	// into context and crowd out genuinely recent messages. The window is
	// loaded by original index (CountMessages + a single bounded range), so
	// context costs O(raw window) instead of an O(n) full scan. The newest
	// messages are kept first so a tight budget preserves the most relevant
	// tail; the appended chunk is reversed afterwards to keep chronological
	// order.
	p := a.policy.Normalize()
	total, err := a.store.CountMessages(ctx, req.ConversationID)
	if err != nil {
		return memory.ContextResult{}, memory.NewError(memory.KindInternal, "context", err)
	}
	boundary := total - p.MaxRawMessages - p.PreserveRecent
	if boundary < 0 {
		boundary = 0
	}
	var raw []message.Message
	if boundary < total {
		raw, err = a.store.LoadMessagesRange(ctx, req.ConversationID, boundary, total-1)
		if err != nil {
			return memory.ContextResult{}, memory.NewError(memory.KindInternal, "context", err)
		}
	}
	// raw[i] carries original index boundary+i.
	type rawCandidate struct {
		id  string
		msg message.Message
		seq int
	}
	candidates := make([]rawCandidate, 0, len(raw))
	for i, msg := range raw {
		id := stableMessageID(req.ConversationID, boundary+i, msg)
		if _, ok := covered[id]; ok {
			continue
		}
		candidates = append(candidates, rawCandidate{id: id, msg: msg, seq: boundary + i})
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

// replayContext returns every stored message as a raw context item in
// chronological order, skipping summary nodes entirely. A caller that
// sets a budget still gets it honored; the opencraft worldstate passes
// an empty budget in replay mode so the graph compact node owns the
// fold decision.
func (a *Assembly) replayContext(
	ctx context.Context,
	req memory.ContextRequest,
) (memory.ContextResult, error) {
	total, err := a.store.CountMessages(ctx, req.ConversationID)
	if err != nil {
		return memory.ContextResult{}, memory.NewError(memory.KindInternal, "context", err)
	}
	if total == 0 {
		return memory.ContextResult{}, nil
	}
	msgs, err := a.store.LoadMessagesRange(ctx, req.ConversationID, 0, total-1)
	if err != nil {
		return memory.ContextResult{}, memory.NewError(memory.KindInternal, "context", err)
	}
	items := make([]memory.ContextItem, 0, len(msgs))
	totalChars := 0
	truncated := false
	for i, msg := range msgs {
		text := msg.Content.Text()
		if text == "" {
			continue
		}
		if !fitsBudget(req.Budget, len(items), totalChars, len(text)) {
			truncated = true
			break
		}
		id := stableMessageID(req.ConversationID, i, msg)
		items = append(items, memory.ContextItem{
			ID:          id,
			Kind:        memory.ContextRawMessage,
			SourceClass: memory.ContextSourceRecent,
			Content:     msg.Content.Clone(),
			Score:       1,
			Sources:     []memory.SourceRef{{Kind: memory.SourceMessage, ID: id}},
			MessageRole: msg.Role,
			Sequence:    uint64(i),
		})
		totalChars += len(text)
	}
	return memory.ContextResult{
		Items:      items,
		TokenCount: 0,
		Truncated:  truncated,
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
