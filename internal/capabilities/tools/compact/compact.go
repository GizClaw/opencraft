// Package compact provides the internal compact tool: it condenses a
// conversation prefix into a short summary and persists the artifact
// per conversation, so repeated compactions reuse previous work instead
// of re-summarizing the same messages.
package compact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/model"
	"github.com/GizClaw/flowcraft/core/inference/route"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"github.com/GizClaw/flowcraft/core/tool"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/ids"
	"github.com/GizClaw/opencraft/internal/foundation/utils/summarytext"
)

// Name is the canonical compact tool name.
const Name = "compact"

// DefaultBudgetChars is the default summary budget in characters.
const DefaultBudgetChars = 4096

// maxCondenseChars bounds one condensation request. A fold can carry
// megabytes of rendered messages — a single compaction call was observed
// at 15 MiB — and a prompt that size is rejected or billed whole by the
// provider, so larger folds are condensed in shards — in parallel, with
// the bound below — and the partial summaries are merged by a final pass.
// Folds under the cap keep the single-call path, so ordinary compaction is
// unchanged.
const maxCondenseChars = 400 << 10

// maxCondenseRounds bounds the merge passes for a fold with many shards:
// every pass replaces its input with summaries, so the size collapses
// after the first round and the loop is a safety net, not the norm.
const maxCondenseRounds = 3

// maxCondenseParallel bounds how many condensation requests one fold
// keeps in flight. Shards are independent summaries of one fold, so they
// are condensed together: the fold runs inside the turn (the next request
// has to carry its summary), and a multi-shard fold used to pay every
// shard's latency one after another. The bound keeps a 15 MiB fold from
// opening dozens of provider streams at once.
const maxCondenseParallel = 4

// Args is the compact tool input.
type Args struct {
	// Conversation is the messages to fold into the summary. Only the
	// newest overflow is expected here; messages already covered by a
	// previous compaction are merged in from the persisted artifact.
	Conversation []message.Message `json:"conversation"`
	// BudgetChars caps the summary length in characters.
	BudgetChars int `json:"budget_chars,omitempty"`
	// ConversationID identifies the per-conversation artifact store.
	ConversationID string `json:"conversation_id,omitempty"`
}

// artifact is the persisted compaction result for one conversation.
type artifact struct {
	// Covered is the cumulative set of folded message ids. Stored in
	// fold order so the union stays deterministic.
	Covered []string `json:"covered"`
	Summary string   `json:"summary"`
}

// generateFunc is the condensation entry; production wires the router,
// tests inject a fake.
type generateFunc func(
	ctx context.Context,
	req inference.GenerateRequest,
) (inference.GenerateResponse, error)

// condenseProbe is what the deployment says about one condensation call
// before it is sent: whether reasoning can be switched off, whether the
// smallest effort survives compilation, and the selected model's declared
// output limit. Every answer comes from the router's local compiler, so
// probing costs no provider I/O.
type condenseProbe struct {
	reasoningCanBeDisabled bool
	effortNative           bool
	maxOutputTokens        int
}

// probeFunc inspects the deployment for one condensation request. Tests
// inject their own; production wires the router.
type probeFunc func(ctx context.Context, req inference.GenerateRequest) condenseProbe

// Tool condenses conversation prefixes and persists the artifact. It
// is safe for concurrent use.
type Tool struct {
	store    *sessions.Store
	generate generateFunc
	probe    probeFunc
	observer func(context.Context, inference.Usage)
	mu       sync.Mutex
}

// New builds a compact tool whose condensation goes through the
// deployment router (same model selection/fallback as agent turns).
func New(
	router *route.Router,
	store *sessions.Store,
	observer func(context.Context, inference.Usage),
) *Tool {
	t := &Tool{store: store, observer: observer, probe: routerProbe(router)}
	if router != nil {
		t.generate = func(
			ctx context.Context,
			req inference.GenerateRequest,
		) (inference.GenerateResponse, error) {
			resp, _, err := router.Generate(ctx, req)
			if err == nil && t.observer != nil {
				t.observer(ctx, resp.Usage)
			}
			return resp, err
		}
	}
	return t
}

var _ tool.Tool = (*Tool)(nil)

