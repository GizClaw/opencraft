package engine

import (
	"sort"
	"testing"

	"github.com/GizClaw/opencraft/internal/capabilities/apps"
)

// TestEveryKnownKindIsClassifiedForApplications is the app platform's
// "new factory" alarm: the policy table in capabilities/apps has to
// decide about every kind this build registers, in either direction. A
// kind nobody decided about is a resource an application layer could
// declare — and therefore an execution surface that reached the app
// platform by adding a factory, not by a decision.
//
// The check is exact: it fails for a kind that is missing, and the
// table's own test fails when a row names a kind that does not exist.
func TestEveryKnownKindIsClassifiedForApplications(t *testing.T) {
	kinds := KnownKinds()
	if len(kinds) == 0 {
		t.Fatal("KnownKinds returned no kinds")
	}
	for _, kind := range kinds {
		if _, ok := apps.Classify(string(kind)); !ok {
			t.Errorf("resource kind %q has no row in the application allow/deny table "+
				"(internal/capabilities/apps): decide whether an application layer may declare it", kind)
		}
	}
}

// TestKnownKindsIsSortedAndDeduplicated pins the enumeration contract
// the table is read against: one entry per kind (not per impl),
// deterministic order, and the kinds this build ships rather than just
// opencraft's own.
func TestKnownKindsIsSortedAndDeduplicated(t *testing.T) {
	kinds := KnownKinds()
	if !sort.SliceIsSorted(kinds, func(i, j int) bool { return kinds[i] < kinds[j] }) {
		t.Errorf("KnownKinds is not sorted: %v", kinds)
	}
	seen := make(map[string]bool, len(kinds))
	for _, kind := range kinds {
		if seen[string(kind)] {
			t.Errorf("KnownKinds lists %q twice", kind)
		}
		seen[string(kind)] = true
	}
	for _, kind := range []string{"memory", "sandbox.Runner", "session.Store", "tool.Source"} {
		if !seen[kind] {
			t.Errorf("KnownKinds is missing %q", kind)
		}
	}
}
