package resourcekind

import (
	"strings"
	"testing"
)

func TestRuleAcceptsTheTwoDocumentedStyles(t *testing.T) {
	cases := []struct {
		value string
		style string
	}{
		// This repo's own style: the prefix plus lower_snake segments.
		{"opencraft.skills", "owned"},
		{"opencraft.user_memory", "owned"},
		{"opencraft.hooks.observer", "owned"},
		{"opencraft.delegation.policy", "owned"},
		{"opencraft.hostworkspace", "owned"},
		// Flowcraft's <domain>.<Role>, both role shapes in use.
		{"session.Store", "flowcraft"},
		{"tool.Source", "flowcraft"},
		{"agent.ScriptRuntime", "flowcraft"},
		{"memory.UsageObserver", "flowcraft"},
		{"hook.prepare", "flowcraft"},
		{"hook.commit", "flowcraft"},
		{"sandbox.Runner", "flowcraft"},
		// Neither style: the bare names, a missing prefix, capitals in an
		// owned segment, and the shapes a hurried contribution reaches for.
		{"memory", ""},
		{"skills", ""},
		{"opencraft.Skills", ""},
		{"opencraft.", ""},
		{"opencraft..skills", ""},
		{"opencraft.new kind", ""},
		{"Opencraft.skills", ""},
		{"opencraft_x.skills", ""},
		{"session.store", ""},
		{"session.Store.Extra", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := Style(tc.value); got != tc.style {
			t.Errorf("Style(%q) = %q, want %q", tc.value, got, tc.style)
		}
		if got := Matches(tc.value); got != (tc.style != "") {
			t.Errorf("Matches(%q) = %v, want %v", tc.value, got, tc.style != "")
		}
	}
}

// TestInventoryEntriesAreDocumented keeps the list reviewable: an entry
// without an owner or a note is a line nobody can judge, and a legacy
// entry has to say out loud that it predates the rule.
func TestInventoryEntriesAreDocumented(t *testing.T) {
	seen := make(map[string]bool, len(Kinds))
	for _, kind := range Kinds {
		if kind.Value == "" {
			t.Error("inventory entry without a value")
			continue
		}
		if seen[kind.Value] {
			t.Errorf("kind %q is listed twice", kind.Value)
		}
		seen[kind.Value] = true
		if kind.Owner == "" {
			t.Errorf("kind %q has no owner package", kind.Value)
		}
		if len(strings.Fields(kind.Note)) < 5 {
			t.Errorf("kind %q has no useful note", kind.Value)
		}
		if kind.Legacy == Matches(kind.Value) {
			t.Errorf("kind %q: legacy=%v contradicts Style=%q",
				kind.Value, kind.Legacy, Style(kind.Value))
		}
	}
	for _, impl := range Impls {
		if impl == "" {
			t.Error("inventory entry without an impl value")
		}
	}
}

// TestInventoryEntriesAreSorted makes the order a rule instead of a
// habit: the scans compare sets, so an entry appended out of order
// would pass them, while Kinds' doc asks for alphabetical order by
// Value — an entry then lands where a reviewer looks for it, and a
// duplicate is a copy that will drift.
func TestInventoryEntriesAreSorted(t *testing.T) {
	for i := 1; i < len(Kinds); i++ {
		if Kinds[i-1].Value >= Kinds[i].Value {
			t.Errorf("Kinds is not sorted by Value: %q before %q",
				Kinds[i-1].Value, Kinds[i].Value)
		}
	}
	for i := 1; i < len(Impls); i++ {
		if Impls[i-1] >= Impls[i] {
			t.Errorf("Impls is not sorted: %q before %q",
				Impls[i-1], Impls[i])
		}
	}
}

// TestLegacySpellingsStayWithinBudget is the gate the rule cannot be:
// the spellings that follow neither style are counted, so a new one has
// to be argued for (raise the budget and say why) instead of quietly
// joining `memory`.
func TestLegacySpellingsStayWithinBudget(t *testing.T) {
	var legacy []string
	for _, kind := range Kinds {
		if kind.Legacy {
			legacy = append(legacy, kind.Value)
		}
	}
	if len(legacy) > legacyBudget {
		t.Fatalf("legacy kinds = %v, budget = %d: a new kind has to "+
			"follow OwnedPattern or FlowcraftPattern; raising "+
			"legacyBudget is a deliberate exception, not a rename",
			legacy, legacyBudget)
	}
}
