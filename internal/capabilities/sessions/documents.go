package sessions

import "github.com/GizClaw/opencraft/internal/capabilities/sessions/state"

// conversation_state is the session store's per-conversation key/value
// table, and every document in it belongs to exactly one owner. The
// names live in this one file, for the same reason
// docs/session-data-model.md §2 lists them in one table: a reader must
// be able to see every document, who writes it, and which shape
// generation this build reads, without grepping five packages.
//
// The names are wire values — they are the keys already in the database
// — so a rename is a migration, not a refactor.
const (
	// DocumentTitle is the name a conversation displays with. It
	// overlays the conversations.title fallback column (the first
	// user message); losing the document loses a rename, nothing else.
	// Written by orchestration/host (auto title) and the desktop
	// rename binding.
	DocumentTitle = "title"
	// DocumentSettings is the session's own settings: reasoning
	// effort, model hint and sandbox mode. Owned by
	// state/settings.go; it replaced the session_settings table in
	// migration 019.
	DocumentSettings = state.SessionSettingsName
	// DocumentPlans is the per-agent plan snapshot (tools/plan).
	DocumentPlans = "plans"
	// DocumentSkillActivations is the per-agent list of skills the
	// model asked to activate (worldstate/activate.go).
	DocumentSkillActivations = "skill_activations"
	// DocumentUsageAnchor is the last provider-measured prompt size
	// (anchor.go), the base of the next turn's compaction estimate.
	DocumentUsageAnchor = "usage_anchor"
	// DocumentCompact is the most recent compaction artifact
	// (tools/compact): the summary and the messages it already covers.
	DocumentCompact = "compact"
)

// StateDocument is one conversation_state document: the key it is
// stored under, the package whose shape it holds, and the shape
// generation this build reads.
//
// Generation is what a document's owner bumps when the stored shape
// changes in a way an older reader would decode wrongly. Every document
// is generation 1 today (settings is generation 1 of the post-019
// shape, not of the session_settings table it replaced); the envelope
// that would carry the number in the document itself is not built yet,
// so the field is the registry's note to whoever builds it. It lives
// here rather than in each owner because a migration needs one list to
// walk.
type StateDocument struct {
	Name       string
	Owner      string
	Generation int
}

// Documents lists every document, in the order
// docs/session-data-model.md §2 does. Readers and writers use the
// constants above; this is the registry tests and future migrations
// walk, and TestDocumentsRegistryIsComplete keeps it in step with the
// constants.
var Documents = []StateDocument{
	{
		Name:       DocumentTitle,
		Owner:      "orchestration/host (auto title) + adapters/desktop (rename)",
		Generation: 1,
	},
	{
		Name:       DocumentSettings,
		Owner:      "capabilities/sessions/state (settings.go)",
		Generation: 1,
	},
	{
		Name:       DocumentPlans,
		Owner:      "capabilities/tools/plan",
		Generation: 1,
	},
	{
		Name:       DocumentSkillActivations,
		Owner:      "capabilities/worldstate (activate.go)",
		Generation: 1,
	},
	{
		Name:       DocumentUsageAnchor,
		Owner:      "capabilities/sessions (anchor.go)",
		Generation: 1,
	},
	{
		Name:       DocumentCompact,
		Owner:      "capabilities/tools/compact",
		Generation: 1,
	},
}
