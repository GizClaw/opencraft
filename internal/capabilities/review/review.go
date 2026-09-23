// Package review owns the post-turn write-back review: after a turn
// that looks worth reviewing (the configured cadence, enough tool use,
// or a failure), one bounded inference call reads the turn and proposes
// durable facts. Nothing is written into long-term memory here — the
// proposals land in the review queue and wait for the user's verdict,
// which is the whole point of the feature: automatic memory writes
// degrade over weeks, a reviewed queue does not.
//
// The review runs detached from the turn (the turn is already committed
// when it starts), bounded by its own timeout, and reports its model
// usage back so a background call is never invisible in the usage
// tables.
package review

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/route"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	opmemory "github.com/GizClaw/opencraft/internal/capabilities/memory"
	"github.com/GizClaw/opencraft/internal/capabilities/memory/userstore"
	reviewstore "github.com/GizClaw/opencraft/internal/capabilities/review/store"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/skills"
	"github.com/GizClaw/opencraft/internal/foundation/utils/resourcedep"
)

// ResourceImpl is the deploy impl id of the review observe hook.
const ResourceImpl = "opencraft.review"

// OriginAttribute marks a run as review work. A review never triggers
// another review, so the marker is checked before anything else runs;
// a delegated review (or a future graph-based one) carries it in its
// request attributes.
const OriginAttribute = "review_origin"

// maxPromptFacts bounds how many existing facts ride into the prompt
// (the model needs to know what is already remembered to avoid
// duplicates, not to see the whole store).
const maxPromptFacts = 40

// maxPromptSkills bounds the skill-name list in the prompt.
const maxPromptSkills = 20

// maxTurnExcerptBytes bounds one excerpt in the prompt.
const maxTurnExcerptBytes = 2000

// Settings is the hook's deploy settings: where the reviewed
// conversation lives, which workspace a workspace-scoped fact belongs
// to, and the model-call intent.
type Settings struct {
	// WorkDir is the workspace root; a workspace-scoped fact is filed
	// under it (the same key the remember tool and the page use).
	WorkDir string `json:"work_dir,omitempty"`
	// MaxOutputTokens bounds the review reply. The answer is a small
	// JSON document.
	MaxOutputTokens int `json:"max_output_tokens,omitempty"`
}

// Factory builds the opencraft.review observe hook. Every dependency is
// optional except the queue: a runtime without a user database, a
// router or a skills registry still assembles, it simply cannot review.
type Factory struct{}

var _ resource.Factory = Factory{}

// Spec implements resource.Factory.
func (Factory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "hook.observe",
		Impl: ResourceImpl,
		Deps: []resource.DepSpec{
			{Name: "queue", Type: reviewstore.ResourceKind, Required: true},
			{Name: "memory", Type: userstore.ResourceKind, Required: false},
			{Name: "skills", Type: skills.ResourceKind, Required: false},
			{Name: "sessions", Type: ocsessions.ResourceKind, Required: false},
			{Name: "router", Type: "inference.Router", Required: false},
			{Name: "observer", Type: opmemory.UsageObserverResourceKind, Required: false},
		},
	}
}

// New implements resource.Factory.
func (Factory) New(ctx context.Context, in resource.Input) (any, error) {
	queue, err := resourcedep.Required[*reviewstore.Binding](
		in, "review", "queue")
	if err != nil {
		return nil, err
	}
	settings, err := resource.DecodeTyped[Settings](ctx, in.Settings)
	if err != nil {
		return nil, err
	}
	o := &Observer{
		queue:    queue,
		settings: settings,
		turns:    map[string]int{},
		inflight: map[string]bool{},
	}
	if dep, ok := in.Dep("memory"); ok {
		if binding, ok := dep.(*userstore.Binding); ok {
			o.memory = binding
		}
	}
	if dep, ok := in.Dep("skills"); ok {
		if svc, ok := dep.(*skills.Service); ok {
			o.skills = svc
		}
	}
	if dep, ok := in.Dep("sessions"); ok {
		if store, ok := dep.(*ocsessions.Store); ok {
			o.sessions = store
		}
	}
	if dep, ok := in.Dep("router"); ok {
		if router, ok := dep.(*route.Router); ok {
			o.router = router
		}
	}
	if dep, ok := in.Dep("observer"); ok {
		if observer, ok := dep.(opmemory.UsageObserver); ok {
			o.usage = observer
		}
	}
	return o, nil
}

