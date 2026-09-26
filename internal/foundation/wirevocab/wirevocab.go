// Package wirevocab is the frozen list of wire field names this repo
// retired, plus the scan that keeps the old spellings out of the tree.
//
// A JSON field name is a cross-version contract: an older UI build, a
// plugin compiled against last year's SDK, or a bundle a user exported
// months ago reads it. W5.3 of docs/architecture-plan.md renamed the
// conversation id from `session_id` to `conversation_id` — four
// hand-written DTOs and the frontend that reads them, in one step. The
// build notices nothing when a new `json:"session_id"` shows up beside
// the new name, which is what this package is for: Retired says which
// names are gone and what replaced them, and scan_test.go fails when one
// comes back.
//
// Scope is hand-written wire structs. Three things stay out of it, said
// here rather than hidden at their sites:
//
//   - generated protobuf (`internal/capabilities/execd/execd.pb.go`):
//     the execd channel is process-to-process plumbing, not UI or plugin
//     wire, and the file is regenerated rather than edited;
//   - the `session_id` SQL column of `model_usage` (owned by
//     `foundation/compat`'s migrations): a column is not a field name,
//     and renaming it is a migration, not a rename;
//   - the `session_id` argument `capabilities/tools/websearch` sends to
//     Parallel's API: that is their vocabulary, not ours.
//
// The names are recorded, not owned: nothing here may change without
// checking the writers and readers of the wire it names.
package wirevocab

// Name is one retired field name and the spelling that replaced it.
type Name struct {
	// Old is the field name no hand-written struct tag may use again.
	Old string
	// New is the field name that carries the value today.
	New string
	// Owner is the surface whose wire used Old and owns the rename; a
	// reader with a question starts there.
	Owner string
	// Why records what the rename bought, so an entry can be judged
	// rather than trusted.
	Why string
}

// Retired is the whole list. Adding an entry means a name went away in
// a release other builds may still be reading — the review surface is
// this table, not a comment somewhere else.
var Retired = []Name{
	{
		Old:   "session_id",
		New:   "conversation_id",
		Owner: "internal/adapters/desktop/bindings",
		Why: "the desktop shell and the plugin SDK each called the " +
			"conversation id a session id while the store, the graph " +
			"and the archive called it a conversation; the wire now " +
			"agrees with them (W5.3: `bindings/conversation.go`'s " +
			"NewChatResult, `bindings/session.go`'s " +
			"SessionDeleteResult/SessionImportDTO, and " +
			"`capabilities/plugins/runtime`'s SessionImportResult).",
	},
}
