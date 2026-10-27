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
	"github.com/GizClaw/flowcraft/core/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

// TurnStore is the storage surface the summary assembly needs: the read
// side of a conversation's transcript, plus the fold tree it maintains
// over it. Messages use the canonical flowcraft message.Message type.
// Implement it with the sessions state store
// (internal/capabilities/sessions/state, SQLite) or a test double.
//
// It has no write side. The transcript is written by the conversation's
// owner — capabilities/sessions, one writer, in the transaction that also
// maintains the search index and the counter caches — and this package
// only reads it and maintains its own summary_nodes.
//
// Transcript-coordinate contract: every row carries the immutable Seq it
// was appended with, and that Seq is the row's identity — the model
// window, the fold coverage set and the context item ids are all
// expressed in it. Reads are addressed by Seq and walk toward older rows,
// so a window, a fold tail or a pair lookback costs O(rows read) and
// never scans the conversation.
//
// The store owns the projection: it decides which transcript rows are
// replayable for the model (the memory adapter states the rules) and
// returns those rows only. Skipped rows never enter the assembly's
// arithmetic — windows are sized in replayable rows, not transcript rows,
// so filtering a row out cannot shrink a window.
type TurnStore interface {
	// MaxSeq returns the newest transcript Seq, or -1 when the
	// conversation has no rows.
	MaxSeq(ctx context.Context, conversationID string) (int64, error)
	// LoadBack returns the newest n replayable rows with Seq < beforeSeq,
	// in chronological order. Fewer than n rows means the start of the
	// conversation was reached.
	LoadBack(ctx context.Context, conversationID string, beforeSeq int64, n int) ([]StoredMessage, error)
	// LoadAll returns every replayable row, oldest first.
	LoadAll(ctx context.Context, conversationID string) ([]StoredMessage, error)
	// LoadAfter returns the replayable rows appended after afterSeq,
	// oldest first. The transcript is append-only, so a reader that
	// already holds the prefix only has to read what came after it.
	LoadAfter(ctx context.Context, conversationID string, afterSeq int64) ([]StoredMessage, error)
	UpsertSummaryNode(ctx context.Context, node SummaryNode) error
	// CondenseSummaryNode replaces the stored copy of want with
	// replacement, but only while the row still holds want exactly
	// (same content, same coverage, same update time). ok=false means a
	// newer fold or condensation landed first: the replacement is stale
	// and must be dropped. Comparison and write are one statement, so
	// nothing can land between them.
	CondenseSummaryNode(ctx context.Context, want, replacement SummaryNode) (bool, error)
	ListSummaryNodes(ctx context.Context, conversationID string) ([]SummaryNode, error)
	// DeleteSummaryNodes removes a thread's nodes at level except the node
	// whose id equals keepID (pass "" to delete all at that level).
	DeleteSummaryNodes(ctx context.Context, conversationID string, level int, keepID string) error
	// DeleteSummaryNodesByID removes the given nodes. It retires coverage
	// written in another identity generation, which no reader can map onto
	// transcript rows.
	DeleteSummaryNodesByID(ctx context.Context, conversationID string, ids []string) error
}

// Assembly implements flowcraft's sdk/memory.Assembly on top of the summary
// algorithms. Agent lifecycle hooks consume it as a TurnSink (commit) and
// ContextProvider (inject), so memory is wired through hooks and
// configuration rather than called ad hoc.
type Assembly struct {
	store             TurnStore
	policy            Policy
	generate          generateFunc // nil => buffer fold only
	usage             func(context.Context, inference.Usage)
	now               func() time.Time
	replayFullHistory bool
	// replay keeps the unbudgeted full-history prefix each conversation
	// has already rendered (see replayContext).
	replay *replayCache
	// workWG tracks everything that can still write: the in-flight
	// folds and the condensation goroutines they launch. Close flips
	// closed under closeMu and then waits here, so a teardown blocks
	// exactly as long as a write is possible.
	workWG  sync.WaitGroup
	closeMu sync.Mutex
	closed  bool
	// condensePerm serializes the LLM condensation stage: at most one
	// generation runs at a time, so two folds racing the same rolling
	// node cannot both pay a model call for a window the other is
	// already replacing (the freshness gate then drops the stale one).
	condensePerm chan struct{}
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
			if err == nil && a.usage != nil {
				a.usage(ctx, resp.Usage)
			}
			return resp, err
		}
	}
}

