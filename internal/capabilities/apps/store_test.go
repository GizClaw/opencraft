package apps

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The registry's two trees, and the rule that keeps them apart:
// <root>/<id>/content is everything the package brought, and the state
// root — <dataDir>/apps/<id>, or <root>/<id> in the single-root layout —
// is everything the runtime wrote. Every test below installs the fixture
// application from validate_test.go, so a change to one of these layouts
// fails here.

// newStore returns a store over a fresh content root with the state root
// under a separate data dir, and both paths.
func newStore(t *testing.T) (*Store, string, string) {
	t.Helper()
	root, dataDir := t.TempDir(), t.TempDir()
	return NewStore(Options{
		Root:        root,
		DataDir:     dataDir,
		HostVersion: "0.1.0",
	}), root, dataDir
}

// stateRootOf names the state root the two-root layout gives one
// application. It is spelled out rather than computed from config so a
// moved state dir fails a test instead of silently following along.
func stateRootOf(dataDir, id string) string {
	return filepath.Join(dataDir, "apps", id)
}

// install installs one package and fails the test when it was refused.
func install(t *testing.T, s *Store, src string) Summary {
	t.Helper()
	sum, err := s.Install(context.Background(), src, InstallOptions{})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	return sum
}

// builtinApp writes the fixture application into a read-only-by-layout
// built-in root and returns that root.
func builtinApp(t *testing.T, id string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, id, "content")
	writeTestFile(t, dir, ManifestFile, strings.Replace(fixtureManifest, "hello", id, 1))
	writeTestFile(t, dir, "layer.yaml", fixtureLayer)
	writeTestFile(t, dir, "graph.yaml", fixtureGraph)
	writeTestFile(t, dir, "nodes/hello.js", fixtureScript)
	return root
}

// mustExist fails the test when a path is not there.
func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("%s: %v", path, err)
	}
}

// mustNotExist fails the test when a path is there.
func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s is there, want nothing at that path", path)
	}
}

// mustBeEmpty fails the test when a directory holds anything. It is how
// the read-only paths state "wrote nothing": the directory itself is a
// temp dir the test made, its contents are the registry's doing.
func mustBeEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Errorf("%s holds %v, want nothing written there", dir, names)
	}
}

// TestInstallLaysOutTheContentRootUnderTheRegistry pins the install
// layout: the package lands in <root>/<id>/content, the runtime's
// directories are nowhere near it, and reading it back reports the
// manifest facts.
func TestInstallLaysOutTheContentRootUnderTheRegistry(t *testing.T) {
	store, root, dataDir := newStore(t)
	sum := install(t, store, newApp(t))
	if sum.ID != "hello" || sum.Name != "Hello" || !sum.Enabled || sum.Error != "" {
		t.Fatalf("summary = %+v", sum)
	}
	content := filepath.Join(root, "hello", "content")
	for _, rel := range []string{ManifestFile, "layer.yaml", "graph.yaml", "nodes/hello.js"} {
		mustExist(t, filepath.Join(content, rel))
	}
	// Nothing of the runtime's is inside the content root, and nothing
	// of the package's is in the state root.
	mustNotExist(t, filepath.Join(content, "workspace"))
	mustNotExist(t, filepath.Join(content, "sessions"))
	mustNotExist(t, filepath.Join(root, "hello", "workspace"))
	mustNotExist(t, stateRootOf(dataDir, "hello"))
	// An installed application is enabled by absence of a state entry,
	// so the install writes no state file at all.
	mustNotExist(t, filepath.Join(root, stateFile))

	app, err := store.Get("hello")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if app.ContentDir != content {
		t.Errorf("ContentDir = %q, want %q", app.ContentDir, content)
	}
	if len(app.Layers) != 1 || app.Layers[0] != "layer.yaml" || app.Agent != DefaultAgent {
		t.Errorf("app = %+v", app)
	}
	if !app.Enabled || app.Builtin {
		t.Errorf("Enabled/Builtin = %v/%v", app.Enabled, app.Builtin)
	}
	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != "hello" || !list[0].Enabled || list[0].Builtin {
		t.Fatalf("list = %+v", list)
	}
	if m, err := store.Manifest("hello"); err != nil || m.ID != "hello" {
		t.Errorf("Manifest = %+v, %v", m, err)
	}
	if _, err := store.Get("nope"); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("Get of an id that is not installed = %v", err)
	}
}

