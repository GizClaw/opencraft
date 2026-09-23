package skills

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"

	skillusage "github.com/GizClaw/opencraft/internal/capabilities/skills/usage"
	"github.com/GizClaw/opencraft/internal/foundation/compat"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// newUsageStore opens a migrated user.db and attaches the skill usage
// store to it, the way both hosts do before the runtime assembles.
func newUsageStore(t *testing.T) *skillusage.Store {
	t.Helper()
	handle, err := db.Open(filepath.Join(t.TempDir(), "user.db"))
	if err != nil {
		t.Fatalf("open user db: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := compat.User(context.Background(), handle); err != nil {
		t.Fatalf("migrate user db: %v", err)
	}
	store, err := skillusage.Attach(handle)
	if err != nil {
		t.Fatalf("attach skill usage: %v", err)
	}
	return store
}

// curatorSettings is the shape a deployment resolves skilllifecycle to.
// The tests pass it explicitly so that a change to the shipped defaults
// cannot silently move the boundary they assert.
func curatorSettings() config.SkillLifecycleConfig {
	return config.SkillLifecycleConfig{
		Enabled:         true,
		StaleAfterDays:  45,
		MinUses:         3,
		UsageWindowDays: 90,
	}
}

func hasSkill(list []SkillMetadata, name string) bool {
	for _, sk := range list {
		if sk.Name == name {
			return true
		}
	}
	return false
}

// newLifecycleService discovers skills under an isolated HOME and wires
// the usage store, so the filters below run against a real user
// database instead of the empty lifecycle.
func newLifecycleService(t *testing.T) (*Service, *skillusage.Store) {
	t.Helper()
	userRoot(t)
	store := newUsageStore(t)
	svc := NewService(context.Background(), Options{Enabled: true})
	svc.SetLifecycle(store, curatorSettings(), filepath.Join(t.TempDir(), "archive"))
	return svc, store
}

// TestServiceRecordUsageWithoutUserDatabaseIsSilent pins the contract
// every write point relies on: a runtime without user.db records
// nothing, fails nothing and leaves the curator off.
func TestServiceRecordUsageWithoutUserDatabaseIsSilent(t *testing.T) {
	userRoot(t)
	ctx := context.Background()
	svc := NewService(ctx, Options{Enabled: true})
	if svc.Lifecycle() != nil || svc.Curator() != nil {
		t.Fatalf("bare service wired a lifecycle: %v / %v",
			svc.Lifecycle(), svc.Curator())
	}
	svc.RecordUsage(ctx, skillusage.Event{Name: "review", Scope: "builtin"})
	svc.RecordUsage(ctx, skillusage.Event{})

	// The empty lifecycle (a runtime whose user.db could not be opened)
	// collapses to the same stateless service.
	svc.SetLifecycle(skillusage.EmptyLifecycle(), curatorSettings(), t.TempDir())
	if svc.Lifecycle() != nil || svc.Curator() != nil {
		t.Fatalf("empty lifecycle wired a store: %v / %v",
			svc.Lifecycle(), svc.Curator())
	}
	svc.RecordUsage(ctx, skillusage.Event{Name: "review"})
	// A nil curator is safe to call: it reports that this runtime cannot
	// retire anything rather than dereferencing itself.
	if _, err := svc.Curator().Retire(ctx, "review"); !errdefs.IsNotAvailable(err) {
		t.Fatalf("Retire without user.db = %v, want not available", err)
	}
}

func TestServiceRecordUsageReachesTheStore(t *testing.T) {
	ctx := context.Background()
	svc, store := newLifecycleService(t)
	if svc.Lifecycle() == nil {
		t.Fatal("lifecycle not wired")
	}
	svc.RecordUsage(ctx, skillusage.Event{
		Name: "review", Scope: "user",
		RunID: "run-1", ConversationID: "s-1",
	})
	// A nameless event is a no-op, not an error: the write points stay
	// best-effort whatever they are handed.
	svc.RecordUsage(ctx, skillusage.Event{})

	stats, err := store.Stats(ctx, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].Name != "review" || stats[0].Uses != 1 {
		t.Fatalf("stats = %+v, want review x1", stats)
	}
	if stats[0].LastUsed.IsZero() {
		t.Fatal("RecordUsage did not stamp a time")
	}
}

// TestServiceRecordUsageHonoursTheDisabledSwitch pins the settings side
// of the write path: the config documents the switch as "Enabled turns
// usage recording on", so a deployment that switched it off records
// nothing and still leaves the store wired for the skills page.
func TestServiceRecordUsageHonoursTheDisabledSwitch(t *testing.T) {
	ctx := context.Background()
	userRoot(t)
	store := newUsageStore(t)
	svc := NewService(ctx, Options{Enabled: true})
	svc.SetLifecycle(store, config.SkillLifecycleConfig{
		Enabled:         false,
		StaleAfterDays:  45,
		MinUses:         3,
		UsageWindowDays: 90,
	}, filepath.Join(t.TempDir(), "archive"))
	if svc.Lifecycle() == nil {
		t.Fatal("a disabled lifecycle un-wired the store")
	}
	svc.RecordUsage(ctx, skillusage.Event{Name: "review", Scope: "user"})

	stats, err := store.Stats(ctx, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 0 {
		t.Fatalf("a disabled lifecycle recorded usage: %+v", stats)
	}
}

// TestAvailableHidesRetiredSkillsButKeepsThemListed pins the split the
// lifecycle needs: retiring stops the registry from offering a skill
// (per-turn list, $mention, ranking) while the skills page keeps seeing
// it, because that is how it is restored.
func TestAvailableHidesRetiredSkillsButKeepsThemListed(t *testing.T) {
	_, root := userRoot(t)
	writeSkill(t, root, "doomed", "name: doomed\ndescription: stale skill\n")
	store := newUsageStore(t)
	ctx := context.Background()
	// The skills page writes the decision straight to the store, so the
	// service has to pick it up from there.
	if _, err := store.SetRetired(ctx, "doomed", "user", true); err != nil {
		t.Fatal(err)
	}
	svc := NewService(ctx, Options{Enabled: true})
	svc.SetLifecycle(store, curatorSettings(), filepath.Join(t.TempDir(), "archive"))

	if !hasSkill(svc.List(), "doomed") {
		t.Fatal("List must keep retired skills: the page restores them")
	}
	if hasSkill(svc.Available(), "doomed") {
		t.Fatal("Available must not offer a retired skill")
	}
	if got := svc.Mentioned("use $doomed"); len(got) != 0 {
		t.Fatalf("$mention resolved a retired skill: %+v", got)
	}
	// The builtins still match the query ("skill" is in their
	// descriptions); what must not come back is the retired one.
	if got := svc.Rank("doomed stale skill", 5, 0); hasSkill(got, "doomed") {
		t.Fatalf("ranking offered a retired skill: %+v", got)
	}

	// Un-retiring (here through the store, as the page does) makes it
	// available again on the next read; the retired cache must not keep
	// serving the set it built for the previous decision.
	if _, err := store.SetRetired(ctx, "doomed", "", false); err != nil {
		t.Fatal(err)
	}
	svc.Reload()
	if !hasSkill(svc.Available(), "doomed") {
		t.Fatal("restored skill is still hidden")
	}
}