// WithUsageObserver installs a usage reporter for every LLM
// condensation generated by this assembly. The callback runs on the
// engine goroutine and must be non-blocking.
func WithUsageObserver(fn func(context.Context, inference.Usage)) AssemblyOption {
	return func(a *Assembly) { a.usage = fn }
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
		store:        store,
		policy:       Policy{},
		now:          time.Now,
		condensePerm: make(chan struct{}, 1),
		replay:       newReplayCache(replayCacheDepth),
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

// CommitTurn implements memory.TurnSink for the assembly: it folds the
// conversation's buffer when the raw window is exceeded, and writes
// nothing.
//
// The turn's messages are already in the transcript by the time a caller
// delivers them here: the conversation's owner (capabilities/sessions)
// writes the archive, and this assembly reads it back (see projection.go
// for how a stored row becomes a window row). Keeping the call is what
// lets the sink satisfy the core TurnSink contract, and folding here is
// the only side effect it can honestly have — a second copy of the
// messages is exactly what the transcript retired.
func (a *Assembly) CommitTurn(ctx context.Context, turn memory.Turn) error {
	if err := turn.Validate(); err != nil {
		return err
	}
	return a.fold(ctx, turn.ConversationID)
}

// FoldOnly runs the fold over the conversation's transcript. It is the
// production path: the archive write and the fold are separate steps
// because the fold must not run inside the write transaction.
func (a *Assembly) FoldOnly(ctx context.Context, conversationID string) error {
	return a.fold(ctx, conversationID)
}

// fold loads only what the rolling fold needs and folds old messages into a
// level-0 node. The cost is bounded by the byte budget plus bounded
// backward chunks (fold tail and pair lookback) — independent of
// conversation length — instead of scanning the whole thread on every turn.
//
// It refuses to start once Close ran: the store is no longer a safe
// target after the close, and teardown is already waiting on the folds
// that registered before it. A fold that arrives that late is skipped,
// not failed: the turn's own transcript is already written by then, and
// the rolling summary is derived state the next fold rebuilds — the
// same disposition the condensation registration below takes.
func (a *Assembly) fold(ctx context.Context, conversationID string) error {
	a.closeMu.Lock()
	if a.closed {
		a.closeMu.Unlock()
		telemetry.Info(ctx,
			"memory: fold skipped; the assembly is closing",
			attribute.String("conversation.id", conversationID))
		return nil
	}
	a.workWG.Add(1)
	a.closeMu.Unlock()
	defer a.workWG.Done()
	p := a.policy.Normalize()
	maxSeq, err := a.store.MaxSeq(ctx, conversationID)
	if err != nil {
		return memory.NewError(memory.KindInternal, "turn", err)
	}
	if maxSeq < 0 {
		return nil
	}
	w := p.MaxRawMessages + p.PreserveRecent
	// The raw window is the newest w replayable rows; if the conversation
	// has not filled it yet, nothing has left it and there is nothing to
	// fold.
	rawTail, err := a.store.LoadBack(ctx, conversationID, maxSeq+1, w)
	if err != nil {
		return memory.NewError(memory.KindInternal, "turn", err)
	}
	if len(rawTail) < w {
		return nil
	}
	// Pair-aware fold boundary: if the count-based boundary would fold
	// an assistant tool_call whose result stays raw, leave the call raw
	// too so structured history can replay the pair.
	foldBefore := rawTail[0].Seq
	if prefix, err := a.pairPrefix(
		ctx, conversationID, foldBefore, rawTail,
	); err != nil {
		return memory.NewError(memory.KindInternal, "turn", err)
	} else if len(prefix) > 0 {
		foldBefore = prefix[0].Seq
	}
	if foldBefore <= 0 {
		// Everything would be folded or a pair reaches the start of the
		// thread: keep the whole conversation raw for this turn.
		return nil
	}
	candidates, err := a.loadFoldTail(ctx, conversationID, foldBefore)
	if err != nil {
		return memory.NewError(memory.KindInternal, "turn", err)
	}
	nodes, err := a.store.ListSummaryNodes(ctx, conversationID)
	if err != nil {
		return memory.NewError(memory.KindInternal, "turn", err)
	}
	if err := a.retireStaleNodes(ctx, conversationID, nodes); err != nil {
		return memory.NewError(memory.KindInternal, "turn", err)
	}
	node, err := bufferFoldCandidates(
		p, conversationID, candidates, latestNode(nodes), a.now())
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
	//
	// The retirement and the write are two statements, hence two
	// transactions. That is accepted here because the rolling node is
	// derived state the next fold rebuilds from the transcript: an
	// interrupted pair costs a rebuild, not data. The order is the one
	// that fails visibly — a missing node reads as a smaller window
	// until the next turn, while an orphaned one would hold a window
	// the transcript has already moved past.
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
		// in the condensed content when the generation lands. One
		// generation runs at a time (condensePerm).
		//
		// Registration and the closed check share closeMu: Close can
		// only start waiting after the flip, so it never races an Add
		// (a zero-counter Add concurrent with Wait is a WaitGroup
		// misuse), and a fold that arrived after the flip launches
		// nothing into a store that is being torn down.
		a.closeMu.Lock()
		if a.closed {
			a.closeMu.Unlock()
			telemetry.Info(ctx,
				"memory: condensation skipped; the assembly is closing",
				attribute.String("conversation.id", conversationID))
			return nil
		}
		a.workWG.Add(1)
		a.closeMu.Unlock()
		go func() {
			defer a.workWG.Done()
			a.condensePerm <- struct{}{}
			defer func() { <-a.condensePerm }()
			a.runCondense(ctx, condense)
		}()
	}
	return nil
}