// Definition describes the compact tool. It is reserved for the graph's
// compaction node; the dynamic catalog exposes it as hidden.
func (t *Tool) Definition() message.ToolDefinition {
	msgSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"role": map[string]any{"type": "string"},
			"content": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"parts": map[string]any{"type": "array"},
				},
			},
		},
		"required": []any{"role"},
	}
	return message.DefineSchema(
		Name,
		"Compresses an internal conversation prefix into a compact summary. "+
			"Reserved for the graph compaction node; do not call directly.",
		message.ToolArrayProperty("conversation",
			"The full messages to fold, oldest first.", msgSchema),
		message.ToolProperty("budget_chars", "integer",
			"Maximum summary length in characters."),
		message.ToolProperty("conversation_id", "string",
			"Conversation id for the persisted compaction artifact."),
	).Required("conversation").Build()
}

// Metadata reports the tool's execution metadata.
func (t *Tool) Metadata() tool.ToolMeta {
	return tool.ToolMeta{}
}

// Execute condenses the given conversation prefix, merging any
// previously persisted artifact, and returns the summary.
// Execute implements tool.Tool. The tool result is a single text part;
// the tool has no multimodal output.
func (t *Tool) Execute(ctx context.Context, arguments string) (message.Content, error) {
	out, err := t.execute(ctx, arguments)
	if err != nil {
		return message.Content{}, err
	}
	return message.NewTextContent(out), nil
}

// execute renders the tool's text result.
func (t *Tool) execute(ctx context.Context, arguments string) (string, error) {
	var args Args
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", errdefs.Validationf("compact: parse arguments: %v", err)
	}
	if len(args.Conversation) == 0 {
		return "", errdefs.Validationf("compact: conversation is required")
	}
	budget := args.BudgetChars
	if budget <= 0 {
		budget = DefaultBudgetChars
	}
	for i := range args.Conversation {
		if args.Conversation[i].Role == "" {
			args.Conversation[i].Role = message.RoleUser
		}
	}

	messageIDs := make([]string, len(args.Conversation))
	covered := map[string]bool{}
	for i, m := range args.Conversation {
		messageIDs[i] = stableID(m)
		covered[messageIDs[i]] = true
	}

	var art artifact
	// Delegated subagent runs mint ephemeral "ctx-" ids the session store
	// rejects (see the writer below): skip the load instead of warning on
	// every fold they run.
	if ids.IsSession(args.ConversationID) {
		if err := t.store.ReadStateStrict(
			args.ConversationID, sessions.DocumentCompact, &art,
		); err != nil && !errors.Is(err, os.ErrNotExist) {
			// Unreadable, and the store reported the row: drop whatever
			// the failed decode left behind, so this fold condenses from
			// scratch and rewrites the document below instead of trusting
			// half of it.
			art = artifact{}
		}
	}
	if art.Summary != "" && setsEqual(art.Covered, messageIDs) {
		return encodePatch(art.Summary)
	}

	// Only the messages this artifact does not cover yet need to be
	// condensed; the previous summary already holds the rest. Marked
	// summary messages are skipped too: their content is already part
	// of the previous summary merged below.
	var fresh []message.Message
	for i, m := range args.Conversation {
		if art.Summary != "" &&
			summarytext.IsSummaryText(summarytext.RenderMessage(m)) {
			continue
		}
		if !containsID(art.Covered, messageIDs[i]) {
			fresh = append(fresh, m)
		}
	}
	if len(fresh) == 0 {
		if art.Summary != "" {
			return encodePatch(art.Summary)
		}
		return "", errdefs.Validationf("compact: nothing new to compact")
	}
	if t.generate == nil {
		return "", errdefs.NotAvailablef("compact: condensation not configured")
	}

	summary := t.condenseFold(ctx, condenseInput{
		conversationID: args.ConversationID,
		fresh:          fresh,
		prevSummary:    art.Summary,
		budget:         budget,
	})
	// Identifiers the summarizer is entitled to paraphrase away ride
	// along verbatim: a folded region is only usable if a later turn can
	// still name the file it changed, the commit it landed, the issue it
	// closed. Extraction is mechanical (regex over the folded messages),
	// so this never invents anything and costs no model call.
	//
	// The block is reserved out of the summary budget rather than appended
	// on top of it: the artifact feeds the next fold as its "previous
	// summary", so an unbudgeted append would grow the conversation's
	// compaction state on every fold.
	index := summarytext.ExtractAnchors(fresh)
	index.AddText(art.Summary)
	summary = fitSummary(summary, index.Render(), budget)

	t.mu.Lock()
	defer t.mu.Unlock()
	merged := mergeIDs(art.Covered, messageIDs)
	// Delegated subagent runs mint ephemeral "ctx-" ids the session store
	// rejects. Their fold still applies to the channel; it just cannot
	// remember the artifact between rounds.
	if ids.IsSession(args.ConversationID) {
		telemetry.WarnErr(ctx, "compact: persist compaction state failed",
			t.store.WriteState(args.ConversationID, sessions.DocumentCompact, artifact{
				Covered: merged,
				Summary: summary,
			}), otellog.String("conversation.id", args.ConversationID))
	}
	return encodePatch(summary)
}

