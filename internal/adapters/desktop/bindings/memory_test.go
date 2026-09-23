package bindings

import (
	"context"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/capabilities/memory/userstore"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// memoryBinding builds a Config over a private temp root. An empty
// workDir is the state the settings page is first reached in, before
// any workspace is selected.
func memoryBinding(t *testing.T, workDir string) *Config {
	t.Helper()
	dir := t.TempDir()
	return NewConfig(core.NewCore(dir, dir, workDir))
}

// openUserDB attaches the user databases the memory bindings read.
func openUserDB(t *testing.T, b *Config) {
	t.Helper()
	openUserDBOn(t, b.core)
}

// openUserDBOn attaches the user databases one core's runtime reads.
func openUserDBOn(t *testing.T, c *core.Core) {
	t.Helper()
	if err := c.Runtime.OpenUserDB(context.Background()); err != nil {
		t.Fatalf("open user db: %v", err)
	}
}

// wantErrorContaining fails unless err carries the expected fragment.
func wantErrorContaining(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q, got nil", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want it to contain %q", err.Error(), want)
	}
}

func TestUserMemoryStateWithoutUserDB(t *testing.T) {
	b := memoryBinding(t, "")
	state, err := b.UserMemoryState()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Enabled {
		t.Fatalf("enabled = %v, want the shipped default", state.Enabled)
	}
	if state.InjectMaxItems != config.UserMemoryDefaultInjectMaxItems ||
		state.InjectMaxChars != config.UserMemoryDefaultInjectMaxChars {
		t.Fatalf("budgets = %+v", state)
	}
	if state.MinInjectMaxItems != config.UserMemoryMinInjectMaxItems ||
		state.MaxInjectMaxItems != config.UserMemoryMaxInjectMaxItems ||
		state.MinInjectMaxChars != config.UserMemoryMinInjectMaxChars ||
		state.MaxInjectMaxChars != config.UserMemoryMaxInjectMaxChars {
		t.Fatalf("bounds = %+v", state)
	}
	if state.MaxItems != userstore.MaxItems ||
		state.MaxTextBytes != userstore.MaxTextBytes {
		t.Fatalf("store limits = %+v", state)
	}
	if state.Available {
		t.Fatal("available without a user database")
	}
	if state.Live != 0 || state.Stale != 0 {
		t.Fatalf("counts = %d/%d", state.Live, state.Stale)
	}
	facts, err := b.MemoryFacts()
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 0 {
		t.Fatalf("facts = %+v, want empty", facts)
	}
}

func TestMemoryWritesRefuseWithoutUserDB(t *testing.T) {
	b := memoryBinding(t, "")
	_, err := b.AddMemoryFact(MemoryFactRequest{
		Text:  "prefers tabs",
		Scope: userstore.ScopeGlobal,
	})
	wantErrorContaining(t, err, "no user database")
	_, err = b.UpdateMemoryFact("f-1", "prefers spaces")
	wantErrorContaining(t, err, "no user database")
	_, err = b.SetMemoryFactStale("f-1", true)
	wantErrorContaining(t, err, "no user database")
	wantErrorContaining(t, b.RemoveMemoryFact("f-1"), "no user database")
}

func TestMemoryFactsRoundTrip(t *testing.T) {
	b := memoryBinding(t, "")
	openUserDB(t, b)

	added, err := b.AddMemoryFact(MemoryFactRequest{
		Text:  "The user runs macOS",
		Scope: userstore.ScopeGlobal,
		Kind:  "environment",
	})
	if err != nil {
		t.Fatal(err)
	}
	if added.ID == "" || added.Scope != userstore.ScopeGlobal {
		t.Fatalf("added = %+v", added)
	}
	if added.CreatedAt == "" || added.UpdatedAt == "" {
		t.Fatalf("timestamps = %+v", added)
	}
	if added.Workspace != "" {
		t.Fatalf("global fact carries a workspace: %+v", added)
	}

	facts, err := b.MemoryFacts()
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].Text != "The user runs macOS" {
		t.Fatalf("facts = %+v", facts)
	}

	updated, err := b.UpdateMemoryFact(added.ID, "The user runs macOS 15")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Text != "The user runs macOS 15" || updated.ID != added.ID {
		t.Fatalf("updated = %+v", updated)
	}

	stale, err := b.SetMemoryFactStale(added.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !stale.Stale {
		t.Fatalf("stale = %+v", stale)
	}

	// Stale facts stay listed (marked) rather than disappearing, and the
	// counts the card shows separate them from live ones.
	facts, err = b.MemoryFacts()
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || !facts[0].Stale {
		t.Fatalf("facts = %+v", facts)
	}
	state, err := b.UserMemoryState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Live != 0 || state.Stale != 1 {
		t.Fatalf("counts = %d/%d", state.Live, state.Stale)
	}

	if err := b.RemoveMemoryFact(added.ID); err != nil {
		t.Fatal(err)
	}
	facts, err = b.MemoryFacts()
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 0 {
		t.Fatalf("facts after remove = %+v", facts)
	}
}