// Close waits for every in-flight fold and condensation to finish, then
// refuses new work. Folds and their condensation goroutines are the
// writers this teardown must drain before the shared session store goes
// away; the assembly layer closes io.Closer resources in reverse
// construction order, which is what calls this.
//
// The closed bit and the wait group live under one mutex: a fold either
// registers before Close flips the bit — and Wait sees it — or observes
// the bit and does not register. Adding to the group concurrently with
// Wait on a zero counter would be a WaitGroup misuse, and a late fold
// would otherwise write into the store while it is being torn down.
func (a *Assembly) Close() error {
	a.closeMu.Lock()
	a.closed = true
	a.closeMu.Unlock()
	a.workWG.Wait()
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
	// The generation may take minutes, and the node id is stable: a
	// newer fold can have advanced the same rolling node while this one
	// ran. Swapping the older condensation in then would roll the
	// stored window and its coverage back, so the swap is one
	// conditional write that lands only while the stored row is still
	// the exact buffer this job condensed. A separate check followed by
	// an unconditional write would leave the fold a window to land
	// between the two.
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
	ok, err := a.store.CondenseSummaryNode(runCtx, *job.node, replacement)
	if err != nil {
		telemetry.WarnErr(runCtx, "memory: write condensed summary failed", err,
			attribute.String("conversation.id", job.node.ThreadID))
		return
	}
	if !ok {
		telemetry.Info(runCtx,
			"memory: dropped a stale condensation; the buffer fold moved on",
			attribute.String("conversation.id", job.node.ThreadID))
	}
}

// foldTailChunk bounds each range query while walking the foldable region
// backward. The total loaded is bounded by the byte budget plus one chunk,
// independent of conversation length.
const foldTailChunk = 64