// TestInstalledAppCarriesTheManifestsRunDefaults: the registry is where
// an assembly reads an application from, so the defaults a manifest
// declares have to be on the App the host gets — the host applies them
// where a turn starts, and an App without them would leave every session
// starting on the host's own defaults instead.
func TestInstalledAppCarriesTheManifestsRunDefaults(t *testing.T) {
	store, _, _ := newStore(t)
	src := newApp(t)
	writeTestFile(t, src, ManifestFile, fixtureManifest+
		"defaults:\n  model: openai-1/fake-model\n  think_level: high\n")
	install(t, store, src)

	app, err := store.Get("hello")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if app.Defaults.Model != "openai-1/fake-model" || app.Defaults.ThinkLevel != "high" {
		t.Errorf("Defaults = %+v, want the manifest's own", app.Defaults)
	}
	m, err := store.Manifest("hello")
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	if m.Defaults == nil || m.Defaults.Model != app.Defaults.Model {
		t.Errorf("Manifest defaults = %+v, want the same as App's", m.Defaults)
	}
}

// TestListOnAStoreThatWasNeverWritten stays empty: a read of a machine
// that installed nothing is a read of an empty registry, not an error
// and not a directory this code creates.
func TestListOnAStoreThatWasNeverWritten(t *testing.T) {
	root := filepath.Join(t.TempDir(), "apps")
	store := NewStore(Options{Root: root, DataDir: t.TempDir()})
	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("list = %+v, want empty", list)
	}
	mustNotExist(t, root)
}

// TestInstallRefusesAPackageThePreflightRefusesAndLeavesNothingBehind:
// the preflight runs over the staged copy, so a refused package changes
// nothing on disk — not even a staging directory.
func TestInstallRefusesAPackageThePreflightRefusesAndLeavesNothingBehind(t *testing.T) {
	src := newApp(t)
	writeTestFile(t, src, "layer.yaml", `resources:
  events:
    kind: event.Bus
`)
	store, root, _ := newStore(t)
	_, err := store.Install(context.Background(), src, InstallOptions{})
	var refusals Refusals
	if !errors.As(err, &refusals) {
		t.Fatalf("install error = %v, want a refusal", err)
	}
	refusedEvery(t, refusals, "layer.yaml: resources.events: the host provides")

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read the content root: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("the refused install left %d entries in the content root", len(entries))
	}
	if list, err := store.List(); err != nil || len(list) != 0 {
		t.Errorf("List after a refused install = %+v, %v", list, err)
	}
}

