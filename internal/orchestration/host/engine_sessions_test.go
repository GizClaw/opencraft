package host

import (
	"reflect"
	"testing"

	coresession "github.com/GizClaw/flowcraft/core/runtime/session"

	"github.com/GizClaw/opencraft/internal/capabilities/apps"
)

// sessionKeyStrings renders the engine session keys of a conversation as
// "agent/conversation" pairs, which is what makes a test about *which*
// sessions a deletion closes readable.
func sessionKeyStrings(keys []coresession.Key) []string {
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key.AgentID+"/"+key.ContextID)
	}
	return out
}

// TestHostEngineSessionKeysCoverEveryAgent pins which engine sessions
// one conversation occupies, because that list is what a deletion
// closes: a Host whose deletion closed only the entry's session would
// leave an application's other agents holding a history the user
// deleted. A workspace has the assistant and nothing else.
func TestHostEngineSessionKeysCoverEveryAgent(t *testing.T) {
	workspace := &Host{id: workspaceIdentity()}
	want := []string{"assistant/s-1"}
	if got := sessionKeyStrings(workspace.engineSessionKeys("s-1")); !reflect.DeepEqual(got, want) {
		t.Errorf("workspace keys = %v, want %v", got, want)
	}
	// A zero-value Host — a unit test's, an assembly that never ran —
	// reads as the workspace's rather than naming no agent at all.
	zero := &Host{}
	if got := sessionKeyStrings(zero.engineSessionKeys("s-1")); !reflect.DeepEqual(got, want) {
		t.Errorf("zero-value keys = %v, want %v", got, want)
	}

	app := &Host{id: appIdentity("narrator", []string{"judge", "coach"}, apps.Defaults{})}
	want = []string{"narrator/s-1", "judge/s-1", "coach/s-1"}
	if got := sessionKeyStrings(app.engineSessionKeys("s-1")); !reflect.DeepEqual(got, want) {
		t.Errorf("application keys = %v, want %v (the entry first, then the manifest order)", got, want)
	}
}