// pairLookbackChunk bounds each backward range query when extending the
// raw-window boundary across an assistant tool_call / tool result pair.
const pairLookbackChunk = 32

// pairLookbackMax caps the total distance pairPrefix may walk backward.
// Providers require tool results to follow their call immediately, so a
// required call is either in the current window or only a few messages
// behind its result; anything older is already folded or missing. The cap
// keeps one dangling tool result from turning every FoldOnly / Context
// into a full-history scan while still covering far more than the window.
const pairLookbackMax = 64

// loadFoldTail loads only the newest foldable rows the rolling window can
// keep, walking backward from beforeSeq (exclusive) in bounded chunks
// until the loaded foldable content exceeds the byte budget (the budget is
// then provably full within the loaded tail) or the start of the
// conversation is reached. Rows older than the loaded tail are provably
// dropped by the budget, so they never need to be loaded: a fold costs
// O(budget) instead of an O(n) full scan. The returned candidates carry
// their transcript Seq, so source IDs are identical to a full load's.
func (a *Assembly) loadFoldTail(
	ctx context.Context, conversationID string, beforeSeq int64,
) ([]foldMsg, error) {
	p := a.policy.Normalize()
	if beforeSeq <= 0 {
		return nil, nil
	}
	var tail []StoredMessage
	foldBytes := 0
	cursor := beforeSeq
	for cursor > 0 {
		batch, err := a.store.LoadBack(ctx, conversationID, cursor, foldTailChunk)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		// batch is chronological; prepend to keep the accumulated tail
		// chronological.
		tail = append(append([]StoredMessage{}, batch...), tail...)
		// Account rendered size (role prefix + text + separator), the same
		// budget the rolling window applies, so termination is exact.
		for _, row := range batch {
			foldBytes += len(renderMessage(row.Message())) + 1
		}
		cursor = batch[0].Seq
		if len(batch) < foldTailChunk || foldBytes > p.MaxSummaryBytes {
			break
		}
	}
	return foldCandidates(tail), nil
}