// Observer is the review observe hook.
type Observer struct {
	agent.BaseObserver

	queue    *reviewstore.Binding
	memory   *userstore.Binding
	skills   *skills.Service
	sessions *ocsessions.Store
	router   *route.Router
	usage    opmemory.UsageObserver
	settings Settings

	// generateFn, when non-nil, replaces the single router call the
	// review consists of. It exists so tests can drive the whole review
	// path (prompt, validation, queueing, usage) without an inference
	// provider; production leaves it nil and goes through the router.
	generateFn func(ctx context.Context, prompt promptInput) (inference.GenerateResponse, error)

	mu       sync.Mutex
	turns    map[string]int
	inflight map[string]bool
	wg       sync.WaitGroup
}

var _ agent.Observer = (*Observer)(nil)

// Close waits for in-flight reviews. Reviews are detached from the run
// that triggered them, so a runtime teardown has to wait for them
// explicitly or the store they write into may close underneath them.
func (o *Observer) Close() error {
	o.wg.Wait()
	return nil
}

// reasoningDisabled disables provider reasoning for the review call: the
// answer is a short JSON document, and a thinking pass only costs
// latency and tokens.
func reasoningDisabled() *bool {
	disabled := false
	return &disabled
}

// OnRunEnd decides whether this turn is worth reviewing and, when it is,
// starts the review detached from the turn. Observer side effects are
// best-effort by contract: a review failure is logged, never returned.
func (o *Observer) OnRunEnd(
	ctx context.Context, id agent.Identity, res *agent.Result,
) {
	if res == nil || o.reviewDisabled(ctx, id) {
		return
	}
	cfg := o.queue.Config
	if o.router == nil || o.queue.Queue == nil || o.queue.Queue.Empty() {
		return
	}
	toolCalls := countToolResults(res.Messages)
	failed := res.Status != agent.StatusCompleted
	if failed {
		if !cfg.OnFailure {
			return
		}
	} else {
		if toolCalls < cfg.MinToolCalls {
			return
		}
		if !o.countTurn(id.ConversationID, cfg.EveryTurns) {
			return
		}
	}
	// A conversation may only have one review in flight: two reviews of
	// the same prefix would spend two model calls to queue the same
	// candidates.
	if !o.claim(id.ConversationID) {
		return
	}
	// The turn is already committed; the review outlives its context but
	// keeps its values (identity included) so usage and logs stay
	// attributable.
	detached := context.WithoutCancel(ctx)
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	o.wg.Add(1)
	go func() {
		defer o.wg.Done()
		defer o.release(id.ConversationID)
		runCtx, cancel := context.WithTimeout(detached, timeout)
		defer cancel()
		if err := o.review(runCtx, id, res, toolCalls); err != nil {
			telemetry.WarnErr(runCtx, "review: post-turn review failed", err,
				otellog.String("conversation.id", id.ConversationID),
				otellog.String("run.id", id.RunID))
		}
	}()
}

// reviewDisabled reports whether this run must not be reviewed: the
// feature is off, the queue cannot hold anything, or the run is itself
// review work (the recursion guard).
func (o *Observer) reviewDisabled(ctx context.Context, id agent.Identity) bool {
	if !o.queue.Enabled() {
		return true
	}
	if IsReviewRun(ctx) {
		return true
	}
	if info, ok := agent.RunInfoFromContext(ctx); ok {
		if info.Attribute(OriginAttribute) != "" {
			return true
		}
	}
	return strings.HasPrefix(id.ConversationID, reviewConversationPrefix)
}

// reviewConversationPrefix marks the conversations a review runs in.
// Subagent and review contexts are not user conversations; reviewing
// them would spend model calls on machinery.
const reviewConversationPrefix = "ctx-"

// IsReviewRun reports whether ctx belongs to review work. The marker is
// set on the review's own context so a nested observation (an observer
// running inside the review) cannot start a second review.
func IsReviewRun(ctx context.Context) bool {
	marked, _ := ctx.Value(reviewContextKey{}).(bool)
	return marked
}

// WithReviewContext marks ctx as review work.
func WithReviewContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, reviewContextKey{}, true)
}

type reviewContextKey struct{}

// claim reserves the review slot of one conversation.
func (o *Observer) claim(conversationID string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.inflight[conversationID] {
		return false
	}
	o.inflight[conversationID] = true
	return true
}

func (o *Observer) release(conversationID string) {
	o.mu.Lock()
	delete(o.inflight, conversationID)
	o.mu.Unlock()
}

// countTurn increments the conversation's completed-turn counter and
// reports whether this turn lands on the review cadence. The counter is
// per process: after a restart the cadence starts over, which is the
// right trade for not paying a query on every turn.
func (o *Observer) countTurn(conversationID string, everyTurns int) bool {
	if everyTurns <= 0 {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.turns) > 512 {
		// Bound the map: a long-lived process may see many
		// conversations, and the counter is only a cadence.
		for key := range o.turns {
			if key != conversationID {
				delete(o.turns, key)
				break
			}
		}
	}
	o.turns[conversationID]++
	return o.turns[conversationID]%everyTurns == 0
}

