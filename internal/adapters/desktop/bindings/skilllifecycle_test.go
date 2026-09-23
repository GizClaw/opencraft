package bindings

import (
	"context"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	skillusage "github.com/GizClaw/opencraft/internal/capabilities/skills/usage"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// skillLifecycleBinding builds the lifecycle service over a private
// temp root. There is no assembled runtime behind it, which is exactly
// how the page behaves before the engine is up: usage comes from the
// user database and retirement is unavailable.
func skillLifecycleBinding(t *testing.T) *SkillLifecycle {
	t.Helper()
	dir := t.TempDir()
	return NewSkillLifecycleBinding(core.NewCore(dir, dir, ""))
}

// recordSkillUse writes one activation the way the skills service does.
func recordSkillUse(t *testing.T, b *SkillLifecycle, name string) {
	t.Helper()
	store := b.core.Runtime.Manager().SkillUsageStore()
	if store == nil {
		t.Fatal("skill usage store is not attached")
	}
	if err := store.Record(context.Background(), skillusage.Event{
		Name:           name,
		Scope:          "user",
		UsedAt:         time.Now().UTC(),
		RunID:          "run-1",
		ConversationID: "conv-1",
	}); err != nil {
		t.Fatal(err)
	}
}

// skillRow finds one row by name.
func skillRow(t *testing.T, state SkillLifecycleState, name string) SkillUsageView {
	t.Helper()
	for _, row := range state.Skills {
		if row.Name == name {
			return row
		}
	}
	t.Fatalf("skill %q is missing from %+v", name, state.Skills)
	return SkillUsageView{}
}

func TestSkillLifecycleStateWithoutUserDB(t *testing.T) {
	b := skillLifecycleBinding(t)
	state, err := b.SkillUsage()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Enabled {
		t.Fatal("usage recording ships enabled")
	}
	if state.StaleAfterDays != config.SkillLifecycleDefaultStaleAfterDays ||
		state.MinUses != config.SkillLifecycleDefaultMinUses ||
		state.UsageWindowDays != config.SkillLifecycleDefaultUsageWindowDays {
		t.Fatalf("settings = %+v", state)
	}
	if state.MinStaleAfterDays != config.SkillLifecycleMinStaleAfterDays ||
		state.MaxStaleAfterDays != config.SkillLifecycleMaxStaleAfterDays ||
		state.MinMinUses != config.SkillLifecycleMinMinUses ||
		state.MaxMinUses != config.SkillLifecycleMaxMinUses ||
		state.MinUsageWindowDays != config.SkillLifecycleMinUsageWindowDays ||
		state.MaxUsageWindowDays != config.SkillLifecycleMaxUsageWindowDays {
		t.Fatalf("bounds = %+v", state)
	}
	if state.UsageAvailable {
		t.Fatal("usage without a user database")
	}
	if state.CuratorAvailable {
		t.Fatal("curator without a runtime")
	}
	if len(state.Skills) != 0 || len(state.Archives) != 0 {
		t.Fatalf("state = %+v", state)
	}
	archives, err := b.SkillArchives()
	if err != nil {
		t.Fatal(err)
	}
	if len(archives) != 0 {
		t.Fatalf("archives = %+v", archives)
	}
}

func TestSkillUsageListsRecordedStats(t *testing.T) {
	b := skillLifecycleBinding(t)
	openUserDBOn(t, b.core)
	recordSkillUse(t, b, "plan")
	recordSkillUse(t, b, "plan")

	state, err := b.SkillUsage()
	if err != nil {
		t.Fatal(err)
	}
	if !state.UsageAvailable {
		t.Fatal("usage store is attached")
	}
	// No registry in this runtime, so the rows are the recorded names.
	// The page joins them with the registry it already lists.
	if len(state.Skills) != 1 {
		t.Fatalf("skills = %+v", state.Skills)
	}
	row := skillRow(t, state, "plan")
	if row.Uses != 2 {
		t.Fatalf("uses = %d", row.Uses)
	}
	if row.LastUsed == "" {
		t.Fatal("last_used is empty")
	}
	// Scope arrives with a stored decision (the statistics table does
	// not carry one), so a row that was only ever used has none yet.
	if row.Scope != "" || row.Pinned || row.Retired ||
		row.SuggestedRetire || row.Builtin {
		t.Fatalf("row = %+v", row)
	}
	if state.CuratorAvailable {
		t.Fatal("the curator needs the skills registry")
	}
}

func TestPinAndUnpinSkill(t *testing.T) {
	b := skillLifecycleBinding(t)
	openUserDBOn(t, b.core)
	recordSkillUse(t, b, "plan")

	if err := b.PinSkill("plan", "user"); err != nil {
		t.Fatal(err)
	}
	state, err := b.SkillUsage()
	if err != nil {
		t.Fatal(err)
	}
	row := skillRow(t, state, "plan")
	if !row.Pinned {
		t.Fatalf("state = %+v", state.Skills)
	}
	// The stored decision carries the scope the page pinned it under.
	if row.Scope != "user" {
		t.Fatalf("scope = %q", row.Scope)
	}
	if err := b.UnpinSkill("plan", "user"); err != nil {
		t.Fatal(err)
	}
	state, err = b.SkillUsage()
	if err != nil {
		t.Fatal(err)
	}
	if skillRow(t, state, "plan").Pinned {
		t.Fatalf("state = %+v", state.Skills)
	}
}

func TestPinSkillNeedsStoreAndName(t *testing.T) {
	b := skillLifecycleBinding(t)
	wantErrorContaining(t, b.PinSkill("plan", "user"), "no user database")
	openUserDBOn(t, b.core)
	wantErrorContaining(t, b.PinSkill("  ", "user"), "name is required")
}

func TestRetireNeedsTheCurator(t *testing.T) {
	b := skillLifecycleBinding(t)
	openUserDBOn(t, b.core)
	// The curator owns retirement: without the registry (and so without
	// the archive directory) there is nothing that may snapshot a skill.
	_, err := b.RetireSkill("plan", "user")
	wantErrorContaining(t, err, "curator is not available")
	_, err = b.RestoreSkill("plan-20260101T000000")
	wantErrorContaining(t, err, "curator is not available")
	// The curator guard comes first, so a nameless request cannot reach
	// a snapshot either way.
	_, err = b.RetireSkill("  ", "user")
	wantErrorContaining(t, err, "curator is not available")
}

func TestSaveSkillLifecycleSettingsRoundTrip(t *testing.T) {
	b := skillLifecycleBinding(t)
	if err := b.SaveSkillLifecycleSettings(SkillLifecycleSettingsRequest{
		Enabled:         false,
		StaleAfterDays:  30,
		MinUses:         2,
		UsageWindowDays: 60,
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadSkillLifecycle(b.core.UserDir)
	if err != nil {
		t.Fatal(err)
	}
	effective, err := loaded.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if effective.Enabled || effective.StaleAfterDays != 30 ||
		effective.MinUses != 2 || effective.UsageWindowDays != 60 {
		t.Fatalf("effective = %+v", effective)
	}
	state, err := b.SkillUsage()
	if err != nil {
		t.Fatal(err)
	}
	if state.Enabled || state.StaleAfterDays != 30 || state.MinUses != 2 ||
		state.UsageWindowDays != 60 {
		t.Fatalf("state = %+v", state)
	}

	if err := b.SaveSkillLifecycleSettings(SkillLifecycleSettingsRequest{
		Enabled:        true,
		StaleAfterDays: config.SkillLifecycleMaxStaleAfterDays + 1,
		MinUses:        2,
	}); err == nil {
		t.Fatal("an out-of-range threshold must be refused")
	}
	state, err = b.SkillUsage()
	if err != nil {
		t.Fatal(err)
	}
	if state.Enabled || state.StaleAfterDays != 30 {
		t.Fatalf("failed save changed the stored settings: %+v", state)
	}
}
