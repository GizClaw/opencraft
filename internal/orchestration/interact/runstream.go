package interact

import (
	"strings"

	"github.com/GizClaw/flowcraft/core/event"
)

// StreamRunID extracts the run id from a stream subject such as
// "agent.run.<runID>.stream.<actor>.delta". It returns "" when the
// subject is not a run stream, so both the host rollout recorder and
// UI stream routers can share one convention instead of re-parsing
// flowcraft subjects.
func StreamRunID(subject event.Subject) string {
	parts := strings.Split(string(subject), ".")
	if len(parts) >= 3 &&
		parts[1] == "run" &&
		(parts[0] == "agent" || parts[0] == "engine") {
		return parts[2]
	}
	return ""
}