// Patch is the graph-facing return value: the exact message to insert
// into the conversation. The graph node no longer needs to know how
// summaries are rendered or marked.
type Patch struct {
	Message message.Message `json:"message"`
}

func encodePatch(summary string) (string, error) {
	raw, err := json.Marshal(Patch{
		Message: message.NewTextMessage(
			message.RoleUser,
			summarytext.SummaryPrefix+"\n"+summary,
		),
	})
	if err != nil {
		return "", fmt.Errorf("compact: encode patch: %w", err)
	}
	return string(raw), nil
}

func stableID(m message.Message) string {
	sum := sha256.Sum256([]byte(string(m.Role) + "\x00" + summarytext.RenderMessage(m)))
	return hex.EncodeToString(sum[:])
}

func renderMessages(msgs []message.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		role := string(m.Role)
		if role == "" {
			role = "user"
		}
		b.WriteString(role)
		b.WriteString(": ")
		b.WriteString(summarytext.RenderMessage(m))
		b.WriteString("\n")
	}
	return b.String()
}

// condenseRequest renders the condensation call. The instruction is a
// system message; the transcript is the current user turn, so the provider
// applies the instruction as context and never mixes it into the data. The
// output and reasoning knobs come from applyCondensePlan.
func condenseRequest(raw string) (inference.GenerateRequest, error) {
	systemText, err := renderSystemPrompt()
	if err != nil {
		return inference.GenerateRequest{}, fmt.Errorf(
			"compact: render condense prompt: %w", err)
	}
	return inference.GenerateRequest{
		Context: []message.Message{
			message.NewTextMessage(message.RoleSystem, systemText),
		},
		Input: inference.GenerateInput{
			Role: inference.InputRoleUser,
			Content: inference.InputContent{
				Content: message.Content{Parts: []message.Part{
					message.TextPart{Text: raw},
				}},
				Intent: inference.Intent{Text: &inference.TextIntent{}},
			},
		},
	}, nil
}

// Condensation output budgets. The cap has to cover the summary *and*
// whatever the model spends thinking before writing it: a reasoning model
// whose cap only fits the summary returns reasoning tokens, finish reason
// max_output, and no text at all — the failure this ladder exists to
// absorb.
const (
	// condenseMinOutput floors the cap so a tiny summary budget still
	// leaves room for an answer.
	condenseMinOutput = 512
	// condenseOutputCeiling bounds the walk upward: a summary is worth
	// paying for, a runaway generation is not.
	condenseOutputCeiling = 16384
	// condenseReasoningReserve is the first attempt's reserve for a model
	// whose reasoning cannot be switched off.
	condenseReasoningReserve = 2048
	// condenseRetryMargin is added on top of the observed reasoning spend
	// when the attempt ran out of output before writing text.
	condenseRetryMargin = 1024
)

// condensePlan is the shape of one condensation call: which reasoning knob
// the deployment can express (if any) and how much output the model may
// spend.
type condensePlan struct {
	reasoningOff bool
	effort       model.ReasoningEffort
	maxOutput    int
	declaredMax  int
}