// runTurn is the projection of one finished turn the review prompt is
// built from.
type runTurn struct {
	Request   string
	Answer    string
	Tools     []string
	ToolCalls int
	Status    string
	Error     string
}

// review performs one review: build the prompt, call the model once,
// validate the proposals and queue them.
func (o *Observer) review(
	ctx context.Context, id agent.Identity, res *agent.Result, toolCalls int,
) error {
	ctx = WithReviewContext(ctx)
	turn := projectTurn(res, toolCalls)
	if strings.TrimSpace(turn.Request) == "" && strings.TrimSpace(turn.Answer) == "" {
		return nil
	}
	facts, err := o.existingFacts(ctx)
	if err != nil {
		return err
	}
	names := o.skillNames()
	prompt := buildPrompt(promptInput{
		Turn:     turn,
		Facts:    facts,
		Skills:   names,
		WorkDir:  o.settings.WorkDir,
		MaxItems: o.queue.Config.MaxSuggestions,
	})
	response, err := o.generate(ctx, prompt)
	if err != nil {
		return err
	}
	o.reportUsage(ctx, id, response.Usage)
	candidates, err := parseCandidates(response.Message.Content.Text(),
		maxParsedCandidates)
	if err != nil {
		// A malformed answer is a model failure, not a bug in the turn:
		// log it with the raw answer's length and move on.
		return errdefs.Internalf(
			"review: model answer is not usable: %v (len %d)",
			err, len(response.Message.Content.Text()))
	}
	if len(candidates) == 0 {
		return nil
	}
	return o.queueCandidates(ctx, id, candidates, facts, turn)
}

// generate runs the single inference call the review consists of.
func (o *Observer) generate(
	ctx context.Context, prompt promptInput,
) (inference.GenerateResponse, error) {
	if o.generateFn != nil {
		return o.generateFn(ctx, prompt)
	}
	maxTokens := o.settings.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	intent := &inference.TextIntent{
		MaxOutputTokens:  &maxTokens,
		ReasoningEnabled: reasoningDisabled(),
	}
	response, _, err := o.router.Generate(ctx, inference.GenerateRequest{
		Context: prompt.contextMessages(),
		Input: inference.GenerateInput{
			Role: inference.InputRoleUser,
			Content: inference.InputContent{
				Content: message.NewTextContent(prompt.userText()),
				Intent:  inference.Intent{Text: intent},
			},
		},
	})
	if err != nil {
		return inference.GenerateResponse{}, fmt.Errorf(
			"review: generate: %w", err)
	}
	return response, nil
}

// existingFacts reads the facts already stored for this workspace: the
// prompt lists them so the model does not propose what the user already
// accepted, and the queue step uses their dedupe keys to drop a
// duplicate before it becomes a suggestion.
func (o *Observer) existingFacts(ctx context.Context) ([]userstore.Fact, error) {
	if o.memory == nil || o.memory.Memory == nil || o.memory.Memory.Empty() {
		return nil, nil
	}
	facts, err := o.memory.Memory.List(ctx, userstore.Query{
		Workspace: o.settings.WorkDir,
		Limit:     o.memory.Config.InjectMaxItems,
	})
	if err != nil {
		return nil, fmt.Errorf("review: list memory: %w", err)
	}
	return facts, nil
}

// skillNames lists the installed skill names, bounded: the prompt only
// needs to know which skills exist so it can tell a durable fact from
// something already written down as a skill.
func (o *Observer) skillNames() []string {
	if o.skills == nil || !o.skills.Enabled() {
		return nil
	}
	all := o.skills.List()
	if len(all) > maxPromptSkills {
		all = all[:maxPromptSkills]
	}
	out := make([]string, 0, len(all))
	for _, sk := range all {
		out = append(out, sk.Name)
	}
	return out
}

