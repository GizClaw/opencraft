package sessions

import (
	"sort"
	"time"
)

// Context usage is the meter the composer shows: how much of the serving
// model's input window one prompt takes, and which part of it is context
// the harness added.
//
// The document exists because the answer cannot be derived on read. The
// prompt a turn sends is assembled at prepare time and leaves no trace in
// the store — worldstate ships it to the graph as board vars and the
// runtime hands it to the provider — and the number the user cares about
// is the provider's own count of that prompt, which arrives on the turn's
// last inference report. Recording both at turn end is what lets a
// conversation be re-opened later (a fresh process, a workspace switch)
// and still show where its context went, instead of going blank until the
// user pays for another turn.
//
// The categories are wire values: the UI renders one row per id and keeps
// its own label table (i18n lives on that side). Adding a category is an
// append on both sides, never a rename — an id the UI does not know is
// dropped from the rows, and the meters on old conversations would
// otherwise lose a slice silently.
const (
	// ContextCategoryInstructions is the system-role instruction layer:
	// the base fragments, the collaboration mode and the personality
	// overlay.
	ContextCategoryInstructions = "instructions"
	// ContextCategoryEnvironment is the environment and permissions
	// sections: workspace facts, the sandbox policy and the approved
	// command prefixes.
	ContextCategoryEnvironment = "environment"
	// ContextCategoryProjectDocs is AGENTS.md (project instructions).
	ContextCategoryProjectDocs = "project_docs"
	// ContextCategoryUserMemory is the user-level long-term memory
	// injection.
	ContextCategoryUserMemory = "user_memory"
	// ContextCategoryMemorySummary is the folded compaction summary the
	// windowed memory provider replays.
	ContextCategoryMemorySummary = "memory_summary"
	// ContextCategoryConversation is the conversation itself: the raw
	// window in bounded mode, the replayed transcript in full-history
	// mode, or both plus the turn's own request text. Its estimate
	// describes the prompt the turn opened with, which compaction can
	// rewrite before the call goes out, so the measurement settles this
	// row rather than trusting the estimate (see Normalize).
	ContextCategoryConversation = "conversation"
	// ContextCategoryTurnContext is the per-turn tail block: the active
	// plan, the skills list and the skills the model activated.
	ContextCategoryTurnContext = "turn_context"
	// ContextCategoryTools is the residual: everything the harness
	// cannot see while it assembles the prompt — the tool JSON schemas,
	// provider-side framing, and the error of every estimate above.
	// It is derived (see Normalize), never estimated on its own, which
	// is why it is the one category whose number can be far bigger than
	// the sum of the rest.
	ContextCategoryTools = "tools"
)

// ContextCategories is the canonical display order of the ids above. The
// meter walks it, so a category the UI does not draw never shows up in
// the middle of the stack.
var ContextCategories = []string{
	ContextCategoryInstructions,
	ContextCategoryEnvironment,
	ContextCategoryProjectDocs,
	ContextCategoryUserMemory,
	ContextCategoryMemorySummary,
	ContextCategoryConversation,
	ContextCategoryTurnContext,
	ContextCategoryTools,
}

// ContextCategory is one slice of the prompt.
//
// Tokens is an estimate for the rows the harness rendered, and comes out
// of the measurement for the two that cannot be estimated: the
// conversation row is what the provider's count left after those rows,
// and the tools row is what the same subtraction left after that (see
// Normalize). The total they add up to is the measurement either way, so
// the bar always fills exactly as far as the provider said the prompt
// reached.
type ContextCategory struct {
	ID     string `json:"id"`
	Tokens int    `json:"tokens"`
}

