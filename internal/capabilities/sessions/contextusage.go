package sessions

import "time"

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
	// mode, or both plus the turn's own request text.
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
// Tokens is an estimate for every category but ContextCategoryTools,
// which is the measured total minus the estimates (see Normalize). The
// total they add up to is the measurement, so the bar always fills
// exactly as far as the provider said the prompt reached.
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
type ContextUsage struct {
	MeasuredTokens int64             `json:"measured_tokens"`
	Categories     []ContextCategory `json:"categories"`
	Model          string            `json:"model,omitempty"`
	At             time.Time         `json:"at"`
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

// EstimatedTokens is the part of the total the harness estimated by
// itself: everything but the residual category.
func (u ContextUsage) EstimatedTokens() int {
	total := 0
	for _, c := range u.Categories {
		if c.ID == ContextCategoryTools {
			continue
		}
		total += c.Tokens
	}
	return total
}

// Normalize recomputes the residual category and puts the list in
// canonical order, dropping the categories that hold nothing.
//
// The residual is the whole reason the meter can be honest about what it
// does not know: tool schemas never reach a board var, provider framing
// is invisible, and every estimate above is a character heuristic. What
// the provider counted is exact, so the leftover goes in one bucket
// rather than being spread over slices that would then read as fact. The
// value is clamped at zero: an estimate that overshoots the measurement
// (a duplicate-heavy prompt, a provider that counts differently) must
// not publish a negative slice.
func (u *ContextUsage) Normalize() {
	estimated := u.EstimatedTokens()
	measured := int(u.MeasuredTokens)
	residual := 0
	if measured > 0 {
		residual = measured - estimated
		if residual < 0 {
			residual = 0
		}
	}
	values := make(map[string]int, len(u.Categories)+1)
	for _, c := range u.Categories {
		if c.ID == ContextCategoryTools {
			continue
		}
		values[c.ID] += c.Tokens
	}
	if residual > 0 {
		values[ContextCategoryTools] = residual
	}
	out := make([]ContextCategory, 0, len(values))
	for _, id := range ContextCategories {
		if tokens := values[id]; tokens > 0 {
			out = append(out, ContextCategory{ID: id, Tokens: tokens})
		}
	}
	u.Categories = out
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
func (s *Store) ReadContextUsage(id string) (ContextUsage, error) {
	var usage ContextUsage
	if s == nil {
		return ContextUsage{}, nil
	}
	if err := s.ReadStateStrict(id, DocumentContextUsage, &usage); err != nil {
		return ContextUsage{}, err
	}
	return usage, nil
}