// pairPrefix returns the rows needed to complete every tool
// pairing that starts at or after boundary. It reports the adjusted
// boundary (the earliest required assistant call) as the prefix rows
// between it and the window start. When a required call is not found
// within the bounded lookback (already folded or missing) no prefix is
// returned; worldstate normalization then drops the orphaned result (or
// synthesizes an aborted output for an unmatched call) instead of
// emitting an invalid tool message.
func (a *Assembly) pairPrefix(
	ctx context.Context,
	conversationID string,
	windowStartSeq int64,
	raw []StoredMessage,
) ([]StoredMessage, error) {
	if windowStartSeq <= 0 {
		return nil, nil
	}
	required := map[string]bool{}
	for _, row := range raw {
		if row.Role != message.RoleTool {
			continue
		}
		for _, result := range row.Message().ToolResults() {
			required[result.CallID] = true
		}
	}
	if len(required) == 0 {
		return nil, nil
	}
	// Calls already inside the window need no lookback.
	for _, row := range raw {
		for _, call := range row.Message().ToolCalls() {
			delete(required, call.ID)
		}
	}
	if len(required) == 0 {
		return nil, nil
	}

	found := map[string]bool{}
	earliest := int64(-1)
	// walked holds every row scanned so far in chronological order;
	// the lookback cap bounds it.
	var walked []StoredMessage
	scanned := 0
	cursor := windowStartSeq
	for cursor > 0 && scanned < pairLookbackMax {
		n := pairLookbackChunk
		if remaining := pairLookbackMax - scanned; remaining < n {
			n = remaining
		}
		batch, err := a.store.LoadBack(ctx, conversationID, cursor, n)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		walked = append(append([]StoredMessage{}, batch...), walked...)
		for _, row := range batch {
			for _, call := range row.Message().ToolCalls() {
				if !required[call.ID] || found[call.ID] {
					continue
				}
				found[call.ID] = true
				if earliest < 0 || row.Seq < earliest {
					earliest = row.Seq
				}
			}
		}
		if len(found) == len(required) {
			// Return exactly the rows from the earliest required call so
			// the window grows by only the messages needed to complete
			// the pair.
			for i, row := range walked {
				if row.Seq >= earliest {
					return walked[i:], nil
				}
			}
			return walked, nil
		}
		scanned += len(batch)
		cursor = batch[0].Seq
		if len(batch) < n {
			break
		}
	}
	return nil, nil
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
	for _, node := range nodes {
		// Coverage from another identity generation describes positions in
		// a load order, not transcript rows: it cannot cover anything here
		// and is retired by the next fold.
		if !node.CurrentGeneration() {
			continue
		}
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
			Sequence:    uint64(len(items)),
			Timestamp:   node.CreatedAt,
		})
		totalChars += len(text)
	}

	// Recent raw messages carry the conversation between folds. Only the
	// raw window (the newest MaxRawMessages + PreserveRecent replayable
	// rows)
	// is eligible: messages older than the window that the rolling summary
	// dropped are not covered, so without this bound they would leak back
	// into context and crowd out genuinely recent messages. The window is
	// one bounded backward walk from the newest row, so context costs
	// O(raw window + bounded pair lookback) instead of an O(n) full scan.
	// The newest messages are kept first so a tight budget preserves the
	// most relevant tail; the appended chunk is reversed afterwards to
	// keep chronological order.
	p := a.policy.Normalize()
	maxSeq, err := a.store.MaxSeq(ctx, req.ConversationID)
	if err != nil {
		return memory.ContextResult{}, memory.NewError(memory.KindInternal, "context", err)
	}
	var raw []StoredMessage
	if maxSeq >= 0 {
		raw, err = a.store.LoadBack(
			ctx, req.ConversationID, maxSeq+1, p.MaxRawMessages+p.PreserveRecent)
		if err != nil {
			return memory.ContextResult{}, memory.NewError(memory.KindInternal, "context", err)
		}
	}
	// Never cut an assistant tool_call from its tool result: extend the
	// window start backward when the boundary splits a pair, so structured
	// tool history can replay with its tool role.
	if len(raw) > 0 {
		prefix, err := a.pairPrefix(ctx, req.ConversationID, raw[0].Seq, raw)
		if err != nil {
			return memory.ContextResult{}, memory.NewError(
				memory.KindInternal, "context", err)
		}
		if len(prefix) > 0 {
			raw = append(prefix, raw...)
		}
	}
	candidates := make([]StoredMessage, 0, len(raw))
	for _, row := range raw {
		id := SourceID(row.Seq)
		if _, ok := covered[id]; ok {
			continue
		}
		candidates = append(candidates, row)
	}
	rawCount := len(candidates)
	appended := make([]memory.ContextItem, 0, rawCount)
	for k := len(candidates) - 1; k >= 0; k-- {
		row := candidates[k]
		if !fitsBudget(req.Budget, len(items)+len(appended), totalChars, len(row.Content.Text())) {
			truncated = true
			break
		}
		id := SourceID(row.Seq)
		appended = append(appended, memory.ContextItem{
			ID:          id,
			Kind:        memory.ContextRawMessage,
			SourceClass: memory.ContextSourceRecent,
			Content:     row.Content.Clone(),
			Score:       1,
			Sources:     []memory.SourceRef{{Kind: memory.SourceMessage, ID: id}},
			MessageRole: row.Role,
			Sequence:    uint64(row.Seq),
		})
		totalChars += len(row.Content.Text())
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
// replayCacheDepth bounds how many conversations keep a rendered replay.
// One entry is a conversation's replayable prefix held as context items;
// two or three turns of a session in flight is the working set, and a
// workspace switch should not pin the previous conversations.
const replayCacheDepth = 4

// replayCacheChars bounds the replay text the cache keeps across all its
// conversations, in characters. The depth alone is not a bound: a transcript
// can be tens of megabytes (the one this was measured on is 45 MB of text, and
// alone accounts for 61 MB of the process's live heap), so four entries of
// that size would be ~180 MB for a feature whose point is to avoid re-decoding.
// Once the budget is spent the oldest entries go and the next turn decodes
// again — the cost a hit saves, paid once when a conversation comes back.
const replayCacheChars = 8 << 20

// replayCache keeps the unbudgeted replay prefix a conversation has
// already rendered, so the next turn pays for the rows it appended
// rather than for the whole transcript.
//
// Full-history replay means the transcript is decoded into the model
// window on every turn. On a 42 MB conversation (14,041 archived
// messages) that decode was 731 MB of allocation per turn — the largest
// single item in a measure of one turn's 6.78 GB. The transcript is
// append-only and the projection is per row, so the prefix never
// changes and never has to be decoded twice.
type replayCache struct {
	depth int
	// maxChars is the whole cache's text budget (see replayCacheChars).
	maxChars int
	mu       sync.Mutex
	order    []string
	items    map[string]*replayEntry
	// chars is what the entries hold together, kept beside the map so an
	// eviction does not have to re-walk every entry.
	chars int
}

type replayEntry struct {
	// seq is the newest transcript row the items cover.
	seq   int64
	chars int
	items []memory.ContextItem
}

func newReplayCache(depth int) *replayCache {
	return &replayCache{
		depth:    depth,
		maxChars: replayCacheChars,
		items:    map[string]*replayEntry{},
	}
}

// state returns the cached prefix for one conversation.
func (c *replayCache) state(conversationID string) (*replayEntry, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.items[conversationID]
	return entry, ok
}

// put records (or replaces) one conversation's rendered prefix.
func (c *replayCache) put(conversationID string, entry *replayEntry) {
	if c == nil || entry == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if previous, ok := c.items[conversationID]; ok {
		c.chars -= previous.chars
	} else {
		c.order = append(c.order, conversationID)
	}
	c.items[conversationID] = entry
	c.chars += entry.chars
	for len(c.order) > 1 && (len(c.order) > c.depth || c.chars > c.maxChars) {
		oldest := c.order[0]
		c.order = c.order[1:]
		if evicted, ok := c.items[oldest]; ok {
			c.chars -= evicted.chars
			delete(c.items, oldest)
		}
	}
	// An entry that alone exceeds the budget is still kept: the alternative is
	// decoding it again on every turn of the conversation being read.
}

// drop forgets one conversation's prefix.
func (c *replayCache) drop(conversationID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if dropped, ok := c.items[conversationID]; ok {
		c.chars -= dropped.chars
		delete(c.items, conversationID)
	}
	for i, id := range c.order {
		if id == conversationID {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
}

// replayContext renders the full transcript as one raw-message item per
// replayable row.
//
// The prefix is cached: an unbudgeted request asks only for the rows
// appended since the last render, which is what keeps a long session's
// per-turn cost proportional to its newest turn instead of to the whole
// conversation. A budgeted request cannot share that prefix, because
// what it returns depends on where the budget ran out.
func (a *Assembly) replayContext(
	ctx context.Context,
	req memory.ContextRequest,
) (memory.ContextResult, error) {
	if req.Budget.MaxItems > 0 || req.Budget.MaxChars > 0 {
		rows, err := a.store.LoadAll(ctx, req.ConversationID)
		if err != nil {
			return memory.ContextResult{}, memory.NewError(
				memory.KindInternal, "context", err)
		}
		items, _, truncated := replayItems(nil, 0, rows, req.Budget)
		return memory.ContextResult{Items: items, Truncated: truncated}, nil
	}
	maxSeq, err := a.store.MaxSeq(ctx, req.ConversationID)
	if err != nil {
		return memory.ContextResult{}, memory.NewError(
			memory.KindInternal, "context", err)
	}
	entry, cached := a.replay.state(req.ConversationID)
	switch {
	case cached && entry.seq == maxSeq:
		// Nothing was appended: the rendered prefix is the answer.
		return memory.ContextResult{Items: entry.items}, nil
	case cached && entry.seq < maxSeq:
		rows, err := a.store.LoadAfter(ctx, req.ConversationID, entry.seq)
		if err != nil {
			return memory.ContextResult{}, memory.NewError(
				memory.KindInternal, "context", err)
		}
		items, chars, _ := replayItems(entry.items, entry.chars, rows, req.Budget)
		a.replay.put(req.ConversationID, &replayEntry{
			seq: maxSeq, chars: chars, items: items,
		})
		return memory.ContextResult{Items: items}, nil
	default:
		// First read, or a transcript that shrank under us (a deleted
		// conversation): render from the start.
		rows, err := a.store.LoadAll(ctx, req.ConversationID)
		if err != nil {
			return memory.ContextResult{}, memory.NewError(
				memory.KindInternal, "context", err)
		}
		items, chars, truncated := replayItems(nil, 0, rows, req.Budget)
		if truncated {
			// Partial results are not a prefix of the transcript.
			a.replay.drop(req.ConversationID)
			return memory.ContextResult{Items: items, Truncated: true}, nil
		}
		a.replay.put(req.ConversationID, &replayEntry{
			seq: maxSeq, chars: chars, items: items,
		})
		return memory.ContextResult{Items: items}, nil
	}
}

// replayItems appends the rows to the prefix, cloning what enters so the
// caller cannot reach into the cache, and reports the prefix's char
// count and whether a budget stopped it.
func replayItems(
	prefix []memory.ContextItem,
	chars int,
	rows []StoredMessage,
	budget memory.Budget,
) ([]memory.ContextItem, int, bool) {
	items := make([]memory.ContextItem, len(prefix), len(prefix)+len(rows))
	copy(items, prefix)
	for _, row := range rows {
		text := row.Content.Text()
		if text == "" {
			continue
		}
		if !fitsBudget(budget, len(items), chars, len(text)) {
			return items, chars, true
		}
		id := SourceID(row.Seq)
		items = append(items, memory.ContextItem{
			ID:          id,
			Kind:        memory.ContextRawMessage,
			SourceClass: memory.ContextSourceRecent,
			Content:     row.Content.Clone(),
			Score:       1,
			Sources:     []memory.SourceRef{{Kind: memory.SourceMessage, ID: id}},
			MessageRole: row.Role,
			Sequence:    uint64(row.Seq),
		})
		chars += len(text)
	}
	return items, chars, false
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

// retireStaleNodes deletes coverage written in another identity
// generation. Its source ids describe positions in a load order, so no
// reader can map them onto transcript rows; the nodes are ignored on read
// and the next fold rebuilds the summary from the transcript. Retirement
// is lazy: a conversation pays for it when it next folds.
func (a *Assembly) retireStaleNodes(
	ctx context.Context, conversationID string, nodes []SummaryNode,
) error {
	var stale []string
	for _, node := range nodes {
		if !node.CurrentGeneration() {
			stale = append(stale, node.ID)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	telemetry.Info(ctx,
		"memory: retiring summary nodes from a previous identity generation",
		attribute.String("conversation.id", conversationID),
		attribute.Int("nodes", len(stale)))
	return a.store.DeleteSummaryNodesByID(ctx, conversationID, stale)
}

// latestNode returns the newest summary node whose coverage is expressed
// in this build's coordinate system; nodes from another generation are
// treated as absent.
func latestNode(nodes []SummaryNode) *SummaryNode {
	var latest *SummaryNode
	for i := range nodes {
		if !nodes[i].CurrentGeneration() {
			continue
		}
		if latest == nil || nodes[i].CreatedAt.After(latest.CreatedAt) {
			latest = &nodes[i]
		}
	}
	return latest
}