// TestInstallRefusesWhatItCannotCopy covers the sources the registry
// turns away before it reads a manifest: a second copy of an id it
// already has, a directory inside the registry itself, and a directory
// that is not a package.
func TestInstallRefusesWhatItCannotCopy(t *testing.T) {
	t.Run("the same id twice", func(t *testing.T) {
		store, _, _ := newStore(t)
		install(t, store, newApp(t))
		_, err := store.Install(context.Background(), newApp(t), InstallOptions{})
		if err == nil || !strings.Contains(err.Error(), "is already installed; uninstall it first") {
			t.Fatalf("second install = %v", err)
		}
	})
	t.Run("a source inside the registry", func(t *testing.T) {
		store, root, _ := newStore(t)
		src := filepath.Join(root, "downloads", "hello")
		if err := os.MkdirAll(src, 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := store.Install(context.Background(), src, InstallOptions{})
		if err == nil || !strings.Contains(err.Error(), "is inside the application root") {
			t.Fatalf("install from inside the registry = %v", err)
		}
	})
	t.Run("a directory that is not a package", func(t *testing.T) {
		src := t.TempDir()
		writeTestFile(t, src, "layer.yaml", fixtureLayer)
		store, _, _ := newStore(t)
		_, err := store.Install(context.Background(), src, InstallOptions{})
		if err == nil || !strings.Contains(err.Error(), "app.yaml is missing") {
			t.Fatalf("install without a manifest = %v", err)
		}
	})
}

// TestInstallChecksTheManifestAgainstTheHostVersion: an application that
// wants a newer host is refused with both versions named, and a host
// that does not know its own version does not pretend to.
func TestInstallChecksTheManifestAgainstTheHostVersion(t *testing.T) {
	src := newApp(t)
	writeTestFile(t, src, ManifestFile, fixtureManifest+"minHostVersion: 2.0.0\n")
	store, _, _ := newStore(t)
	_, err := store.Install(context.Background(), src, InstallOptions{})
	if err == nil || !strings.Contains(err.Error(), "requires host 2.0.0, running 0.1.0") {
		t.Fatalf("install of a newer-host package = %v", err)
	}
	unknown := NewStore(Options{Root: t.TempDir(), DataDir: t.TempDir()})
	install(t, unknown, src)
}

// TestInstallCopiesPackagesTheWayItPromised: the copier skips dotfiles
// (a manifest may not name one as a layer, and the copy agrees) and
// refuses symbolic links rather than following them, so "inside the
// content root" means the same thing before and after the install.
func TestInstallCopiesPackagesTheWayItPromised(t *testing.T) {
	src := newApp(t)
	writeTestFile(t, src, ".git/config", "[core]\n")
	writeTestFile(t, src, "notes/.DS_Store", "junk\n")
	elsewhere := filepath.Join(t.TempDir(), "elsewhere.yaml")
	writeTestFile(t, filepath.Dir(elsewhere), filepath.Base(elsewhere), "name: elsewhere\n")
	if err := os.Symlink(elsewhere, filepath.Join(src, "extra.yaml")); err != nil {
		t.Fatal(err)
	}
	store, root, _ := newStore(t)
	_, err := store.Install(context.Background(), src, InstallOptions{})
	if err == nil || !strings.Contains(err.Error(), "extra.yaml is a symbolic link") {
		t.Fatalf("install of a package with a link = %v", err)
	}
	mustNotExist(t, filepath.Join(root, "hello"))

	// The same package without the link installs, and carries no
	// dotfiles.
	if err := os.Remove(filepath.Join(src, "extra.yaml")); err != nil {
		t.Fatal(err)
	}
	install(t, store, src)
	content := filepath.Join(root, "hello", "content")
	mustNotExist(t, filepath.Join(content, ".git"))
	mustNotExist(t, filepath.Join(content, "notes", ".DS_Store"))
	mustExist(t, filepath.Join(content, "notes"))
}

// TestSetEnabledRecordsTheChoiceAndOnlyWhenItChanges pins the state file
// as a record of *deviations*: enabled is the absence of an entry, so a
// call that changes nothing leaves the file alone.
func TestSetEnabledRecordsTheChoiceAndOnlyWhenItChanges(t *testing.T) {
	store, root, _ := newStore(t)
	install(t, store, newApp(t))
	path := filepath.Join(root, stateFile)
	mustNotExist(t, path)

	if err := store.SetEnabled("hello", false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the state file: %v", err)
	}
	if !strings.Contains(string(raw), `"hello": false`) {
		t.Errorf("state file = %s", raw)
	}
	if app, err := store.Get("hello"); err != nil || app.Enabled {
		t.Errorf("after disabling: %+v, %v", app, err)
	}

	// A repeat call writes nothing: the file is left exactly as found,
	// so a backend that rewrites its formatting would fail here.
	if err := os.WriteFile(path, []byte("{\"hello\":false}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.SetEnabled("hello", false); err != nil {
		t.Fatalf("SetEnabled (no change): %v", err)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{\"hello\":false}\n" {
		t.Errorf("a no-op call rewrote the state file: %s", raw)
	}

	if err := store.SetEnabled("hello", true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if app, err := store.Get("hello"); err != nil || !app.Enabled {
		t.Errorf("after enabling: %+v, %v", app, err)
	}
	if err := store.SetEnabled("nope", true); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("SetEnabled of an id that is not installed = %v", err)
	}
}

// TestUninstallKeepsTheStateUnlessItIsPurged is the rule the whole split
// exists for: removing an application removes its content, and its
// sessions and private workspace stay until the caller says purge.
func TestUninstallKeepsTheStateUnlessItIsPurged(t *testing.T) {
	store, root, dataDir := newStore(t)
	install(t, store, newApp(t))
	state := stateRootOf(dataDir, "hello")
	writeTestFile(t, state, "workspace/note.txt", "a turn wrote this\n")
	writeTestFile(t, state, "sessions/3f/session.db", "the transcript\n")

	if err := store.Uninstall("hello", false); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	mustNotExist(t, filepath.Join(root, "hello"))
	mustExist(t, filepath.Join(state, "workspace", "note.txt"))
	mustExist(t, filepath.Join(state, "sessions", "3f", "session.db"))
	if _, err := store.Get("hello"); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("Get after uninstall = %v", err)
	}
	if list, err := store.List(); err != nil || len(list) != 0 {
		t.Errorf("List after uninstall = %+v, %v", list, err)
	}

	// Reinstalling is possible — the content root is free — and purge is
	// what removes the data that survived it.
	install(t, store, newApp(t))
	if err := store.Uninstall("hello", true); err != nil {
		t.Fatalf("Uninstall (purge): %v", err)
	}
	mustNotExist(t, state)
	mustNotExist(t, filepath.Join(root, "hello"))
}

// TestUninstallForgetsTheChoice: the enable state belongs to the
// installation, so uninstalling leaves no entry behind to be inherited
// by a later install of the same id.
func TestUninstallForgetsTheChoice(t *testing.T) {
	store, root, _ := newStore(t)
	install(t, store, newApp(t))
	if err := store.SetEnabled("hello", false); err != nil {
		t.Fatal(err)
	}
	if err := store.Uninstall("hello", false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, stateFile))
	if err != nil {
		t.Fatalf("read the state file: %v", err)
	}
	if strings.Contains(string(raw), "hello") {
		t.Errorf("state file still mentions the uninstalled id: %s", raw)
	}
}

// TestUninstallRefusesABuiltin: the built-in root is read-only, so the
// only honest answer is to disable the application instead.
func TestUninstallRefusesABuiltin(t *testing.T) {
	builtin := builtinApp(t, "hello")
	store := NewStore(Options{Root: t.TempDir(), DataDir: t.TempDir(), Builtin: builtin})
	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || !list[0].Builtin {
		t.Fatalf("list = %+v, want the built-in application", list)
	}
	app, err := store.Get("hello")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if app.ContentDir != filepath.Join(builtin, "hello", "content") || !app.Builtin {
		t.Errorf("built-in app = %+v", app)
	}
	if err := store.Uninstall("hello", false); err == nil ||
		!strings.Contains(err.Error(), "is built in and cannot be uninstalled") {
		t.Fatalf("Uninstall of a built-in = %v", err)
	}
	if err := store.SetEnabled("hello", false); err != nil {
		t.Fatalf("disabling a built-in: %v", err)
	}
	if app, err := store.Get("hello"); err != nil || app.Enabled {
		t.Errorf("after disabling the built-in: %+v, %v", app, err)
	}
}

// TestAUserInstallShadowsTheBuiltin: one id resolves to the user's
// content when there is any, which is what makes an install an override.
func TestAUserInstallShadowsTheBuiltin(t *testing.T) {
	builtin := builtinApp(t, "hello")
	root := t.TempDir()
	store := NewStore(Options{Root: root, DataDir: t.TempDir(), Builtin: builtin})
	install(t, store, newApp(t))
	app, err := store.Get("hello")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if app.Builtin || app.ContentDir != filepath.Join(root, "hello", "content") {
		t.Errorf("app = %+v, want the user's copy", app)
	}
	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Builtin {
		t.Errorf("list = %+v, want one shadowing entry", list)
	}
}

// TestBrokenInstallsStayVisible covers the two ways an install stops
// being usable without disappearing: a manifest that no longer parses
// and one edited into another identity. Both must be reported, not
// skipped — the list is the only way to repair them.
func TestBrokenInstallsStayVisible(t *testing.T) {
	t.Run("a manifest that does not parse", func(t *testing.T) {
		store, root, _ := newStore(t)
		install(t, store, newApp(t))
		writeTestFile(t, filepath.Join(root, "hello", "content"), ManifestFile,
			"app: v1\nid: hello\nname: \"\"\nversion: 0.1.0\nlayers:\n  - layer.yaml\n")
		list, err := store.List()
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(list) != 1 || list[0].ID != "hello" ||
			!strings.Contains(list[0].Error, "name is required") {
			t.Fatalf("list = %+v", list)
		}
		if _, err := store.Get("hello"); err == nil {
			t.Error("Get accepted a broken manifest")
		}
	})
	t.Run("a manifest re-homed to another id", func(t *testing.T) {
		store, root, _ := newStore(t)
		install(t, store, newApp(t))
		writeTestFile(t, filepath.Join(root, "hello", "content"), ManifestFile,
			"app: v1\nid: other\nname: Hello\nversion: 0.1.0\nlayers:\n  - layer.yaml\n")
		_, err := store.Get("hello")
		if err == nil || !strings.Contains(err.Error(),
			`declares id "other" but is installed as "hello"`) {
			t.Fatalf("Get = %v", err)
		}
		list, err := store.List()
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(list) != 1 || !strings.Contains(list[0].Error, "declares id") {
			t.Fatalf("list = %+v", list)
		}
	})
}

// TestListIgnoresStateRootsAndUnnameableDirectories: an application is
// its content root, so a directory that only holds state (an uninstall
// with the data kept) or a name that is not an id is not one.
func TestListIgnoresStateRootsAndUnnameableDirectories(t *testing.T) {
	store, root, _ := newStore(t)
	writeTestFile(t, root, "leftover/workspace/note.txt", "state without content\n")
	writeTestFile(t, root, "NotAnID/content/"+ManifestFile, fixtureManifest)
	writeTestFile(t, root, ".hidden/content/"+ManifestFile, fixtureManifest)
	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("list = %+v, want empty", list)
	}
}

// TestTheSingleRootLayoutKeepsStateOnUninstall pins the layout a launch
// without a separate data dir gets: content and state share <root>/<id>,
// and an uninstall still removes only the content half of it.
func TestTheSingleRootLayoutKeepsStateOnUninstall(t *testing.T) {
	root := t.TempDir()
	store := NewStore(Options{Root: root})
	install(t, store, newApp(t))
	content := filepath.Join(root, "hello", "content")
	state := filepath.Join(root, "hello")
	writeTestFile(t, state, "workspace/note.txt", "a turn wrote this\n")

	if err := store.Uninstall("hello", false); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	mustNotExist(t, content)
	mustExist(t, filepath.Join(state, "workspace", "note.txt"))
	if list, err := store.List(); err != nil || len(list) != 0 {
		t.Errorf("List after uninstall = %+v, %v", list, err)
	}

	// The content root is free again, and purge is what removes what the
	// uninstall kept.
	install(t, store, newApp(t))
	if err := store.Uninstall("hello", true); err != nil {
		t.Fatalf("Uninstall (purge): %v", err)
	}
	mustNotExist(t, state)
}

// TestStoreWithoutAContentRootRefusesToWrite: the registry is useless
// without a root, and it says so instead of writing somewhere else.
func TestStoreWithoutAContentRootRefusesToWrite(t *testing.T) {
	store := NewStore(Options{})
	if _, err := store.Install(context.Background(), newApp(t), InstallOptions{}); err == nil ||
		!strings.Contains(err.Error(), "content root is not configured") {
		t.Fatalf("Install without a content root = %v", err)
	}
	if _, err := store.Get("hello"); err == nil ||
		!strings.Contains(err.Error(), "content root is not configured") {
		t.Fatalf("Get without a content root = %v", err)
	}
	if list, err := store.List(); err != nil || len(list) != 0 {
		t.Errorf("List without a content root = %+v, %v", list, err)
	}
}