// ContextUsage is the last measurement of one conversation's prompt.
//
// MeasuredTokens is the provider's own inclusive input count (see
// PromptTokens); zero means the turn produced no usage report, in which
// case the categories are estimates only and Total is their sum. Model is
// the model that served the measured call, kept because the window it
// implies is the one the number must be read against.
//
// ConversationCapped marks a snapshot whose conversation estimate ran
// past what the measurement left for it (see Normalize): the turn that
// folds the conversation is the common case, because prepare estimated
// the transcript it opened with and the call carried a fraction of it.
// The total is still the provider's count, and every other row is its own
// estimate; what the flag qualifies is the conversation row, which is
// then a remainder rather than a size. The UI says so instead of drawing
// that row as an account of what the conversation cost.
type ContextUsage struct {
	MeasuredTokens     int64             `json:"measured_tokens"`
	Categories         []ContextCategory `json:"categories"`
	ConversationCapped bool              `json:"conversation_capped,omitempty"`
	Model              string            `json:"model,omitempty"`
	At                 time.Time         `json:"at"`
}

// Token returns one category's size, zero when it is absent.
func (u ContextUsage) Token(id string) int {
	for _, c := range u.Categories {
		if c.ID == id {
			return c.Tokens
		}
	}
	return 0
}

// TotalTokens is what the meter shows as used: the measurement when there
// is one, the sum of the estimates otherwise. Normalize keeps the two in
// step by construction, so this is the sum either way.
func (u ContextUsage) TotalTokens() int {
	total := 0
	for _, c := range u.Categories {
		total += c.Tokens
	}
	return total
}

// Normalize reconciles the estimates with the measurement, puts the list in
// canonical order and drops the categories that hold nothing.
//
// Two rows do not come out of an estimate. The residual is the whole reason
// the meter can be honest about what it does not know: tool schemas never
// reach a board var, provider framing is invisible, and every estimate
// above is a character heuristic. What the provider counted is exact, so
// the leftover goes in one bucket rather than being spread over slices
// that would then read as fact.
//
// The conversation row is the other one, and the reason it is derived is
// the fold. Prepare estimates the transcript the turn opens with, then
// compaction rewrites that prefix and the call carries a summary plus a
// recent window — routinely an order of magnitude less. Shrinking every
// slice proportionally to fit, which is what this did first, therefore cut
// the harness rows too: a 2.4k instruction layer that was 22% of the
// prompt was drawn as six tokens, because a multi-million-token transcript
// estimate had to be absorbed somewhere. So the rows the harness rendered
// keep their own numbers, the conversation gets what the measurement left
// after them, and the leftover after that is the residual. Nothing is
// inflated to fill the total and nothing is shrunk to hide it.
//
// Only a harness estimate that alone claims the whole prompt still scales
// the lot, conversation included: there the miss is the harness's own, a
// short turn over a large instruction layer makes it a few percent wide,
// and proportions are the least wrong thing to draw.
func (u *ContextUsage) Normalize() {
	values := make(map[string]int, len(u.Categories))
	conversation := 0
	attributed := 0
	for _, c := range u.Categories {
		switch c.ID {
		case ContextCategoryTools:
			// Derived, never estimated: recomputed below from whatever
			// the measurement leaves, so a stored one is dropped instead
			// of being treated as an estimate.
			continue
		case ContextCategoryConversation:
			conversation += c.Tokens
			continue
		}
		values[c.ID] += c.Tokens
		attributed += c.Tokens
	}
	measured := int(u.MeasuredTokens)
	residual := 0
	switch {
	case measured <= 0:
		// Nothing was measured, so there is nothing to reconcile the
		// estimates against and nothing left over to bucket: the sum is
		// the total.
		if conversation > 0 {
			values[ContextCategoryConversation] = conversation
		}
	case attributed >= measured:
		// The rows the harness rendered alone claim the whole prompt, so
		// they are the ones that are wrong. Scale them, conversation
		// included: this is the short turn over a large instruction
		// layer, where every estimate is within a few percent of the
		// measurement and their proportions still describe it.
		if conversation > 0 {
			values[ContextCategoryConversation] = conversation
		}
		scaleTo(values, measured)
	default:
		room := measured - attributed
		if conversation >= room {
			// The estimate described a prompt that was rewritten before
			// it was sent. The conversation gets what the measurement
			// left for it, which is what it actually cost, and the rows
			// above it keep their own numbers.
			u.ConversationCapped = true
			if room > 0 {
				values[ContextCategoryConversation] = room
			}
		} else {
			values[ContextCategoryConversation] = conversation
			residual = room - conversation
		}
	}
	if residual > 0 {
		values[ContextCategoryTools] = residual
	}
	u.Categories = renderCategories(values)
}

