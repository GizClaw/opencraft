package worldstate

import "github.com/GizClaw/flowcraft/core/agent"

// Board var names carrying the OpenCraft client identifiers used by
// the default graph's inference request_metadata.
const (
	clientMetadataThreadVar = "oc_thread_id"
	clientMetadataTurnVar   = "oc_turn_id"
)

// seedClientMetadata stamps the current conversation and run on the
// board. The graph inference node copies them into
// GenerateRequest.RequestMetadata, and providers configured with the
// client_metadata envelope forward them verbatim:
//
//	"client_metadata": {
//	  "thread_id": "oc-<conversation id>",
//	  "turn_id":   "oc-turn-<run id>"
//	}
func seedClientMetadata(board *agent.Board, id agent.Identity) {
	thread := id.ConversationID
	if thread == "" {
		thread = "unknown"
	}
	run := id.RunID
	if run == "" {
		run = "unknown"
	}
	board.SetVar(clientMetadataThreadVar, "oc-"+thread)
	board.SetVar(clientMetadataTurnVar, "oc-turn-"+run)
}
