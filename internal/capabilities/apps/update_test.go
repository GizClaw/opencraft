package apps

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// Updating is the second write the registry does, and the only one that
// touches content an application may already be serving. What these tests
// are about is what an update is *not* allowed to change: the state root
// (sessions, private workspace), the enable state, the source directory,
// and — until the new version has passed the same preflight an install
// runs — the version that is live.

// appAt is the fixture package at another version, with a file that names
// it: an update is only observable through the bytes the host would read,
// so the tests read them back.
func appAt(t *testing.T, version string) string {
	t.Helper()
	dir := newApp(t)
	writeTestFile(t, dir, ManifestFile,
		strings.Replace(fixtureManifest, "version: 0.1.0", "version: "+version, 1))
	writeTestFile(t, dir, "version.txt", version+"\n")
	return dir
}

// contentFile reads one file out of an installed content root, which is
// how a test says "this is the version that is live".
func contentFile(t *testing.T, store *Store, id, rel string) string {
	t.Helper()
	app, err := store.Get(id)
	if err != nil {
		t.Fatalf("get %q: %v", id, err)
	}
	raw, err := os.ReadFile(filepath.Join(app.ContentDir, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(raw)
}

// stateFileOf writes one file into an application's state root, standing
// in for the sessions and the private workspace an update must not touch.
func stateFileOf(t *testing.T, store *Store, dataDir, id, rel, data string) {
	t.Helper()
	root, err := store.stateRoot(id)
	if err != nil {
		t.Fatalf("state root: %v", err)
	}
	want, err := config.AppStateRoot(dataDir, id)
	if err != nil {
		t.Fatalf("layout state root: %v", err)
	}
	if root != want {
		t.Fatalf("state root = %q, want %q", root, want)
	}
	writeTestFile(t, root, rel, data)
}

// summaryOf reads one application out of the list, which is where the
// page gets the card it renders — including whether a rollback is
// available.
func listSummary(t *testing.T, store *Store, id string) Summary {
	t.Helper()
	list, err := store.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, sum := range list {
		if sum.ID == id {
			return sum
		}
	}
	t.Fatalf("%q is not in the list: %+v", id, list)
	return Summary{}
}

// TestUpdateSwapsContentAndKeepsTheStateRoot is the whole contract in one
// test: the new version is live, the snapshot holds the old one, and the
// application's data — plus the user's source directory — is exactly where
// it was.
func TestUpdateSwapsContentAndKeepsTheStateRoot(t *testing.T) {
	store, root, dataDir := newStore(t)
	if _, err := store.Install(context.Background(), appAt(t, "0.1.0"), InstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	stateFileOf(t, store, dataDir, "hello", "sessions/keep.txt", "a session that must survive")
	src := appAt(t, "0.2.0")
	before := snapshotTree(t, src)

	sum, err := store.Update(context.Background(), "hello", src)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if sum.Version != "0.2.0" || !sum.Enabled || !sum.CanRollback {
		t.Fatalf("summary = %+v", sum)
	}
	if got := contentFile(t, store, "hello", "version.txt"); got != "0.2.0\n" {
		t.Fatalf("the live content says %q", got)
	}
	m, err := store.Manifest("hello")
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if m.Version != "0.2.0" {
		t.Fatalf("the live manifest says %s", m.Version)
	}
	// The snapshot is the version the update replaced, byte for byte.
	if got, err := os.ReadFile(
		filepath.Join(store.backupDir("hello"), "version.txt"),
	); err != nil || string(got) != "0.1.0\n" {
		t.Fatalf("snapshot content = %q, %v", got, err)
	}
	if got := listSummary(t, store, "hello").CanRollback; !got {
		t.Fatal("the card cannot offer the rollback the disk has")
	}
	if raw, err := os.ReadFile(
		filepath.Join(dataDir, "apps", "hello", "sessions", "keep.txt"),
	); err != nil || string(raw) != "a session that must survive" {
		t.Fatalf("state root after the update: %q, %v", raw, err)
	}
	mustBeUntouched(t, src, before)
	// Nothing of the staging or snapshot bookkeeping is left behind, and
	// the content root is not inside any of it.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 2 || names[0] != ".backups" || names[1] != "hello" {
		t.Fatalf("content root holds %v, want the install and its snapshot", names)
	}
	snapshots, err := os.ReadDir(filepath.Join(root, ".backups"))
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 || snapshots[0].Name() != "hello" {
		t.Fatalf(".backups holds %v, want one snapshot", snapshots)
	}
}

// TestUpdateRefusesAPackageThatIsNotNewer keeps the version rule the
// plugin registry has: an update moves forward, and the refusal names both
// versions so a user who picked the wrong directory can see why.
func TestUpdateRefusesAPackageThatIsNotNewer(t *testing.T) {
	store, _, _ := newStore(t)
	if _, err := store.Install(context.Background(), appAt(t, "0.2.0"), InstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	for _, tc := range []struct{ name, version string }{
		{"the same version", "0.2.0"},
		{"an older version", "0.1.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.Update(context.Background(), "hello", appAt(t, tc.version))
			if err == nil {
				t.Fatalf("update to %s was accepted", tc.version)
			}
			if !strings.Contains(err.Error(), tc.version) ||
				!strings.Contains(err.Error(), "0.2.0") {
				t.Fatalf("refusal does not name both versions: %v", err)
			}
		})
	}
	if got := contentFile(t, store, "hello", "version.txt"); got != "0.2.0\n" {
		t.Fatalf("a refused update moved the content: %q", got)
	}
	if got := listSummary(t, store, "hello").CanRollback; got {
		t.Fatal("a refused update left a snapshot behind")
	}
}

// TestUpdateRefusesAnotherApplication is the id guard: the package names
// the application it updates, and an update never becomes an install.
func TestUpdateRefusesAnotherApplication(t *testing.T) {
	store, root, _ := newStore(t)
	if _, err := store.Install(context.Background(), appAt(t, "0.1.0"), InstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	src := appAt(t, "0.2.0")
	writeTestFile(t, src, ManifestFile,
		strings.Replace(fixtureManifest, "id: hello", "id: other", 1))
	_, err := store.Update(context.Background(), "hello", src)
	if err == nil || !strings.Contains(err.Error(), "other") {
		t.Fatalf("update with a foreign id: %v", err)
	}
	mustNotExist(t, filepath.Join(root, "other"))
	if got := contentFile(t, store, "hello", "version.txt"); got != "0.1.0\n" {
		t.Fatalf("content = %q", got)
	}
}

// TestUpdateRefusesAPackageThePreflightRefuses is the staging guarantee:
// the new version is validated on the bytes that would land, *before* the
// swap, so a package the preflight refuses leaves the installed version
// serving and no snapshot behind.
func TestUpdateRefusesAPackageThePreflightRefuses(t *testing.T) {
	store, _, _ := newStore(t)
	if _, err := store.Install(context.Background(), appAt(t, "0.1.0"), InstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	src := appAt(t, "0.2.0")
	// A reference that leaves the content root: valid YAML, refused by
	// the preflight wherever it is read from.
	writeTestFile(t, src, "layer.yaml", strings.Replace(
		fixtureLayer, "graph: { file: graph.yaml }",
		"graph: { file: ../../outside.yaml }", 1))
	_, err := store.Update(context.Background(), "hello", src)
	var refusals Refusals
	if !errors.As(err, &refusals) {
		t.Fatalf("update = %v, want the preflight's refusals", err)
	}
	if got := contentFile(t, store, "hello", "version.txt"); got != "0.1.0\n" {
		t.Fatalf("the refused version is live: %q", got)
	}
	if got := listSummary(t, store, "hello").CanRollback; got {
		t.Fatal("a refused update left a snapshot behind")
	}
	if _, err := store.Get("hello"); err != nil {
		t.Fatalf("the installed version no longer reads: %v", err)
	}
}

// TestUpdateRepairsAnUnreadableManifest is the one exception to the
// version rule: an install whose manifest cannot be read has no version
// to compare against and nothing to keep, so an update over it is the
// repair the card's error asks for.
func TestUpdateRepairsAnUnreadableManifest(t *testing.T) {
	store, _, _ := newStore(t)
	if _, err := store.Install(context.Background(), appAt(t, "0.2.0"), InstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	writeTestFile(t, storeContentDir(t, store, "hello"), ManifestFile, "app: v1\n")
	if _, err := store.Get("hello"); err == nil {
		t.Fatal("the broken install still reads")
	}
	if got := listSummary(t, store, "hello").Error; got == "" {
		t.Fatal("the list does not report the broken manifest")
	}
	if _, err := store.Update(context.Background(), "hello", appAt(t, "0.2.0")); err != nil {
		t.Fatalf("repair update: %v", err)
	}
	if got := contentFile(t, store, "hello", "version.txt"); got != "0.2.0\n" {
		t.Fatalf("repaired content = %q", got)
	}
	if _, err := store.Get("hello"); err != nil {
		t.Fatalf("the repaired install does not read: %v", err)
	}
}

// TestUpdateRequiresTheHostTheManifestNames is the upgrade gate on the
// update path: a package for a newer host is refused before anything is
// staged.
func TestUpdateRequiresTheHostTheManifestNames(t *testing.T) {
	store, _, _ := newStore(t)
	if _, err := store.Install(context.Background(), appAt(t, "0.1.0"), InstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	src := appAt(t, "0.2.0")
	writeTestFile(t, src, ManifestFile,
		strings.Replace(fixtureManifest, "version: 0.1.0",
			"version: 0.2.0\nminHostVersion: 9.9.9", 1))
	_, err := store.Update(context.Background(), "hello", src)
	if err == nil || !strings.Contains(err.Error(), "9.9.9") {
		t.Fatalf("update for a newer host: %v", err)
	}
}

// TestUpdateRefusesABuiltin: a built-in application's content lives in the
// read-only bundle, so there is nothing to replace and the caller is told
// to install the package under an id of its own.
func TestUpdateRefusesABuiltin(t *testing.T) {
	root, dataDir, builtin := t.TempDir(), t.TempDir(), t.TempDir()
	store := NewStore(Options{Root: root, DataDir: dataDir, Builtin: builtin, HostVersion: "0.1.0"})
	writeTestFile(t, builtin, "hello/content/"+ManifestFile, fixtureManifest)
	if _, err := store.Update(context.Background(), "hello", appAt(t, "0.2.0")); err == nil ||
		!strings.Contains(err.Error(), "built in") {
		t.Fatalf("update of a built-in: %v", err)
	}
	if _, err := store.Rollback(context.Background(), "hello"); err == nil ||
		!strings.Contains(err.Error(), "built in") {
		t.Fatalf("rollback of a built-in: %v", err)
	}
}

// TestRollbackRestoresThePreviousVersion: one update back, the snapshot is
// consumed, the data stays, and a second rollback has nothing to do.
func TestRollbackRestoresThePreviousVersion(t *testing.T) {
	store, _, dataDir := newStore(t)
	if _, err := store.Install(context.Background(), appAt(t, "0.1.0"), InstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	stateFileOf(t, store, dataDir, "hello", "workspace/notes.md", "written by the app")
	if _, err := store.Update(context.Background(), "hello", appAt(t, "0.2.0")); err != nil {
		t.Fatalf("update: %v", err)
	}
	sum, err := store.Rollback(context.Background(), "hello")
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if sum.Version != "0.1.0" || sum.CanRollback {
		t.Fatalf("summary = %+v", sum)
	}
	if got := contentFile(t, store, "hello", "version.txt"); got != "0.1.0\n" {
		t.Fatalf("rolled-back content = %q", got)
	}
	if raw, err := os.ReadFile(
		filepath.Join(dataDir, "apps", "hello", "workspace", "notes.md"),
	); err != nil || string(raw) != "written by the app" {
		t.Fatalf("state root after the rollback: %q, %v", raw, err)
	}
	if _, err := store.Rollback(context.Background(), "hello"); err == nil ||
		!strings.Contains(err.Error(), "no update") {
		t.Fatalf("second rollback: %v", err)
	}
	if entries, err := os.ReadDir(filepath.Join(store.root, ".backups")); err != nil ||
		len(entries) != 0 {
		t.Fatalf("snapshots left behind: %v, %v", entries, err)
	}
}

// TestRollbackValidatesTheSnapshotBeforeItMoves: a snapshot that cannot
// run any more is refused where it lies, so a rollback never trades a
// working application for a broken one.
func TestRollbackValidatesTheSnapshotBeforeItMoves(t *testing.T) {
	store, _, _ := newStore(t)
	if _, err := store.Install(context.Background(), appAt(t, "0.1.0"), InstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := store.Update(context.Background(), "hello", appAt(t, "0.2.0")); err != nil {
		t.Fatalf("update: %v", err)
	}
	writeTestFile(t, store.backupDir("hello"), "layer.yaml", strings.Replace(
		fixtureLayer, "graph: { file: graph.yaml }",
		"graph: { file: ../../outside.yaml }", 1))
	if _, err := store.Rollback(context.Background(), "hello"); err == nil {
		t.Fatal("a snapshot the preflight refuses was restored")
	}
	if got := contentFile(t, store, "hello", "version.txt"); got != "0.2.0\n" {
		t.Fatalf("the live version changed anyway: %q", got)
	}
}

// TestRollbackRepairsABrokenInstall is the other direction of the repair
// path: the snapshot is read on its own, so an install whose manifest went
// bad can be restored to the version that was running before.
func TestRollbackRepairsABrokenInstall(t *testing.T) {
	store, _, _ := newStore(t)
	if _, err := store.Install(context.Background(), appAt(t, "0.1.0"), InstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := store.Update(context.Background(), "hello", appAt(t, "0.2.0")); err != nil {
		t.Fatalf("update: %v", err)
	}
	writeTestFile(t, storeContentDir(t, store, "hello"), ManifestFile, "app: v1\n")
	if _, err := store.Rollback(context.Background(), "hello"); err != nil {
		t.Fatalf("rollback over a broken install: %v", err)
	}
	if got := contentFile(t, store, "hello", "version.txt"); got != "0.1.0\n" {
		t.Fatalf("rolled-back content = %q", got)
	}
}

// TestUpdateKeepsTheEnableState: an update is not an install, so it does
// not enable what the user turned off, and a rollback does not either.
func TestUpdateKeepsTheEnableState(t *testing.T) {
	store, _, _ := newStore(t)
	if _, err := store.Install(context.Background(), appAt(t, "0.1.0"), InstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := store.SetEnabled("hello", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	sum, err := store.Update(context.Background(), "hello", appAt(t, "0.2.0"))
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if sum.Enabled {
		t.Fatal("the update enabled a disabled application")
	}
	sum, err = store.Rollback(context.Background(), "hello")
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if sum.Enabled || listSummary(t, store, "hello").Enabled {
		t.Fatal("the rollback enabled a disabled application")
	}
}

// TestUpdateZipIsTheDirectoryUpdate: a release artifact installs the same
// package a directory does, entry point and all.
func TestUpdateZipIsTheDirectoryUpdate(t *testing.T) {
	store, _, _ := newStore(t)
	if _, err := store.Install(context.Background(), appAt(t, "0.1.0"), InstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	zipPath := zipTree(t, appAt(t, "0.2.0"), "hello/")
	if _, err := store.UpdateZip(context.Background(), "hello", zipPath); err != nil {
		t.Fatalf("update zip: %v", err)
	}
	if got := contentFile(t, store, "hello", "version.txt"); got != "0.2.0\n" {
		t.Fatalf("content = %q", got)
	}
	if _, err := store.UpdateZip(context.Background(), "hello", zipPath); err == nil {
		t.Fatal("the same archive updated twice")
	}
}

// TestUninstallDropsTheSnapshot: unloading an application takes its
// rollback snapshot with it — a version of an application that is no
// longer installed is not a version anyone can go back to.
func TestUninstallDropsTheSnapshot(t *testing.T) {
	store, _, _ := newStore(t)
	if _, err := store.Install(context.Background(), appAt(t, "0.1.0"), InstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := store.Update(context.Background(), "hello", appAt(t, "0.2.0")); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := store.Uninstall("hello", false); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	mustNotExist(t, store.backupDir("hello"))
}

// storeContentDir is the installed content root of one application, for
// tests that break it on purpose.
func storeContentDir(t *testing.T, store *Store, id string) string {
	t.Helper()
	app, err := store.Get(id)
	if err != nil {
		t.Fatalf("get %q: %v", id, err)
	}
	return app.ContentDir
}