// planCondense shapes a call from what the deployment declared. Reasoning
// off is preferred (it keeps the whole cap for prose); a model that cannot
// switch reasoning off still gets the smallest effort the deployment
// publishes; a model with no reasoning at all gets neither knob.
func planCondense(probe condenseProbe, budget int) condensePlan {
	plan := condensePlan{declaredMax: probe.maxOutputTokens}
	switch {
	case probe.reasoningCanBeDisabled:
		plan.reasoningOff = true
	case probe.effortNative:
		plan.effort = model.ReasoningMinimal
	}
	// The summary budget is a character count and the cap is tokens:
	// assume the worst case — one token per character (CJK) — so the prose
	// still fits when the digest is dense.
	plan.maxOutput = budget
	if !plan.reasoningOff {
		plan.maxOutput += condenseReasoningReserve
	}
	return plan
}

// outputTokens is the cap actually sent: bounded by the model's declared
// output limit and by the ceiling, never below the floor.
func (p condensePlan) outputTokens() int {
	out := p.maxOutput
	if p.declaredMax > 0 && out > p.declaredMax {
		out = p.declaredMax
	}
	if out > condenseOutputCeiling {
		out = condenseOutputCeiling
	}
	if out < condenseMinOutput {
		out = condenseMinOutput
	}
	return out
}

// growAfterMaxOutput raises the cap past the reasoning the attempt actually
// spent, so the retry can afford the thinking plus the prose.
func (p condensePlan) growAfterMaxOutput(reasoningTokens int64) condensePlan {
	if reasoningTokens > 0 {
		p.maxOutput += int(reasoningTokens)
	}
	p.maxOutput += condenseRetryMargin
	return p
}

// applyCondensePlan writes the plan's knobs onto the request's text intent.
// Reasoning off and an effort are mutually exclusive by contract, so the
// intent carries at most one of them.
func applyCondensePlan(req *inference.GenerateRequest, plan condensePlan) {
	if req.Input.Content.Intent.Text == nil {
		req.Input.Content.Intent.Text = &inference.TextIntent{}
	}
	text := req.Input.Content.Intent.Text
	maxOut := plan.outputTokens()
	text.MaxOutputTokens = &maxOut
	text.ReasoningEnabled = nil
	text.ReasoningEffort = ""
	if plan.reasoningOff {
		off := false
		text.ReasoningEnabled = &off
		return
	}
	text.ReasoningEffort = plan.effort
}

// routerProbe answers the condense probe from the router's local compiler.
// A knob the compiler would reject or drop must not be sent, so each one is
// explained first: reasoning off where the model publishes a toggle, the
// smallest declared effort where it cannot.
func routerProbe(router *route.Router) probeFunc {
	if router == nil {
		return nil
	}
	return func(ctx context.Context, base inference.GenerateRequest) condenseProbe {
		var out condenseProbe
		off := condenseProbeRequest(base, func(text *inference.TextIntent) {
			disabled := false
			text.ReasoningEnabled = &disabled
		})
		explanation, decision, err := router.ExplainGenerate(ctx, off)
		out.maxOutputTokens = declaredMaxOutput(router, decision.Selected)
		if err == nil && !fieldLost(
			explanation.Decisions, inference.FieldGenerateIntentReasoningEnabled,
		) {
			out.reasoningCanBeDisabled = true
			return out
		}
		minimal := condenseProbeRequest(base, func(text *inference.TextIntent) {
			text.ReasoningEffort = model.ReasoningMinimal
		})
		explanation, _, err = router.ExplainGenerate(ctx, minimal)
		out.effortNative = err == nil && !fieldLost(
			explanation.Decisions, inference.FieldGenerateIntentReasoningEffort,
		)
		return out
	}
}

// condenseProbeRequest clones the base request and applies one reasoning
// knob to a copy. Probing sets no output cap: the cap has no bearing on
// whether a knob compiles, and one the model cannot honor would fail the
// probe for the wrong reason.
func condenseProbeRequest(
	base inference.GenerateRequest,
	apply func(*inference.TextIntent),
) inference.GenerateRequest {
	probe := base.Clone()
	if probe.Input.Content.Intent.Text == nil {
		probe.Input.Content.Intent.Text = &inference.TextIntent{}
	}
	apply(probe.Input.Content.Intent.Text)
	return probe
}