// renderCategories emits the categories in ContextCategories order, then
// any id this build does not know. The extras are kept rather than
// dropped: ContextCategories is append-only, so one can only arrive from a
// newer build's document, and dropping it would leave the slices summing
// short of the measurement the bar is drawn against. The UI is the side
// that drops the row it has no label for.
func renderCategories(values map[string]int) []ContextCategory {
	out := make([]ContextCategory, 0, len(values))
	for _, id := range ContextCategories {
		if tokens := values[id]; tokens > 0 {
			out = append(out, ContextCategory{ID: id, Tokens: tokens})
			delete(values, id)
		}
	}
	extra := make([]string, 0, len(values))
	for id, tokens := range values {
		if tokens > 0 {
			extra = append(extra, id)
		}
	}
	sort.Strings(extra)
	for _, id := range extra {
		out = append(out, ContextCategory{ID: id, Tokens: values[id]})
	}
	return out
}

// scaleTo shrinks values so they sum to total, keeping each category's
// share of what the estimates described.
//
// The rounding is largest-remainder, the one rule that keeps the sum exact
// and hands the tokens lost to flooring to the categories whose fractional
// parts came closest to a whole. Ties break by id: map iteration is random,
// and a breakdown that reshuffles between two readings of the same
// snapshot is a bug the user can see.
func scaleTo(values map[string]int, total int) {
	sum := 0
	for _, tokens := range values {
		sum += tokens
	}
	if sum <= 0 || total <= 0 || sum <= total {
		return
	}
	type remainder struct {
		id   string
		frac int64
	}
	floors := make(map[string]int, len(values))
	rest := make([]remainder, 0, len(values))
	assigned := 0
	for id, tokens := range values {
		exact := int64(tokens) * int64(total)
		floors[id] = int(exact / int64(sum))
		assigned += floors[id]
		rest = append(rest, remainder{id: id, frac: exact % int64(sum)})
	}
	sort.Slice(rest, func(i, j int) bool {
		if rest[i].frac != rest[j].frac {
			return rest[i].frac > rest[j].frac
		}
		return rest[i].id < rest[j].id
	})
	for i := 0; assigned < total; i++ {
		floors[rest[i%len(rest)].id]++
		assigned++
	}
	for _, r := range rest {
		values[r.id] = floors[r.id]
	}
}

// Valid reports whether the snapshot says anything.
func (u ContextUsage) Valid() bool { return u.TotalTokens() > 0 }

// WriteContextUsage persists one conversation's context snapshot. A
// snapshot that says nothing is dropped instead of stored: the previous
// turn's measurement is more useful than a zero row, and a turn that
// never reached the model has nothing to report.
func (s *Store) WriteContextUsage(id string, usage ContextUsage) error {
	if s == nil || !usage.Valid() {
		return nil
	}
	return s.WriteState(id, DocumentContextUsage, usage)
}

// ReadContextUsage loads the conversation's last context snapshot. A
// missing document returns os.ErrNotExist from ReadState, which callers
// treat as "no measurement yet"; a document that cannot be decoded is
// reported (ReadStateStrict), because
// the meter silently falling back to "nothing measured" is exactly the
// kind of quiet feature death that rule exists for.
//
// The snapshot is normalized on the way out. A document written before the
// conversation row was derived (an estimate that ran past its own
// measurement, which a folding turn produces) would otherwise draw a
// prompt larger than the window that served it for as long as nobody ran
// another turn on that conversation. Normalize is idempotent on a
// well-formed document, so this costs nothing on the common path and
// repairs the ones already stored.
func (s *Store) ReadContextUsage(id string) (ContextUsage, error) {
	var usage ContextUsage
	if s == nil {
		return ContextUsage{}, nil
	}
	if err := s.ReadStateStrict(id, DocumentContextUsage, &usage); err != nil {
		return ContextUsage{}, err
	}
	usage.Normalize()
	return usage, nil
}
