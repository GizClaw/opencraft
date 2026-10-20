package worldstate

import (
	"encoding/json"
	"strings"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/utils/summarytext"
)

// The context meter's inputs are split in two, on purpose.
//
// The estimates are written here, at prepare time, because this is the
// one place that holds the prompt the turn is about to send: the
// sections it just rendered, the tail block, and the transcript or raw
// window the memory provider replayed. Reading them back off the board at
// turn end would mean parsing and re-classifying what we had in hand.
//
// The measurement is added at turn end, from the llm node's own usage
// report (see ContextUsageFromBoard). No estimate can know the tool
// schemas the runtime attaches to the request, or how a provider frames
// its prompt, so the difference between the two numbers becomes the one
// residual category instead of being spread over the estimates.
//
// What the estimates describe is the prompt the turn *opened* with. A turn
// that folds mid-way rewrites the conversation prefix it was given
// (compact.js moves the folded rounds to another channel), so the
// conversation slice it estimated is a generation old and routinely an
// order of magnitude wider than what the call carried. That miss is
// expected rather than exceptional: a full-history deployment replays the
// transcript at the start of every turn and folds it again for that turn's
// window, so the estimate describes the input to compaction, not its
// output. Normalize therefore lets the measurement settle that one slice —
// the conversation gets what the count left after the rows rendered here —
// and leaves those rows at their own numbers, which are the ones the meter
// exists to show.

// contextEstimate is the board form of the prepare-time half: one token
// estimate per category id.
type contextEstimate struct {
	Categories map[string]int `json:"categories"`
}

// add accumulates tokens into one category, ignoring the empty ones so
// the board var stays small.
func (e contextEstimate) add(id string, tokens int) {
	if id == "" || tokens <= 0 {
		return
	}
	e.Categories[id] += tokens
}

// categories renders the estimates in session order, dropping the ids
// nothing landed in.
func (e contextEstimate) categories() []ocsessions.ContextCategory {
	count := 0
	for _, tokens := range e.Categories {
		if tokens > 0 {
			count++
		}
	}
	out := make([]ocsessions.ContextCategory, 0, count)
	for _, id := range ocsessions.ContextCategories {
		if tokens := e.Categories[id]; tokens > 0 {
			out = append(out, ocsessions.ContextCategory{ID: id, Tokens: tokens})
		}
	}
	return out
}

// estimateContextUsage splits one rendered prompt into the categories the
// meter draws.
//
// historyTokens is the replayed transcript in full-history mode; in
// bounded mode the raw window arrives as ordinary sections and is
// classified as conversation there. requestTokens is the user's own turn
// text: it is not a section (the engine puts it on the channel) but it is
// prompt the user's message paid for.
func estimateContextUsage(
	sections []Section,
	tailBlock string,
	historyTokens, requestTokens int,
) contextEstimate {
	est := contextEstimate{Categories: make(map[string]int, len(ocsessions.ContextCategories))}
	grouped := make(map[string][]message.Message, len(ocsessions.ContextCategories))
	for _, sec := range sections {
		id := sectionCategory(sec.ID)
		if id == "" {
			continue
		}
		grouped[id] = append(grouped[id], sec.Message)
	}
	for id, msgs := range grouped {
		est.add(id, summarytext.EstimateTokens(msgs))
	}
	est.add(ocsessions.ContextCategoryConversation, historyTokens+requestTokens)
	// The tail block is appended as an extra text part of the turn's
	// own message (world.js), so it carries no per-message overhead of
	// its own.
	est.add(ocsessions.ContextCategoryTurnContext, summarytext.EstimateText(tailBlock))
	return est
}

// sectionCategory maps a world-state section id onto the meter category
// it belongs to. It returns "" for ids the meter does not draw: those
// tokens land in the residual, which is where anything the harness
// cannot attribute belongs.
//
// The ids are the ones base.go and the service build: base fragments,
// the mode and personality overlays, the environment and permissions
// sections, AGENTS.md, the user-level memory injection, and the two
// memory-provider sections. A new section whose content is neither
// harness instructions nor conversation belongs here, not in a category
// of its own.
func sectionCategory(id string) string {
	switch id {
	case "environment", "permissions":
		return ocsessions.ContextCategoryEnvironment
	case "agents_md":
		return ocsessions.ContextCategoryProjectDocs
	case "user_memory":
		return ocsessions.ContextCategoryUserMemory
	case "memory_summary":
		return ocsessions.ContextCategoryMemorySummary
	case "memory_raw":
		return ocsessions.ContextCategoryConversation
	}
	switch {
	case strings.HasPrefix(id, "base_"),
		strings.HasPrefix(id, "mode_"),
		strings.HasPrefix(id, "personality_"):
		return ocsessions.ContextCategoryInstructions
	}
	return ""
}

// requestTextTokens estimates the turn's own request text as the one
// message the engine will put on the channel. Attachments are added to
// that message by the media hook, before the board exists, so they are
// not visible here; the residual covers them.
func requestTextTokens(reqText string) int {
	if reqText == "" {
		return 0
	}
	return summarytext.EstimateTokens([]message.Message{
		message.NewTextMessage(message.RoleUser, reqText),
	})
}

// writeContextEstimate publishes the prepare-time half on the board.
// A board can be a reused one (resume restores the previous run's
// board), so a turn that produced no estimate deletes the var instead of
// leaving the last turn's numbers to be measured against this turn's
// usage.
func (e contextEstimate) writeTo(board *agent.Board) {
	if board == nil {
		return
	}
	if len(e.Categories) == 0 {
		board.DeleteVar(config.BoardVarContextUsage)
		return
	}
	data, err := json.Marshal(e)
	if err != nil {
		board.DeleteVar(config.BoardVarContextUsage)
		return
	}
	board.SetVar(config.BoardVarContextUsage, string(data))
}

// ContextUsageFromBoard assembles the finished turn's context snapshot:
// what prepare estimated about the prompt, plus what the provider counted
// for it. ok is false when the turn never prepared a prompt (a custom
// graph, a turn that failed before its first call) or when the board's
// estimate is unreadable.
//
// The snapshot is a record of one request, not a property of the
// conversation: it describes the prompt the last model call of the turn
// carried, which is why the measurement and the estimates are paired
// here and nowhere else.
func ContextUsageFromBoard(board *agent.Board) (ocsessions.ContextUsage, bool) {
	if board == nil {
		return ocsessions.ContextUsage{}, false
	}
	raw, _ := board.GetVar(config.BoardVarContextUsage)
	text, _ := raw.(string)
	if text == "" {
		return ocsessions.ContextUsage{}, false
	}
	var est contextEstimate
	if err := json.Unmarshal([]byte(text), &est); err != nil {
		return ocsessions.ContextUsage{}, false
	}
	usage := ocsessions.ContextUsage{Categories: est.categories()}
	if measured, ok := boardUsage(board); ok {
		// The inclusive prompt size, the same number the compaction
		// budget is anchored on (sessions.PromptTokens): a provider
		// that reports its cache buckets outside the input count is
		// not showing a smaller prompt, it is counting it in parts,
		// and the window a model enforces counts them all.
		usage.MeasuredTokens = ocsessions.PromptTokens(measured)
		usage.Model = measured.Model.ID.Name
	}
	usage.Normalize()
	if !usage.Valid() {
		return ocsessions.ContextUsage{}, false
	}
	return usage, true
}