// declaredMaxOutput reads the selected model's declared output limit. Zero
// means the deployment declares none.
func declaredMaxOutput(router *route.Router, ref model.ModelRef) int {
	if router == nil || ref.ID.Name == "" {
		return 0
	}
	target := router.Target()
	if target == nil {
		return 0
	}
	descriptor, err := target.InspectModel(ref)
	if err != nil || descriptor.Limits.MaxOutputTokens == nil {
		return 0
	}
	return *descriptor.Limits.MaxOutputTokens
}

// fieldLost reports whether the compiler could not carry one intent field
// into the provider request: a rejected field fails the whole compile, a
// dropped field is silently discarded. Either way the knob must not be sent,
// and a field the compile never reported on was not active at all.
func fieldLost(decisions []inference.Decision, field inference.FieldID) bool {
	for _, decision := range decisions {
		if decision.Field == field {
			return decision.Disposition != inference.Native
		}
	}
	return true
}

// condenseInput is one fold's condensation request: the transcript to
// summarize, the messages it was rendered from, and the knobs the graph
// passed.
type condenseInput struct {
	conversationID string
	raw            string
	fresh          []message.Message
	prevSummary    string
	budget         int
}

// condenseFold renders a fold and condenses it, sharding the transcript
// when it exceeds one request. Shards follow message boundaries, and a
// single message larger than the cap is sent as pieces of it: one provider
// window cannot take the message whole, but the pieces travel together and
// the fold keeps the text instead of cutting it.
func (t *Tool) condenseFold(ctx context.Context, in condenseInput) string {
	started := time.Now()
	raw := renderMessages(in.fresh)
	if in.prevSummary != "" {
		raw = in.prevSummary + "\n\n" + raw
	}
	if len(raw) <= maxCondenseChars {
		in.raw = raw
		return t.condenseText(ctx, in)
	}
	shards := shardMessages(in.fresh, maxCondenseChars)
	telemetry.Info(ctx, "compact: condensing a large fold in shards",
		otellog.String("conversation.id", in.conversationID),
		otellog.Int("fold.chars", len(raw)),
		otellog.Int("fold.shards", len(shards)),
		otellog.Int("fold.parallel", maxCondenseParallel))
	inputs := make([]condenseInput, 0, len(shards))
	for i, shard := range shards {
		// A shard that does not fit is one message larger than the cap: a
		// tool result carrying a log, a diff, a page of text. It is cut
		// into pieces rather than truncated — the pieces run in parallel
		// with every other shard, none of them exceeds one provider
		// window, and the fold keeps the text.
		for j, piece := range splitText(renderMessages(shard), maxCondenseChars) {
			if i == 0 && j == 0 && in.prevSummary != "" {
				piece = in.prevSummary + "\n\n" + piece
			}
			inputs = append(inputs, condenseInput{
				conversationID: in.conversationID,
				raw:            piece,
				fresh:          shard,
				prevSummary:    in.prevSummary,
				budget:         in.budget,
			})
		}
	}
	partials := t.condenseAll(ctx, inputs)
	// The fold's cost lands squarely in the turn (the next request has to
	// carry the summary), so its duration is the number that says whether
	// sharding is paying for itself.
	telemetry.Info(ctx, "compact: fold condensed",
		otellog.String("conversation.id", in.conversationID),
		otellog.Int("fold.chars", len(raw)),
		otellog.Int("fold.shards", len(shards)),
		otellog.Int("fold.requests", len(inputs)),
		otellog.Int64("fold.ms", time.Since(started).Milliseconds()))
	merged := strings.Join(partials, "\n\n")
	for round := 0; round < maxCondenseRounds && len(merged) > maxCondenseChars; round++ {
		chunks := splitText(merged, maxCondenseChars)
		inputs = make([]condenseInput, 0, len(chunks))
		for _, chunk := range chunks {
			inputs = append(inputs, condenseInput{
				conversationID: in.conversationID,
				raw:            chunk,
				fresh:          in.fresh,
				prevSummary:    in.prevSummary,
				budget:         in.budget,
			})
		}
		merged = strings.Join(t.condenseAll(ctx, inputs), "\n\n")
	}
	if len(merged) > maxCondenseChars {
		// Every model pass returned something too long to merge: fall back
		// to the mechanical digest so the fold still shrinks the channel.
		return summarytext.MechanicalSummary(in.fresh, in.prevSummary, in.budget)
	}
	return merged
}

