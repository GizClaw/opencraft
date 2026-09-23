package skills

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"

	skillusage "github.com/GizClaw/opencraft/internal/capabilities/skills/usage"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// newCurator wires the registry and the usage store the way the deploy
// does, with a fixed clock so the idle-day arithmetic is exact.
func newCurator(
	t *testing.T, store skillusage.Lifecycle, archiveDir string,
) (*Service, *Curator, time.Time) {
	t.Helper()
	svc := NewService(context.Background(), Options{Enabled: true})
	svc.SetLifecycle(store, curatorSettings(), archiveDir)
	curator := svc.Curator()
	if curator == nil {
		t.Fatal("curator not wired")
	}
	now := time.Now().UTC().Truncate(time.Second)
	curator.now = func() time.Time { return now }
	return svc, curator, now
}

// TestCuratorCandidatesUseThresholdsAndSkipBuiltins pins the two
// retirement conditions (idle past StaleAfterDays, used fewer than
// MinUses times) and the exclusion builtins are never part of.
func TestCuratorCandidatesUseThresholdsAndSkipBuiltins(t *testing.T) {
	_, root := userRoot(t)
	writeSkill(t, root, "stale",
		"name: stale\ndescription: rarely used skill\n")
	writeSkill(t, root, "busy",
		"name: busy\ndescription: well used skill\n")
	writeSkill(t, root, "fresh",
		"name: fresh\ndescription: just installed skill\n")
	store := newUsageStore(t)
	_, curator, now := newCurator(t, store, filepath.Join(t.TempDir(), "archive"))
	ctx := context.Background()

	// Used once, 60 days ago: idle past the threshold and below MinUses.
	if err := store.Record(ctx, skillusage.Event{
		Name: "stale", Scope: "user", UsedAt: now.AddDate(0, 0, -60),
	}); err != nil {
		t.Fatal(err)
	}
	// Used often enough, and its last use is just as old: the count wins
	// over the idle time.
	for i := 0; i < 3; i++ {
		if err := store.Record(ctx, skillusage.Event{
			Name: "busy", Scope: "user",
			UsedAt: now.AddDate(0, 0, -50-i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// "fresh" was never used; it ages from its own file time, so a newly
	// installed skill gets its grace period. The builtins (plan, review,
	// ...) are never candidates at all.

	candidates, err := curator.Candidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v, want only the stale user skill", candidates)
	}
	got := candidates[0]
	if got.Name != "stale" || got.Scope != "user" || got.Uses != 1 ||
		got.IdleDays != 60 {
		t.Fatalf("candidate = %+v", got)
	}
	if !strings.Contains(got.Reason, "1 use(s)") ||
		!strings.Contains(got.Reason, "60 days") {
		t.Fatalf("candidate reason = %q", got.Reason)
	}
}

// TestCuratorCandidatesSkipPinnedAndRetired covers the two decisions
// that must outrank the counters: a pinned skill is kept on purpose and
// a retired one is already archived.
func TestCuratorCandidatesSkipPinnedAndRetired(t *testing.T) {
	_, root := userRoot(t)
	for _, name := range []string{"kept", "archived"} {
		writeSkill(t, root, name,
			"name: "+name+"\ndescription: idle skill\n")
	}
	store := newUsageStore(t)
	_, curator, now := newCurator(t, store, filepath.Join(t.TempDir(), "archive"))
	ctx := context.Background()
	// Never used and untouched for 200 days: both would be candidates on
	// the counters alone.
	old := now.AddDate(0, 0, -200)
	for _, name := range []string{"kept", "archived"} {
		path := filepath.Join(root, name, "SKILL.md")
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	candidates, err := curator.Candidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates = %+v, want both idle skills", candidates)
	}

	if _, err := store.SetPinned(ctx, "kept", "user", true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetRetired(ctx, "archived", "user", true); err != nil {
		t.Fatal(err)
	}
	candidates, err = curator.Candidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates after decisions = %+v, want none", candidates)
	}
}

// TestCuratorRetireSnapshotsWithoutDeleting is the reversibility
// contract: retiring archives a skill and stops offering it, leaves
// every byte on disk, and a restore rebuilds the directory from the
// snapshot when it is gone.
func TestCuratorRetireSnapshotsWithoutDeleting(t *testing.T) {
	_, root := userRoot(t)
	skillPath := writeSkill(t, root, "doomed",
		"name: doomed\ndescription: stale skill\n")
	skillDir := filepath.Dir(skillPath)
	original, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatal(err)
	}
	store := newUsageStore(t)
	archiveDir := filepath.Join(t.TempDir(), "archive")
	svc, curator, _ := newCurator(t, store, archiveDir)
	ctx := context.Background()

	// Reading the offered list first warms the retired-name cache, so
	// the retirement below has to invalidate it to take effect.
	if !hasSkill(svc.Available(), "doomed") {
		t.Fatal("skill not discovered")
	}

	// Refusals first: a builtin is not the user's to retire, an unknown
	// name addresses nothing, and without an archive directory there is
	// no snapshot to fall back on.
	if _, err := curator.Retire(ctx, "review"); !errdefs.IsValidation(err) {
		t.Fatalf("Retire(builtin) = %v, want validation", err)
	}
	if _, err := curator.Retire(ctx, "nope"); !errdefs.IsNotFound(err) {
		t.Fatalf("Retire(unknown) = %v, want not found", err)
	}
	noArchive := NewService(ctx, Options{Enabled: true})
	noArchive.SetLifecycle(store, curatorSettings(), "")
	if _, err := noArchive.Curator().Retire(ctx, "doomed"); !errdefs.IsValidation(err) {
		t.Fatalf("Retire without archive dir = %v, want validation", err)
	}

	record, err := curator.Retire(ctx, "doomed")
	if err != nil {
		t.Fatal(err)
	}
	if record.Name != "doomed" || record.Scope != "user" ||
		record.SkillPath != skillDir || record.ArchivePath == "" {
		t.Fatalf("archive record = %+v", record)
	}
	// Retiring archives, it never deletes.
	current, err := os.ReadFile(skillPath)
	if err != nil || !bytes.Equal(current, original) {
		t.Fatalf("skill file after retire = %q, %v", current, err)
	}
	info, err := os.Stat(record.ArchivePath)
	if err != nil || info.Size() == 0 {
		t.Fatalf("snapshot %s = %v, %v", record.ArchivePath, info, err)
	}
	if filepath.Dir(record.ArchivePath) != archiveDir {
		t.Fatalf("snapshot landed in %q, want %q",
			filepath.Dir(record.ArchivePath), archiveDir)
	}

	states, err := store.States(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state := states["doomed"]; !state.Retired || state.Scope != "user" {
		t.Fatalf("state = %+v, want retired", state)
	}
	archives, err := curator.Archives(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(archives) != 1 || archives[0].ID != record.ID ||
		!archives[0].RestoredAt.IsZero() {
		t.Fatalf("archives = %+v", archives)
	}
	// The registry stops offering it everywhere it offers skills, while
	// List keeps it so the page can show and restore it.
	if hasSkill(svc.Available(), "doomed") {
		t.Fatal("retired skill still offered")
	}
	if got := svc.Mentioned("use $doomed"); len(got) != 0 {
		t.Fatalf("$mention resolved a retired skill: %+v", got)
	}
	// The builtins still match the query ("skill" is in their
	// descriptions); what must not come back is the retired one.
	if got := svc.Rank("doomed stale skill", 5, 0); hasSkill(got, "doomed") {
		t.Fatalf("ranking offered a retired skill: %+v", got)
	}
	if !hasSkill(svc.List(), "doomed") {
		t.Fatal("List must keep retired skills")
	}

	// A restore is what a lost directory recovers from (the curator
	// itself never removes it, so the test removes it by hand).
	if err := os.RemoveAll(skillDir); err != nil {
		t.Fatal(err)
	}
	restored, err := curator.Restore(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.ID != record.ID {
		t.Fatalf("Restore = %+v, want %s", restored, record.ID)
	}
	current, err = os.ReadFile(skillPath)
	if err != nil || !bytes.Equal(current, original) {
		t.Fatalf("restored file = %q, %v", current, err)
	}
	retired, err := store.Retired(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(retired) != 0 {
		t.Fatalf("restored skill still retired: %v", retired)
	}
	archived, err := store.GetArchive(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if archived.RestoredAt.IsZero() {
		t.Fatal("restore was not recorded")
	}
	if !hasSkill(svc.Available(), "doomed") {
		t.Fatal("restored skill is not offered again")
	}
	// Restoring the same record twice keeps the flag cleared and does
	// not re-extract over the live directory.
	if _, err := curator.Restore(ctx, record.ID); err != nil {
		t.Fatalf("second Restore = %v", err)
	}
}

func TestCuratorPruneBoundsUsageEvents(t *testing.T) {
	userRoot(t)
	store := newUsageStore(t)
	ctx := context.Background()
	_, curator, now := newCurator(t, store, filepath.Join(t.TempDir(), "archive"))

	for _, at := range []time.Time{
		now.AddDate(0, 0, -100),
		now.AddDate(0, 0, -10),
	} {
		if err := store.Record(ctx, skillusage.Event{
			Name: "review", UsedAt: at,
		}); err != nil {
			t.Fatal(err)
		}
	}
	pruned, err := curator.Prune(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 1 {
		t.Fatalf("Prune = %d, want the event outside the 90-day window", pruned)
	}
	stats, err := store.Stats(ctx, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].Uses != 1 {
		t.Fatalf("stats after prune = %+v, want one use", stats)
	}

	// A window of zero days is "no window configured": the curator must
	// not read that as "prune everything".
	noWindow := NewService(ctx, Options{Enabled: true})
	noWindow.SetLifecycle(store,
		config.SkillLifecycleConfig{Enabled: true},
		filepath.Join(t.TempDir(), "archive"))
	pruned, err = noWindow.Curator().Prune(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 0 {
		t.Fatalf("Prune with no window = %d, want 0", pruned)
	}
}