// queueCandidates validates the proposals and stores the survivors.
// Every rejection keeps the queue honest: text limits come from the
// memory store, and a candidate the store would refuse on write is
// refused here instead of surfacing a suggestion that cannot be
// accepted.
func (o *Observer) queueCandidates(
	ctx context.Context,
	id agent.Identity,
	candidates []Candidate,
	facts []userstore.Fact,
	turn runTurn,
) error {
	known := map[string]bool{}
	for _, fact := range facts {
		known[userstore.DedupeKeyOf(fact.Text)] = true
	}
	queued := 0
	for i, candidate := range candidates {
		fact := userstore.Fact{
			Kind:               candidate.Kind,
			Text:               candidate.Text,
			SourceConversation: id.ConversationID,
			SourceRun:          id.RunID,
		}
		if candidate.Scope == userstore.ScopeWorkspace {
			fact.Scope = userstore.ScopeWorkspace
			fact.Workspace = o.settings.WorkDir
		} else {
			fact.Scope = userstore.ScopeGlobal
		}
		normalized, err := userstore.Validate(fact)
		if err != nil {
			telemetry.Info(ctx, "review: suggestion rejected",
				otellog.String("reason", err.Error()))
			continue
		}
		if known[normalized.DedupeKey] {
			continue
		}
		known[normalized.DedupeKey] = true
		payload, err := json.Marshal(Candidate{
			Text:   normalized.Text,
			Scope:  normalized.Scope,
			Kind:   normalized.Kind,
			Reason: candidate.Reason,
		})
		if err != nil {
			return fmt.Errorf("review: marshal suggestion: %w", err)
		}
		if _, err := o.queue.Queue.Create(ctx, reviewstore.Suggestion{
			// A stable id: replaying the same review of the same turn
			// cannot queue the same candidate twice.
			ID:                 suggestionID(id, i),
			Kind:               reviewstore.KindMemory,
			Payload:            payload,
			Reason:             candidate.Reason,
			SourceWorkspace:    o.settings.WorkDir,
			SourceConversation: id.ConversationID,
			SourceRun:          id.RunID,
		}); err != nil {
			return err
		}
		queued++
		if queued >= o.queue.Config.MaxSuggestions {
			break
		}
	}
	if queued > 0 {
		telemetry.Info(ctx, "review: suggestions queued",
			otellog.Int("count", queued),
			otellog.String("conversation.id", id.ConversationID),
			otellog.String("run.id", id.RunID),
			otellog.String("status", turn.Status))
	}
	return nil
}

// reportUsage attributes the review's model call. The turn it reviews
// is already committed, so the call is recorded like the other
// background generations (auto-title): the workspace-level usage tables
// see it, and the conversation total carries it too.
func (o *Observer) reportUsage(
	ctx context.Context, id agent.Identity, usage inference.Usage,
) {
	if o.usage != nil {
		o.usage.ReportUsage(ctx, usage)
	}
	if o.sessions == nil || id.ConversationID == "" {
		return
	}
	delta := ocsessions.UsageFromReport(usage)
	if delta.TotalTokens <= 0 {
		return
	}
	if err := o.sessions.AddUsage(ctx, id.ConversationID, delta); err != nil {
		telemetry.WarnErr(ctx, "review: record review usage failed", err,
			otellog.String("conversation.id", id.ConversationID))
	}
}

// suggestionID derives the stable id of one candidate: the reviewed run
// plus its position in the answer.
func suggestionID(id agent.Identity, index int) string {
	run := strings.TrimSpace(id.RunID)
	if run == "" {
		run = strings.TrimSpace(id.ConversationID)
	}
	return fmt.Sprintf("rv-%s-%d", run, index)
}

// projectTurn reduces a finished turn to what the prompt needs: the
// user's request, the last assistant answer, the tools that ran and the
// outcome. Full message bodies stay out: the review is about durable
// facts, not about replaying the turn.
func projectTurn(res *agent.Result, toolCalls int) runTurn {
	turn := runTurn{
		ToolCalls: toolCalls,
		Status:    string(res.Status),
	}
	for _, msg := range res.Messages {
		switch msg.Role {
		case message.RoleUser:
			if text := strings.TrimSpace(msg.Content.Text()); text != "" {
				if turn.Request == "" {
					turn.Request = text
				} else {
					turn.Request += "\n" + text
				}
			}
		case message.RoleAssistant:
			if text := strings.TrimSpace(msg.Content.Text()); text != "" {
				turn.Answer = text
			}
		}
		for _, part := range msg.Content.Parts {
			call, ok := part.(message.ToolCallPart)
			if !ok {
				continue
			}
			if name := strings.TrimSpace(call.Call.Name); name != "" {
				turn.Tools = appendUnique(turn.Tools, name)
			}
		}
	}
	turn.Request = excerpt(turn.Request, maxTurnExcerptBytes)
	turn.Answer = excerpt(turn.Answer, maxTurnExcerptBytes)
	return turn
}

func appendUnique(list []string, value string) []string {
	for _, existing := range list {
		if existing == value {
			return list
		}
	}
	return append(list, value)
}

// excerpt truncates one prompt excerpt on a rune boundary.
func excerpt(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

// countToolResults counts the tool results in one turn: each completed
// tool call produces exactly one, so it is the turn's real work volume.
func countToolResults(msgs []message.Message) int {
	n := 0
	for _, msg := range msgs {
		for _, part := range msg.Content.Parts {
			if _, ok := part.(message.ToolResultPart); ok {
				n++
			}
		}
	}
	return n
}