func TestMemoryFactScopeDefaultsToWorkspace(t *testing.T) {
	workspace := t.TempDir()
	b := memoryBinding(t, workspace)
	openUserDB(t, b)

	// No scope given: with a workspace open the narrower scope wins.
	fact, err := b.AddMemoryFact(MemoryFactRequest{Text: "The repo uses tabs"})
	if err != nil {
		t.Fatal(err)
	}
	if fact.Scope != userstore.ScopeWorkspace || fact.Workspace != workspace {
		t.Fatalf("fact = %+v", fact)
	}
	facts, err := b.MemoryFacts()
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Fatalf("facts = %+v", facts)
	}

	// An explicit scope wins over the fallback, and an unknown one is
	// refused instead of silently filed somewhere.
	global, err := b.AddMemoryFact(MemoryFactRequest{
		Text:  "Prefers short answers",
		Scope: userstore.ScopeGlobal,
	})
	if err != nil {
		t.Fatal(err)
	}
	if global.Scope != userstore.ScopeGlobal || global.Workspace != "" {
		t.Fatalf("global = %+v", global)
	}
	_, err = b.AddMemoryFact(MemoryFactRequest{
		Text:  "somewhere",
		Scope: "elsewhere",
	})
	wantErrorContaining(t, err, "unknown scope")
}

func TestMemoryFactScopeWithoutWorkspaceIsGlobal(t *testing.T) {
	b := memoryBinding(t, "")
	openUserDB(t, b)
	fact, err := b.AddMemoryFact(MemoryFactRequest{Text: "The user reads zh"})
	if err != nil {
		t.Fatal(err)
	}
	if fact.Scope != userstore.ScopeGlobal || fact.Workspace != "" {
		t.Fatalf("fact = %+v", fact)
	}
	// A workspace-scoped write with nothing to scope it to is refused.
	_, err = b.AddMemoryFact(MemoryFactRequest{
		Text:  "The repo uses tabs",
		Scope: userstore.ScopeWorkspace,
	})
	wantErrorContaining(t, err, "open workspace")
}

func TestSaveUserMemorySettingsRoundTrip(t *testing.T) {
	b := memoryBinding(t, "")
	if err := b.SaveUserMemorySettings(UserMemorySettingsRequest{
		Enabled:        false,
		InjectMaxItems: 7,
		InjectMaxChars: 4096,
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadUserMemory(b.core.UserDir)
	if err != nil {
		t.Fatal(err)
	}
	effective, err := loaded.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if effective.Enabled || effective.InjectMaxItems != 7 ||
		effective.InjectMaxChars != 4096 {
		t.Fatalf("effective = %+v", effective)
	}
	state, err := b.UserMemoryState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Enabled || state.InjectMaxItems != 7 || state.InjectMaxChars != 4096 {
		t.Fatalf("state = %+v", state)
	}

	// Out-of-range values are rejected by the config layer and leave the
	// stored settings alone.
	if err := b.SaveUserMemorySettings(UserMemorySettingsRequest{
		Enabled:        true,
		InjectMaxItems: config.UserMemoryMaxInjectMaxItems + 1,
		InjectMaxChars: 2048,
	}); err == nil {
		t.Fatal("an out-of-range budget must be refused")
	}
	state, err = b.UserMemoryState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Enabled || state.InjectMaxItems != 7 {
		t.Fatalf("failed save changed the stored settings: %+v", state)
	}
}
