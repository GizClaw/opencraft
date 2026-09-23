package bindings

import (
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// delegationBinding builds the delegation service over a private temp
// root. The work directory is empty on purpose: no runtime is
// assembled, which is the state the card has to survive.
func delegationBinding(t *testing.T) *Delegation {
	t.Helper()
	dir := t.TempDir()
	return NewDelegationBinding(core.NewCore(dir, dir, ""))
}

func TestDelegationStateDefaultsAndBounds(t *testing.T) {
	b := delegationBinding(t)

	state, err := b.DelegationState()
	if err != nil {
		t.Fatal(err)
	}
	if state.MaxConcurrency != 4 || state.MaxDepth != 8 {
		t.Fatalf("state limits = %+v, want the shipped defaults", state)
	}
	if len(state.AllowedTargets) != 0 || len(state.BlockedTargets) != 0 {
		t.Fatalf("state lists = %+v, want none", state)
	}
	// The lists are non-nil so the card can render empty rows without a
	// JSON null check.
	if state.AllowedTargets == nil || state.BlockedTargets == nil {
		t.Fatal("the lists must be empty slices, not nil")
	}
	if state.MinMaxConcurrency != 1 ||
		state.MaxMaxConcurrency != config.DelegationMaxConcurrencyCeiling ||
		state.DefaultMaxConcurrency != 4 ||
		state.MinMaxDepth != 1 ||
		state.MaxMaxDepth != config.DelegationMaxDepthCeiling ||
		state.DefaultMaxDepth != 8 ||
		state.MaxTargets != config.MaxDelegationTargets {
		t.Fatalf("bounds = %+v", state)
	}
	// No runtime is assembled, so there is no target list — and that is
	// reported rather than guessed.
	if state.TargetsAvailable {
		t.Fatal("targets reported available without a runtime")
	}
	if len(state.Targets) != 0 {
		t.Fatalf("targets = %+v, want none", state.Targets)
	}
}

func TestDelegationSaveRoundsThroughState(t *testing.T) {
	b := delegationBinding(t)

	if err := b.SaveDelegationSettings(DelegationSettingsRequest{
		MaxConcurrency: 8,
		MaxDepth:       4,
		AllowedTargets: []string{" researcher ", "researcher", "writer*"},
		BlockedTargets: []string{"danger*"},
	}); err != nil {
		t.Fatal(err)
	}
	state, err := b.DelegationState()
	if err != nil {
		t.Fatal(err)
	}
	if state.MaxConcurrency != 8 || state.MaxDepth != 4 {
		t.Fatalf("saved limits = %+v", state)
	}
	if got := len(state.AllowedTargets); got != 2 ||
		state.AllowedTargets[0] != "researcher" ||
		state.AllowedTargets[1] != "writer*" {
		t.Fatalf("saved allowed = %+v, want the trimmed de-duped list",
			state.AllowedTargets)
	}
	if len(state.BlockedTargets) != 1 || state.BlockedTargets[0] != "danger*" {
		t.Fatalf("saved blocked = %+v", state.BlockedTargets)
	}

	// A vacuous save clears the lists; empty means "no restriction",
	// not "keep the previous one".
	if err := b.SaveDelegationSettings(DelegationSettingsRequest{
		MaxConcurrency: 8,
		MaxDepth:       4,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = b.DelegationState()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.AllowedTargets) != 0 || len(state.BlockedTargets) != 0 {
		t.Fatalf("cleared lists = %+v", state)
	}

	// An out-of-range save is refused and leaves the stored layer alone.
	if err := b.SaveDelegationSettings(DelegationSettingsRequest{
		MaxConcurrency: config.DelegationMaxConcurrencyCeiling + 1,
	}); err == nil {
		t.Fatal("SaveDelegationSettings accepted an out-of-range limit")
	}
	state, err = b.DelegationState()
	if err != nil {
		t.Fatal(err)
	}
	if state.MaxConcurrency != 8 || state.MaxDepth != 4 {
		t.Fatalf("a refused save changed the settings: %+v", state)
	}
}