// condenseAll condenses independent inputs with bounded parallelism and
// returns the summaries in input order. The mechanical digest stands in
// for an input the caller's context canceled before it started, so a
// cancel never leaves a hole in the merged summary.
func (t *Tool) condenseAll(
	ctx context.Context,
	inputs []condenseInput,
) []string {
	out := make([]string, len(inputs))
	if len(inputs) == 0 {
		return out
	}
	if len(inputs) == 1 {
		out[0] = t.condenseText(ctx, inputs[0])
		return out
	}
	sem := make(chan struct{}, maxCondenseParallel)
	var wg sync.WaitGroup
	for i, input := range inputs {
		wg.Add(1)
		go func(i int, input condenseInput) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				out[i] = summarytext.MechanicalSummary(
					input.fresh, input.prevSummary, input.budget)
				return
			}
			defer func() { <-sem }()
			out[i] = t.condenseText(ctx, input)
		}(i, input)
	}
	wg.Wait()
	return out
}

// shardMessages packs whole messages into shards of at most limit
// characters of rendered text. A message that exceeds the limit on its
// own becomes a shard of its own; the caller splits its rendering.
func shardMessages(msgs []message.Message, limit int) [][]message.Message {
	if len(msgs) == 0 {
		return nil
	}
	var out [][]message.Message
	var cur []message.Message
	size := 0
	for _, m := range msgs {
		rendered := len(renderMessages([]message.Message{m}))
		if len(cur) > 0 && size+rendered > limit {
			out = append(out, cur)
			cur = nil
			size = 0
		}
		cur = append(cur, m)
		size += rendered
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// splitText cuts text into chunks of at most limit characters, preferring
// line boundaries so a shard never starts mid-line when it can avoid it,
// and backing off to a rune boundary when the text has no line to cut on:
// a chunk boundary inside a multi-byte character would reach the provider
// as invalid UTF-8.
func splitText(text string, limit int) []string {
	if len(text) <= limit {
		return []string{text}
	}
	var out []string
	for len(text) > limit {
		cut := strings.LastIndexByte(text[:limit], '\n')
		if cut <= 0 {
			cut = limit
		}
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		if cut <= 0 {
			cut = limit
		}
		out = append(out, text[:cut])
		text = strings.TrimPrefix(text[cut:], "\n")
	}
	if text != "" {
		out = append(out, text)
	}
	return out
}

// condenseText produces the fold's summary text. It walks a short ladder —
// the planned reasoning knob, one retry with a cap grown past the reasoning
// the model actually spent, then a mechanical digest — and never fails the
// fold: the prompt has to shrink even when no model summary can be
// produced, because skipping the fold sends an over-window request and the
// provider rejects the whole turn.
func (t *Tool) condenseText(ctx context.Context, in condenseInput) string {
	base, err := condenseRequest(in.raw)
	if err != nil {
		telemetry.WarnErr(ctx, "compact: render condensation request failed",
			err, otellog.String("conversation.id", in.conversationID))
		return summarytext.MechanicalSummary(
			in.fresh, in.prevSummary, in.budget)
	}
	var probe condenseProbe
	if t.probe != nil {
		probe = t.probe(ctx, base)
	}
	plan := planCondense(probe, in.budget)
	for attempt := 1; attempt <= 2; attempt++ {
		req := base.Clone()
		applyCondensePlan(&req, plan)
		resp, err := t.generate(ctx, req)
		if err != nil {
			// A provider failure says nothing about the next fold, but the
			// fold still has to happen: fold mechanically and report it.
			telemetry.WarnErr(ctx, "compact: condensation call failed",
				err,
				otellog.String("conversation.id", in.conversationID),
				otellog.Int("condense.attempt", attempt),
				otellog.Int("condense.max_output_tokens", plan.outputTokens()),
				otellog.Bool("condense.reasoning_off", plan.reasoningOff))
			return summarytext.MechanicalSummary(
				in.fresh, in.prevSummary, in.budget)
		}
		if summary := strings.TrimSpace(resp.Message.Content.Text()); summary != "" {
			return summary
		}
		t.warnEmptyCondensation(ctx, in, resp, plan, attempt)
		if resp.FinishReason != inference.FinishMaxOutput || attempt == 2 {
			break
		}
		plan = plan.growAfterMaxOutput(reasoningTokens(resp))
	}
	telemetry.Warn(ctx, "compact: folding a mechanical digest",
		otellog.String("conversation.id", in.conversationID),
		otellog.Int("fresh.messages", len(in.fresh)),
		otellog.Int("input.chars", len(in.raw)),
		otellog.Bool("condense.reasoning_off", plan.reasoningOff),
		otellog.Int("condense.max_output_tokens", plan.outputTokens()))
	return summarytext.MechanicalSummary(in.fresh, in.prevSummary, in.budget)
}

// warnEmptyCondensation reports an attempt that came back without text. A
// reasoning model that spends its whole output budget thinking is the
// common cause, so the finish reason, the part kinds, and the output-token
// split are logged to tell that apart from a provider that returned
// nothing at all.
func (t *Tool) warnEmptyCondensation(
	ctx context.Context,
	in condenseInput,
	resp inference.GenerateResponse,
	plan condensePlan,
	attempt int,
) {
	textParts, reasoningParts := 0, 0
	for _, part := range resp.Message.Content.Parts {
		switch part.(type) {
		case message.TextPart:
			textParts++
		case message.ReasoningPart:
			reasoningParts++
		}
	}
	telemetry.Warn(ctx, "compact: condensation returned no text",
		otellog.String("conversation.id", in.conversationID),
		otellog.String("provider", resp.Metadata.Model.Provider),
		otellog.String("model", resp.Metadata.Model.Name),
		otellog.String("finish.reason", string(resp.FinishReason)),
		otellog.Bool("finish.synthesized", resp.FinishSynthesized),
		otellog.Int("message.parts", len(resp.Message.Content.Parts)),
		otellog.Int("text.parts", textParts),
		otellog.Int("reasoning.parts", reasoningParts),
		otellog.Int("fresh.messages", len(in.fresh)),
		otellog.Int("input.chars", len(in.raw)),
		otellog.Int("condense.attempt", attempt),
		otellog.Int("max.output.tokens", plan.outputTokens()),
		otellog.Bool("condense.reasoning_off", plan.reasoningOff),
		otellog.Int64("output.tokens", resp.Usage.OutputTokens),
		otellog.Int64("reasoning.tokens", reasoningTokens(resp)),
		otellog.String("request.id", resp.Metadata.RequestID))
}

// reasoningTokens reads the reasoning spend of one response, or -1 when the
// provider did not report it.
func reasoningTokens(resp inference.GenerateResponse) int64 {
	if rt := resp.Usage.Output.ReasoningTokens; rt != nil {
		return *rt
	}
	return -1
}

func containsID(ids []string, want string) bool {
	return slices.Contains(ids, want)
}

// fitSummary fits generated prose plus its identifier block into the
// node's summary budget. The budget is a cap, not a target: the artifact is
// stored as the conversation's compaction state and handed back as the
// previous summary on the next fold, so anything over it grows every time.
//
// The identifier block is what a later turn cannot reconstruct (paths,
// commits, issue numbers), so it is reserved first and the prose takes what
// is left. When the block alone would exceed the budget the budget still
// wins — the block is bounded and internally ordered (files first), so
// truncating it drops the least useful end.
func fitSummary(prose, anchors string, budget int) string {
	if budget <= 0 {
		budget = DefaultBudgetChars
	}
	anchors = strings.TrimSpace(anchors)
	if anchors == "" {
		return truncateRunes(prose, budget)
	}
	block := "\n\n" + anchors
	remaining := budget - utf8.RuneCountInString(block)
	if remaining <= 0 {
		return truncateRunes(anchors, budget)
	}
	prose = truncateRunes(prose, remaining)
	if prose == "" {
		return anchors
	}
	return prose + block
}

// truncateRunes cuts s to at most n runes, never inside one.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

func setsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]bool, len(a))
	for _, id := range a {
		seen[id] = true
	}
	for _, id := range b {
		if !seen[id] {
			return false
		}
	}
	return true
}

func mergeIDs(prev, next []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(prev)+len(next))
	for _, id := range append(append([]string(nil), prev...), next...) {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
