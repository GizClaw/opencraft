package apps

import (
	"slices"
	"sort"
	"testing"
)

// TestOnlyTheAgentSurfaceIsAllowed pins the v1 policy in the direction
// review cares about: an application layer may declare the agent surface
// itself, and nothing that would build a second instance of what the
// contract layer already provides. Granting a layer a kind the host
// never gave an application is a policy change, and it has to show up
// here, not only in the row it edited — a denial that disappears quietly
// is the whole failure mode this table exists to prevent.
func TestOnlyTheContractLayersSurfaceIsAllowed(t *testing.T) {
	want := []string{
		"agent.Engine",
		"agent.ScriptRuntime",
	}
	got := make([]string, 0, len(want))
	for _, rule := range kindTable {
		if rule.allowed {
			got = append(got, rule.kind)
		}
	}
	sort.Strings(got)
	if !slices.Equal(got, want) {
		t.Errorf("allowed kinds = %v, want exactly %v — an application "+
			"layer declares agents, not infrastructure: every store, bus "+
			"and workspace the contract layer already provides is refused "+
			"as a kind too, so a layer cannot build a second history under "+
			"another key. A new allowance is a policy decision (the app "+
			"platform plan, §2.5), not a table edit", got, want)
	}
}

// TestClassifyCarriesAReasonExactlyWhenItDenies keeps the table honest
// in the two directions the caller relies on: a denial has the text the
// import wizard shows the user, and an allowance carries none.
func TestClassifyCarriesAReasonExactlyWhenItDenies(t *testing.T) {
	for _, rule := range kindTable {
		verdict, ok := Classify(rule.kind)
		if !ok {
			t.Errorf("kind %q is in the table but Classify does not find it", rule.kind)
			continue
		}
		if verdict.Allowed != rule.allowed {
			t.Errorf("kind %q: Classify = %v, want %v", rule.kind, verdict.Allowed, rule.allowed)
		}
		if verdict.Allowed == (verdict.Reason != "") {
			t.Errorf("kind %q: allowed=%v reason=%q — a denial needs a reason, an allowance must not carry one",
				rule.kind, verdict.Allowed, verdict.Reason)
		}
	}
	if len(kindTable) == 0 {
		t.Fatal("the kind table is empty")
	}
}

// TestClassifyRefusesKindsTheBuildDoesNotKnow pins the failure mode for
// a kind nobody registered: not allowed, not denied — unknown. The
// coverage test in the engine is what keeps the table total for this
// build; this is the answer for anything else.
func TestClassifyRefusesKindsTheBuildDoesNotKnow(t *testing.T) {
	for _, kind := range []string{"", "sandbox.Runner.extra", "opencraft.new_thing", "n"} {
		if verdict, ok := Classify(kind); ok {
			t.Errorf("Classify(%q) = %+v, ok — want unknown", kind, verdict)
		}
	}
}
